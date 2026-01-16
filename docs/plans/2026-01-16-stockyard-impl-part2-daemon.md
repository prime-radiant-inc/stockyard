# Stockyard Implementation - Part 2: Daemon & gRPC (Phases 4-6)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the daemon foundation, gRPC API, and basic CLI commands.

**Reference:** Design doc at `docs/plans/2026-01-16-stockyard-design.md`

---

## Phase 4: Daemon Foundation

### Task 4.1: Create State Management

**Files:**
- Create: `pkg/daemon/state.go`
- Create: `pkg/daemon/state_test.go`

**Step 1: Write failing test**

```go
// pkg/daemon/state_test.go
package daemon

import (
    "testing"
    "time"
)

func TestState_CreateAndGetTask(t *testing.T) {
    state, err := NewStateInMemory()
    if err != nil {
        t.Fatalf("failed to create state: %v", err)
    }
    defer state.Close()

    task := &Task{
        ID:        "task-123",
        Name:      "test-task",
        Repo:      "github.com/test/repo",
        Ref:       "main",
        Command:   "claude-code",
        Status:    "running",
        CreatedAt: time.Now(),
    }

    err = state.CreateTask(task)
    if err != nil {
        t.Fatalf("failed to create task: %v", err)
    }

    got, err := state.GetTask("task-123")
    if err != nil {
        t.Fatalf("failed to get task: %v", err)
    }

    if got.ID != task.ID {
        t.Errorf("ID mismatch: got %q, want %q", got.ID, task.ID)
    }
    if got.Status != "running" {
        t.Errorf("Status mismatch: got %q, want %q", got.Status, "running")
    }
}

func TestState_ListTasks(t *testing.T) {
    state, err := NewStateInMemory()
    if err != nil {
        t.Fatalf("failed to create state: %v", err)
    }
    defer state.Close()

    // Create two tasks
    state.CreateTask(&Task{ID: "task-1", Repo: "repo1", Ref: "main", Command: "cmd", Status: "running", CreatedAt: time.Now()})
    state.CreateTask(&Task{ID: "task-2", Repo: "repo2", Ref: "main", Command: "cmd", Status: "stopped", CreatedAt: time.Now()})

    // List all
    tasks, err := state.ListTasks("")
    if err != nil {
        t.Fatalf("failed to list tasks: %v", err)
    }
    if len(tasks) != 2 {
        t.Errorf("expected 2 tasks, got %d", len(tasks))
    }

    // List running only
    running, err := state.ListTasks("running")
    if err != nil {
        t.Fatalf("failed to list running tasks: %v", err)
    }
    if len(running) != 1 {
        t.Errorf("expected 1 running task, got %d", len(running))
    }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/daemon/... -v
```

Expected: FAIL

**Step 3: Add SQLite dependency**

```bash
go get github.com/mattn/go-sqlite3
```

**Step 4: Implement state management**

```go
// pkg/daemon/state.go
package daemon

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    "time"

    _ "github.com/mattn/go-sqlite3"
)

type State struct {
    db *sql.DB
}

type Task struct {
    ID        string
    Name      string
    Repo      string
    Ref       string
    Command   string
    Status    string
    VMID      string
    CreatedAt time.Time
    StoppedAt *time.Time
}

func NewState() (*State, error) {
    dataDir, err := DataDir()
    if err != nil {
        return nil, err
    }

    if err := os.MkdirAll(dataDir, 0755); err != nil {
        return nil, err
    }

    dbPath := filepath.Join(dataDir, "state.db")
    return newStateFromPath(dbPath)
}

func NewStateInMemory() (*State, error) {
    return newStateFromPath(":memory:")
}

func newStateFromPath(dbPath string) (*State, error) {
    db, err := sql.Open("sqlite3", dbPath)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }

    s := &State{db: db}
    if err := s.migrate(); err != nil {
        db.Close()
        return nil, fmt.Errorf("failed to migrate database: %w", err)
    }

    return s, nil
}

func (s *State) migrate() error {
    _, err := s.db.Exec(`
        CREATE TABLE IF NOT EXISTS tasks (
            id TEXT PRIMARY KEY,
            name TEXT,
            repo TEXT NOT NULL,
            ref TEXT NOT NULL,
            command TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'running',
            vm_id TEXT,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            stopped_at DATETIME
        );

        CREATE TABLE IF NOT EXISTS snapshots (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            task_id TEXT NOT NULL,
            name TEXT NOT NULL,
            label TEXT,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (task_id) REFERENCES tasks(id)
        );

        CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
        CREATE INDEX IF NOT EXISTS idx_snapshots_task ON snapshots(task_id);
    `)
    return err
}

func (s *State) CreateTask(task *Task) error {
    _, err := s.db.Exec(
        `INSERT INTO tasks (id, name, repo, ref, command, status, vm_id, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        task.ID, task.Name, task.Repo, task.Ref, task.Command, task.Status, task.VMID, task.CreatedAt,
    )
    return err
}

func (s *State) GetTask(id string) (*Task, error) {
    row := s.db.QueryRow(
        `SELECT id, name, repo, ref, command, status, vm_id, created_at, stopped_at
         FROM tasks WHERE id = ?`, id,
    )

    var task Task
    var name, vmID sql.NullString
    var stoppedAt sql.NullTime
    err := row.Scan(&task.ID, &name, &task.Repo, &task.Ref, &task.Command,
        &task.Status, &vmID, &task.CreatedAt, &stoppedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    task.Name = name.String
    task.VMID = vmID.String
    if stoppedAt.Valid {
        task.StoppedAt = &stoppedAt.Time
    }
    return &task, nil
}

func (s *State) ListTasks(status string) ([]*Task, error) {
    query := `SELECT id, name, repo, ref, command, status, vm_id, created_at, stopped_at FROM tasks`
    var args []interface{}

    if status != "" {
        query += ` WHERE status = ?`
        args = append(args, status)
    }
    query += ` ORDER BY created_at DESC`

    rows, err := s.db.Query(query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var tasks []*Task
    for rows.Next() {
        var task Task
        var name, vmID sql.NullString
        var stoppedAt sql.NullTime
        if err := rows.Scan(&task.ID, &name, &task.Repo, &task.Ref, &task.Command,
            &task.Status, &vmID, &task.CreatedAt, &stoppedAt); err != nil {
            return nil, err
        }
        task.Name = name.String
        task.VMID = vmID.String
        if stoppedAt.Valid {
            task.StoppedAt = &stoppedAt.Time
        }
        tasks = append(tasks, &task)
    }
    return tasks, nil
}

func (s *State) UpdateTaskStatus(id, status string) error {
    var stoppedAt interface{}
    if status == "stopped" || status == "failed" {
        now := time.Now()
        stoppedAt = now
    }

    _, err := s.db.Exec(
        `UPDATE tasks SET status = ?, stopped_at = ? WHERE id = ?`,
        status, stoppedAt, id,
    )
    return err
}

func (s *State) DeleteTask(id string) error {
    _, err := s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
    return err
}

func (s *State) RecordSnapshot(taskID, name, label string) error {
    _, err := s.db.Exec(
        `INSERT INTO snapshots (task_id, name, label) VALUES (?, ?, ?)`,
        taskID, name, label,
    )
    return err
}

func (s *State) Close() error {
    return s.db.Close()
}

func DataDir() (string, error) {
    if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
        return filepath.Join(xdg, "stockyard"), nil
    }

    home, err := os.UserHomeDir()
    if err != nil {
        return "", err
    }

    return filepath.Join(home, ".local", "share", "stockyard"), nil
}
```

**Step 5: Run tests**

```bash
go test ./pkg/daemon/... -v
```

Expected: PASS

**Step 6: Commit**

```bash
go mod tidy
git add pkg/daemon/ go.mod go.sum
git commit -m "feat: add daemon state management with SQLite"
```

---

### Task 4.2: Create Daemon Core

**Files:**
- Create: `pkg/daemon/daemon.go`

**Step 1: Implement daemon**

```go
// pkg/daemon/daemon.go
package daemon

import (
    "context"
    "fmt"
    "net"
    "os"
    "path/filepath"
    "sync"

    "github.com/obra/stockyard/pkg/config"
    "github.com/obra/stockyard/pkg/secrets"
    "github.com/obra/stockyard/pkg/zfs"
)

type Daemon struct {
    cfg      *config.Config
    secrets  secrets.Provider
    zfs      *zfs.Manager
    state    *State

    listener net.Listener
    mu       sync.Mutex
    running  bool
}

func New(cfg *config.Config, secretsProvider secrets.Provider) (*Daemon, error) {
    zfsMgr := zfs.NewManager(cfg.ZFS.Pool, cfg.ZFS.BasePath)

    state, err := NewState()
    if err != nil {
        return nil, fmt.Errorf("failed to initialize state: %w", err)
    }

    return &Daemon{
        cfg:     cfg,
        secrets: secretsProvider,
        zfs:     zfsMgr,
        state:   state,
    }, nil
}

func (d *Daemon) Start(ctx context.Context) error {
    d.mu.Lock()
    if d.running {
        d.mu.Unlock()
        return fmt.Errorf("daemon already running")
    }
    d.running = true
    d.mu.Unlock()

    socketDir := filepath.Dir(d.cfg.Daemon.SocketPath)
    if err := os.MkdirAll(socketDir, 0755); err != nil {
        return fmt.Errorf("failed to create socket directory: %w", err)
    }

    os.Remove(d.cfg.Daemon.SocketPath)

    listener, err := net.Listen("unix", d.cfg.Daemon.SocketPath)
    if err != nil {
        return fmt.Errorf("failed to listen on socket: %w", err)
    }
    d.listener = listener

    fmt.Printf("Daemon listening on %s\n", d.cfg.Daemon.SocketPath)

    // TODO: Start gRPC server here

    <-ctx.Done()
    return d.Stop()
}

func (d *Daemon) Stop() error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if !d.running {
        return nil
    }

    d.running = false

    if d.listener != nil {
        d.listener.Close()
    }

    if d.state != nil {
        d.state.Close()
    }

    return nil
}

func (d *Daemon) State() *State {
    return d.state
}

func (d *Daemon) ZFS() *zfs.Manager {
    return d.zfs
}

func (d *Daemon) Secrets() secrets.Provider {
    return d.secrets
}

func (d *Daemon) Config() *config.Config {
    return d.cfg
}
```

**Step 2: Verify compilation**

```bash
go build ./pkg/daemon/...
```

Expected: Compiles without errors

**Step 3: Commit**

```bash
git add pkg/daemon/daemon.go
git commit -m "feat: add daemon core"
```

---

### Task 4.3: Update Daemon Entry Point

**Files:**
- Modify: `cmd/stockyardd/main.go`

**Step 1: Update daemon main**

```go
// cmd/stockyardd/main.go
package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/obra/stockyard/pkg/config"
    "github.com/obra/stockyard/pkg/daemon"
    "github.com/obra/stockyard/pkg/secrets"
    "github.com/obra/stockyard/pkg/version"
)

func main() {
    if len(os.Args) > 1 && os.Args[1] == "version" {
        fmt.Printf("stockyardd %s\n", version.Version)
        os.Exit(0)
    }

    if err := run(); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}

func run() error {
    cfg, err := config.Load()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    if cfg.InstanceID == "" {
        return fmt.Errorf("stockyard not initialized. Run: stockyard init --instance <name>")
    }

    secretsProvider := secrets.NewOnePasswordProvider(cfg.Secrets.Vault, cfg.Secrets.Prefix)

    d, err := daemon.New(cfg, secretsProvider)
    if err != nil {
        return fmt.Errorf("failed to create daemon: %w", err)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        <-sigCh
        fmt.Println("\nShutting down...")
        cancel()
    }()

    fmt.Printf("Starting stockyardd for instance %q\n", cfg.InstanceID)
    return d.Start(ctx)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyardd ./cmd/stockyardd
./bin/stockyardd version
```

Expected: Prints version

**Step 3: Commit**

```bash
git add cmd/stockyardd/
git commit -m "feat: update daemon entry point"
```

---

## Phase 5: gRPC API

### Task 5.1: Define Protobuf API

**Files:**
- Create: `api/stockyard.proto`
- Create: `Makefile`

**Step 1: Create proto file**

```protobuf
// api/stockyard.proto
syntax = "proto3";

package stockyard.v1;

option go_package = "github.com/obra/stockyard/pkg/api/v1";

service Stockyard {
    rpc CreateTask(CreateTaskRequest) returns (CreateTaskResponse);
    rpc GetTask(GetTaskRequest) returns (GetTaskResponse);
    rpc ListTasks(ListTasksRequest) returns (ListTasksResponse);
    rpc StopTask(StopTaskRequest) returns (StopTaskResponse);
    rpc DestroyTask(DestroyTaskRequest) returns (DestroyTaskResponse);

    rpc CreateSnapshot(CreateSnapshotRequest) returns (CreateSnapshotResponse);
    rpc ListSnapshots(ListSnapshotsRequest) returns (ListSnapshotsResponse);
    rpc RestoreSnapshot(RestoreSnapshotRequest) returns (RestoreSnapshotResponse);

    rpc GetLogs(GetLogsRequest) returns (stream LogEntry);
}

message CreateTaskRequest {
    string repo = 1;
    string ref = 2;
    string name = 3;
    repeated string command = 4;
    map<string, string> env = 5;
    int32 cpus = 6;
    string memory = 7;
    bool no_tailscale = 8;
}

message CreateTaskResponse {
    string task_id = 1;
    string tailscale_hostname = 2;
}

message GetTaskRequest {
    string task_id = 1;
}

message GetTaskResponse {
    Task task = 1;
}

message ListTasksRequest {
    string status = 1;
}

message ListTasksResponse {
    repeated Task tasks = 1;
}

message StopTaskRequest {
    string task_id = 1;
}

message StopTaskResponse {}

message DestroyTaskRequest {
    string task_id = 1;
}

message DestroyTaskResponse {}

message CreateSnapshotRequest {
    string task_id = 1;
    string label = 2;
}

message CreateSnapshotResponse {
    string snapshot_name = 1;
}

message ListSnapshotsRequest {
    string task_id = 1;
}

message ListSnapshotsResponse {
    repeated Snapshot snapshots = 1;
}

message RestoreSnapshotRequest {
    string task_id = 1;
    string snapshot_name = 2;
}

message RestoreSnapshotResponse {}

message GetLogsRequest {
    string task_id = 1;
    bool follow = 2;
    int32 tail = 3;
}

message LogEntry {
    string timestamp = 1;
    string line = 2;
}

message Task {
    string id = 1;
    string name = 2;
    string repo = 3;
    string ref = 4;
    string status = 5;
    string tailscale_hostname = 6;
    string created_at = 7;
    string stopped_at = 8;
}

message Snapshot {
    string name = 1;
    string label = 2;
    string created_at = 3;
}
```

**Step 2: Create Makefile**

```makefile
# Makefile

.PHONY: all build proto clean test

all: proto build

build:
	go build -o bin/stockyard ./cmd/stockyard
	go build -o bin/stockyardd ./cmd/stockyardd

proto:
	mkdir -p pkg/api/v1
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/stockyard.proto
	mv api/*.go pkg/api/v1/

clean:
	rm -rf bin/

test:
	go test ./...
```

**Step 3: Install protoc tools and generate**

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go get google.golang.org/grpc
go get google.golang.org/protobuf

# If protoc not installed:
# sudo apt-get install -y protobuf-compiler

make proto
```

**Step 4: Commit**

```bash
git add api/ Makefile pkg/api/ go.mod go.sum
git commit -m "feat: add gRPC API definition"
```

---

### Task 5.2: Implement gRPC Server

**Files:**
- Create: `pkg/daemon/grpc.go`

**Step 1: Implement gRPC server**

```go
// pkg/daemon/grpc.go
package daemon

import (
    "context"
    "fmt"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"

    pb "github.com/obra/stockyard/pkg/api/v1"
)

type grpcServer struct {
    pb.UnimplementedStockyardServer
    daemon *Daemon
}

func newGRPCServer(d *Daemon) *grpcServer {
    return &grpcServer{daemon: d}
}

func (s *grpcServer) Register(srv *grpc.Server) {
    pb.RegisterStockyardServer(srv, s)
}

func (s *grpcServer) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.CreateTaskResponse, error) {
    // TODO: Implement with Flintlock
    return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *grpcServer) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.GetTaskResponse, error) {
    task, err := s.daemon.state.GetTask(req.TaskId)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to get task: %v", err)
    }
    if task == nil {
        return nil, status.Error(codes.NotFound, "task not found")
    }

    return &pb.GetTaskResponse{
        Task: taskToProto(task),
    }, nil
}

func (s *grpcServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
    tasks, err := s.daemon.state.ListTasks(req.Status)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to list tasks: %v", err)
    }

    pbTasks := make([]*pb.Task, len(tasks))
    for i, t := range tasks {
        pbTasks[i] = taskToProto(t)
    }

    return &pb.ListTasksResponse{Tasks: pbTasks}, nil
}

func (s *grpcServer) StopTask(ctx context.Context, req *pb.StopTaskRequest) (*pb.StopTaskResponse, error) {
    // TODO: Stop VM via Flintlock
    err := s.daemon.state.UpdateTaskStatus(req.TaskId, "stopped")
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to stop task: %v", err)
    }
    return &pb.StopTaskResponse{}, nil
}

func (s *grpcServer) DestroyTask(ctx context.Context, req *pb.DestroyTaskRequest) (*pb.DestroyTaskResponse, error) {
    // TODO: Destroy VM via Flintlock
    // Destroy ZFS dataset
    if err := s.daemon.zfs.DestroyDataset(ctx, req.TaskId); err != nil {
        // Log but don't fail - dataset may not exist
        fmt.Printf("Warning: failed to destroy ZFS dataset: %v\n", err)
    }

    if err := s.daemon.state.DeleteTask(req.TaskId); err != nil {
        return nil, status.Errorf(codes.Internal, "failed to delete task: %v", err)
    }
    return &pb.DestroyTaskResponse{}, nil
}

func (s *grpcServer) CreateSnapshot(ctx context.Context, req *pb.CreateSnapshotRequest) (*pb.CreateSnapshotResponse, error) {
    // Sync filesystem first
    if err := s.daemon.zfs.Sync(ctx, req.TaskId); err != nil {
        fmt.Printf("Warning: sync failed: %v\n", err)
    }

    snapName, err := s.daemon.zfs.CreateSnapshot(ctx, req.TaskId, req.Label)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to create snapshot: %v", err)
    }

    // Record in database
    s.daemon.state.RecordSnapshot(req.TaskId, snapName, req.Label)

    return &pb.CreateSnapshotResponse{SnapshotName: snapName}, nil
}

func (s *grpcServer) ListSnapshots(ctx context.Context, req *pb.ListSnapshotsRequest) (*pb.ListSnapshotsResponse, error) {
    snapshots, err := s.daemon.zfs.ListSnapshots(ctx, req.TaskId)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "failed to list snapshots: %v", err)
    }

    pbSnaps := make([]*pb.Snapshot, len(snapshots))
    for i, name := range snapshots {
        pbSnaps[i] = &pb.Snapshot{Name: name}
    }

    return &pb.ListSnapshotsResponse{Snapshots: pbSnaps}, nil
}

func (s *grpcServer) RestoreSnapshot(ctx context.Context, req *pb.RestoreSnapshotRequest) (*pb.RestoreSnapshotResponse, error) {
    if err := s.daemon.zfs.RollbackSnapshot(ctx, req.TaskId, req.SnapshotName); err != nil {
        return nil, status.Errorf(codes.Internal, "failed to restore snapshot: %v", err)
    }
    return &pb.RestoreSnapshotResponse{}, nil
}

func (s *grpcServer) GetLogs(req *pb.GetLogsRequest, stream pb.Stockyard_GetLogsServer) error {
    // TODO: Implement log streaming
    return status.Error(codes.Unimplemented, "not implemented")
}

func taskToProto(t *Task) *pb.Task {
    pt := &pb.Task{
        Id:        t.ID,
        Name:      t.Name,
        Repo:      t.Repo,
        Ref:       t.Ref,
        Status:    t.Status,
        CreatedAt: t.CreatedAt.Format(time.RFC3339),
    }
    if t.StoppedAt != nil {
        pt.StoppedAt = t.StoppedAt.Format(time.RFC3339)
    }
    return pt
}
```

**Step 2: Update daemon to start gRPC server**

Add to `pkg/daemon/daemon.go` in the `Start` method, replace the `// TODO: Start gRPC server here` with:

```go
// In daemon.go Start method, after creating listener:

grpcSrv := grpc.NewServer()
grpcHandler := newGRPCServer(d)
grpcHandler.Register(grpcSrv)

go func() {
    if err := grpcSrv.Serve(listener); err != nil {
        fmt.Printf("gRPC server error: %v\n", err)
    }
}()

fmt.Printf("gRPC server started on %s\n", d.cfg.Daemon.SocketPath)
```

Also add the import: `"google.golang.org/grpc"`

**Step 3: Verify compilation**

```bash
go build ./pkg/daemon/...
```

Expected: Compiles without errors

**Step 4: Commit**

```bash
git add pkg/daemon/
git commit -m "feat: implement gRPC server"
```

---

## Phase 6: CLI Client Commands

### Task 6.1: Create Client Package

**Files:**
- Create: `pkg/client/client.go`

**Step 1: Implement client**

```go
// pkg/client/client.go
package client

import (
    "context"
    "fmt"
    "io"
    "net"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pb "github.com/obra/stockyard/pkg/api/v1"
)

type Client struct {
    conn   *grpc.ClientConn
    client pb.StockyardClient
}

func New(socketPath string) (*Client, error) {
    conn, err := grpc.Dial(
        socketPath,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
            return net.Dial("unix", addr)
        }),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to daemon: %w", err)
    }

    return &Client{
        conn:   conn,
        client: pb.NewStockyardClient(conn),
    }, nil
}

func (c *Client) Close() error {
    return c.conn.Close()
}

func (c *Client) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.CreateTaskResponse, error) {
    return c.client.CreateTask(ctx, req)
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*pb.Task, error) {
    resp, err := c.client.GetTask(ctx, &pb.GetTaskRequest{TaskId: taskID})
    if err != nil {
        return nil, err
    }
    return resp.Task, nil
}

func (c *Client) ListTasks(ctx context.Context, status string) ([]*pb.Task, error) {
    resp, err := c.client.ListTasks(ctx, &pb.ListTasksRequest{Status: status})
    if err != nil {
        return nil, err
    }
    return resp.Tasks, nil
}

func (c *Client) StopTask(ctx context.Context, taskID string) error {
    _, err := c.client.StopTask(ctx, &pb.StopTaskRequest{TaskId: taskID})
    return err
}

func (c *Client) DestroyTask(ctx context.Context, taskID string) error {
    _, err := c.client.DestroyTask(ctx, &pb.DestroyTaskRequest{TaskId: taskID})
    return err
}

func (c *Client) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
    resp, err := c.client.CreateSnapshot(ctx, &pb.CreateSnapshotRequest{
        TaskId: taskID,
        Label:  label,
    })
    if err != nil {
        return "", err
    }
    return resp.SnapshotName, nil
}

func (c *Client) ListSnapshots(ctx context.Context, taskID string) ([]*pb.Snapshot, error) {
    resp, err := c.client.ListSnapshots(ctx, &pb.ListSnapshotsRequest{TaskId: taskID})
    if err != nil {
        return nil, err
    }
    return resp.Snapshots, nil
}

func (c *Client) RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error {
    _, err := c.client.RestoreSnapshot(ctx, &pb.RestoreSnapshotRequest{
        TaskId:       taskID,
        SnapshotName: snapshotName,
    })
    return err
}

func (c *Client) GetLogs(ctx context.Context, taskID string, follow bool, tail int32) (pb.Stockyard_GetLogsClient, error) {
    return c.client.GetLogs(ctx, &pb.GetLogsRequest{
        TaskId: taskID,
        Follow: follow,
        Tail:   tail,
    })
}

// StreamLogs is a helper that prints logs to the given writer
func (c *Client) StreamLogs(ctx context.Context, taskID string, follow bool, tail int32, out io.Writer) error {
    stream, err := c.GetLogs(ctx, taskID, follow, tail)
    if err != nil {
        return err
    }

    for {
        entry, err := stream.Recv()
        if err == io.EOF {
            return nil
        }
        if err != nil {
            return err
        }
        fmt.Fprintf(out, "%s %s\n", entry.Timestamp, entry.Line)
    }
}
```

**Step 2: Commit**

```bash
git add pkg/client/
git commit -m "feat: add gRPC client package"
```

---

### Task 6.2: Add List Command

**Files:**
- Create: `cmd/stockyard/list.go`

**Step 1: Create list command**

```go
// cmd/stockyard/list.go
package main

import (
    "context"
    "fmt"
    "os"
    "text/tabwriter"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var listStatus string

var listCmd = &cobra.Command{
    Use:     "list",
    Short:   "List tasks",
    Aliases: []string{"ls"},
    RunE: func(cmd *cobra.Command, args []string) error {
        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        tasks, err := c.ListTasks(context.Background(), listStatus)
        if err != nil {
            return err
        }

        if len(tasks) == 0 {
            fmt.Println("No tasks found")
            return nil
        }

        w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
        fmt.Fprintln(w, "ID\tNAME\tREPO\tSTATUS\tCREATED")
        for _, t := range tasks {
            name := t.Name
            if name == "" {
                name = "-"
            }
            fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
                t.Id, name, t.Repo, t.Status, t.CreatedAt)
        }
        w.Flush()

        return nil
    },
}

func init() {
    listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (running, stopped, failed)")
    rootCmd.AddCommand(listCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard list --help
```

Expected: Shows help for list command

**Step 3: Commit**

```bash
git add cmd/stockyard/list.go
git commit -m "feat: add list command"
```

---

### Task 6.3: Add Snapshot Commands

**Files:**
- Create: `cmd/stockyard/snapshot.go`
- Create: `cmd/stockyard/snapshots.go`
- Create: `cmd/stockyard/restore.go`

**Step 1: Create snapshot command**

```go
// cmd/stockyard/snapshot.go
package main

import (
    "context"
    "fmt"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
    Use:   "snapshot <task-id> [label]",
    Short: "Create a snapshot of a task's workspace",
    Args:  cobra.RangeArgs(1, 2),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]
        label := ""
        if len(args) > 1 {
            label = args[1]
        }

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w", err)
        }
        defer c.Close()

        snapName, err := c.CreateSnapshot(context.Background(), taskID, label)
        if err != nil {
            return fmt.Errorf("failed to create snapshot: %w", err)
        }

        fmt.Printf("Created snapshot: %s\n", snapName)
        return nil
    },
}

func init() {
    rootCmd.AddCommand(snapshotCmd)
}
```

**Step 2: Create snapshots list command**

```go
// cmd/stockyard/snapshots.go
package main

import (
    "context"
    "fmt"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var snapshotsCmd = &cobra.Command{
    Use:   "snapshots <task-id>",
    Short: "List snapshots for a task",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w", err)
        }
        defer c.Close()

        snapshots, err := c.ListSnapshots(context.Background(), taskID)
        if err != nil {
            return fmt.Errorf("failed to list snapshots: %w", err)
        }

        if len(snapshots) == 0 {
            fmt.Println("No snapshots found")
            return nil
        }

        for _, s := range snapshots {
            fmt.Println(s.Name)
        }
        return nil
    },
}

func init() {
    rootCmd.AddCommand(snapshotsCmd)
}
```

**Step 3: Create restore command**

```go
// cmd/stockyard/restore.go
package main

import (
    "context"
    "fmt"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
    Use:   "restore <task-id> <snapshot-name>",
    Short: "Restore a task's workspace to a snapshot",
    Args:  cobra.ExactArgs(2),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]
        snapName := args[1]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w", err)
        }
        defer c.Close()

        if err := c.RestoreSnapshot(context.Background(), taskID, snapName); err != nil {
            return fmt.Errorf("failed to restore snapshot: %w", err)
        }

        fmt.Printf("Restored to snapshot: %s\n", snapName)
        return nil
    },
}

func init() {
    rootCmd.AddCommand(restoreCmd)
}
```

**Step 4: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard snapshot --help
./bin/stockyard snapshots --help
./bin/stockyard restore --help
```

**Step 5: Commit**

```bash
git add cmd/stockyard/snapshot.go cmd/stockyard/snapshots.go cmd/stockyard/restore.go
git commit -m "feat: add snapshot commands"
```

---

**End of Part 2. Continue with Part 3: Flintlock Integration.**

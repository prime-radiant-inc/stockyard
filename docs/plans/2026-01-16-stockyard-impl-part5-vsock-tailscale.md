# Stockyard Implementation - Part 5: vsock & Tailscale (Phases 9-10)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement vsock-based snapshot triggers from VMs and Tailscale auto-join for SSH access.

**Reference:**
- Design doc at `docs/plans/2026-01-16-stockyard-design.md`

---

## Phase 9: vsock Snapshot Service

### Task 9.1: Add vsock Dependencies

**Files:**
- Modify: `go.mod`

**Step 1: Add vsock package**

```bash
go get github.com/mdlayher/vsock
```

**Step 2: Verify**

```bash
go mod tidy
go build ./...
```

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add vsock package for VM communication"
```

---

### Task 9.2: Create vsock Package

**Files:**
- Create: `pkg/vsock/listener.go`
- Create: `pkg/vsock/listener_test.go`
- Create: `pkg/vsock/protocol.go`

**Step 1: Write test**

```go
// pkg/vsock/protocol_test.go
package vsock

import (
    "bytes"
    "testing"
)

func TestProtocol_EncodeDecodeRequest(t *testing.T) {
    label := "edit-main.py"

    // Encode
    var buf bytes.Buffer
    if err := EncodeSnapshotRequest(&buf, label); err != nil {
        t.Fatalf("encode failed: %v", err)
    }

    // Decode
    decoded, err := DecodeSnapshotRequest(&buf)
    if err != nil {
        t.Fatalf("decode failed: %v", err)
    }

    if decoded != label {
        t.Errorf("got %q, want %q", decoded, label)
    }
}

func TestProtocol_EncodeDecodeResponse(t *testing.T) {
    tests := []struct {
        name    string
        success bool
        message string
    }{
        {"success", true, "snapshot-name"},
        {"failure", false, "disk full"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            var buf bytes.Buffer
            if err := EncodeSnapshotResponse(&buf, tt.success, tt.message); err != nil {
                t.Fatalf("encode failed: %v", err)
            }

            success, message, err := DecodeSnapshotResponse(&buf)
            if err != nil {
                t.Fatalf("decode failed: %v", err)
            }

            if success != tt.success {
                t.Errorf("success: got %v, want %v", success, tt.success)
            }
            if message != tt.message {
                t.Errorf("message: got %q, want %q", message, tt.message)
            }
        })
    }
}
```

**Step 2: Run test**

```bash
go test ./pkg/vsock/... -v
```

Expected: FAIL

**Step 3: Implement protocol**

```go
// pkg/vsock/protocol.go
package vsock

import (
    "encoding/binary"
    "fmt"
    "io"
)

// Protocol for snapshot requests over vsock:
// Request:  [label_len:uint32][label:bytes]
// Response: [status:uint8][msg_len:uint32][msg:bytes]
//
// status: 0 = success, 1 = failure

const (
    StatusSuccess = 0
    StatusFailure = 1
)

// EncodeSnapshotRequest encodes a snapshot request
func EncodeSnapshotRequest(w io.Writer, label string) error {
    labelBytes := []byte(label)
    if err := binary.Write(w, binary.LittleEndian, uint32(len(labelBytes))); err != nil {
        return fmt.Errorf("write label length: %w", err)
    }
    if _, err := w.Write(labelBytes); err != nil {
        return fmt.Errorf("write label: %w", err)
    }
    return nil
}

// DecodeSnapshotRequest decodes a snapshot request
func DecodeSnapshotRequest(r io.Reader) (string, error) {
    var labelLen uint32
    if err := binary.Read(r, binary.LittleEndian, &labelLen); err != nil {
        return "", fmt.Errorf("read label length: %w", err)
    }

    if labelLen > 1024 {
        return "", fmt.Errorf("label too long: %d", labelLen)
    }

    labelBytes := make([]byte, labelLen)
    if _, err := io.ReadFull(r, labelBytes); err != nil {
        return "", fmt.Errorf("read label: %w", err)
    }

    return string(labelBytes), nil
}

// EncodeSnapshotResponse encodes a snapshot response
func EncodeSnapshotResponse(w io.Writer, success bool, message string) error {
    status := uint8(StatusSuccess)
    if !success {
        status = StatusFailure
    }

    if err := binary.Write(w, binary.LittleEndian, status); err != nil {
        return fmt.Errorf("write status: %w", err)
    }

    msgBytes := []byte(message)
    if err := binary.Write(w, binary.LittleEndian, uint32(len(msgBytes))); err != nil {
        return fmt.Errorf("write message length: %w", err)
    }

    if len(msgBytes) > 0 {
        if _, err := w.Write(msgBytes); err != nil {
            return fmt.Errorf("write message: %w", err)
        }
    }

    return nil
}

// DecodeSnapshotResponse decodes a snapshot response
func DecodeSnapshotResponse(r io.Reader) (success bool, message string, err error) {
    var status uint8
    if err := binary.Read(r, binary.LittleEndian, &status); err != nil {
        return false, "", fmt.Errorf("read status: %w", err)
    }

    var msgLen uint32
    if err := binary.Read(r, binary.LittleEndian, &msgLen); err != nil {
        return false, "", fmt.Errorf("read message length: %w", err)
    }

    if msgLen > 0 {
        if msgLen > 4096 {
            return false, "", fmt.Errorf("message too long: %d", msgLen)
        }

        msgBytes := make([]byte, msgLen)
        if _, err := io.ReadFull(r, msgBytes); err != nil {
            return false, "", fmt.Errorf("read message: %w", err)
        }
        message = string(msgBytes)
    }

    return status == StatusSuccess, message, nil
}
```

**Step 4: Run tests**

```bash
go test ./pkg/vsock/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/vsock/protocol.go pkg/vsock/protocol_test.go
git commit -m "feat: add vsock protocol for snapshot requests

- Binary protocol with label length prefix
- Success/failure response with message
- Size limits for safety"
```

---

### Task 9.3: Implement vsock Listener

**Files:**
- Create: `pkg/vsock/listener.go`
- Create: `pkg/vsock/listener_test.go`

**Step 1: Write test**

```go
// pkg/vsock/listener_test.go
package vsock

import (
    "context"
    "net"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestSnapshotServer_UnixFallback(t *testing.T) {
    tmpDir := t.TempDir()
    sockPath := filepath.Join(tmpDir, "test.sock")

    // Track snapshot requests
    var receivedLabel string
    handler := func(vmID, label string) error {
        receivedLabel = label
        return nil
    }

    // Create server with Unix socket fallback
    server := NewSnapshotServer(handler)
    server.UnixSocketPath = sockPath

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Start server
    go func() {
        if err := server.ListenUnix(ctx); err != nil && ctx.Err() == nil {
            t.Errorf("server error: %v", err)
        }
    }()

    // Wait for socket
    for i := 0; i < 10; i++ {
        if _, err := os.Stat(sockPath); err == nil {
            break
        }
        time.Sleep(50 * time.Millisecond)
    }

    // Connect and send request
    conn, err := net.Dial("unix", sockPath)
    if err != nil {
        t.Fatalf("failed to connect: %v", err)
    }
    defer conn.Close()

    // Send request
    if err := EncodeSnapshotRequest(conn, "test-label"); err != nil {
        t.Fatalf("failed to send request: %v", err)
    }

    // Read response
    conn.SetReadDeadline(time.Now().Add(5 * time.Second))
    success, msg, err := DecodeSnapshotResponse(conn)
    if err != nil {
        t.Fatalf("failed to read response: %v", err)
    }

    if !success {
        t.Errorf("expected success, got failure: %s", msg)
    }

    if receivedLabel != "test-label" {
        t.Errorf("label: got %q, want %q", receivedLabel, "test-label")
    }
}
```

**Step 2: Run test**

```bash
go test ./pkg/vsock/... -v -run TestSnapshotServer
```

Expected: FAIL

**Step 3: Implement listener**

```go
// pkg/vsock/listener.go
package vsock

import (
    "context"
    "fmt"
    "io"
    "log"
    "net"
    "os"
    "sync"

    "github.com/mdlayher/vsock"
)

const (
    // DefaultPort is the vsock port for snapshot service
    DefaultPort = 52000
)

// SnapshotHandler is called when a snapshot is requested
// vmID identifies which VM made the request
// label is the snapshot label from the VM
type SnapshotHandler func(vmID, label string) error

// SnapshotServer listens for snapshot requests from VMs
type SnapshotServer struct {
    handler        SnapshotHandler
    port           uint32
    UnixSocketPath string // Fallback for testing

    mu        sync.Mutex
    listeners []io.Closer
}

// NewSnapshotServer creates a new snapshot server
func NewSnapshotServer(handler SnapshotHandler) *SnapshotServer {
    return &SnapshotServer{
        handler: handler,
        port:    DefaultPort,
    }
}

// ListenVsock starts listening on vsock
func (s *SnapshotServer) ListenVsock(ctx context.Context) error {
    l, err := vsock.Listen(s.port, nil)
    if err != nil {
        return fmt.Errorf("vsock listen: %w", err)
    }

    s.mu.Lock()
    s.listeners = append(s.listeners, l)
    s.mu.Unlock()

    log.Printf("Snapshot server listening on vsock port %d", s.port)

    go func() {
        <-ctx.Done()
        l.Close()
    }()

    for {
        conn, err := l.Accept()
        if err != nil {
            if ctx.Err() != nil {
                return nil
            }
            log.Printf("vsock accept error: %v", err)
            continue
        }

        // Get VM ID from vsock connection
        vsockConn, ok := conn.(*vsock.Conn)
        vmID := "unknown"
        if ok {
            vmID = fmt.Sprintf("cid-%d", vsockConn.RemoteAddr().(*vsock.Addr).ContextID)
        }

        go s.handleConnection(conn, vmID)
    }
}

// ListenUnix starts listening on Unix socket (for testing)
func (s *SnapshotServer) ListenUnix(ctx context.Context) error {
    if s.UnixSocketPath == "" {
        s.UnixSocketPath = "/run/stockyard/snapshot.sock"
    }

    // Ensure directory exists
    if err := os.MkdirAll("/run/stockyard", 0755); err != nil {
        // Ignore error, might be testing
    }

    // Remove stale socket
    os.Remove(s.UnixSocketPath)

    l, err := net.Listen("unix", s.UnixSocketPath)
    if err != nil {
        return fmt.Errorf("unix listen: %w", err)
    }

    s.mu.Lock()
    s.listeners = append(s.listeners, l)
    s.mu.Unlock()

    log.Printf("Snapshot server listening on %s", s.UnixSocketPath)

    go func() {
        <-ctx.Done()
        l.Close()
    }()

    for {
        conn, err := l.Accept()
        if err != nil {
            if ctx.Err() != nil {
                return nil
            }
            log.Printf("unix accept error: %v", err)
            continue
        }

        go s.handleConnection(conn, "unix-client")
    }
}

// Listen starts both vsock and unix listeners
func (s *SnapshotServer) Listen(ctx context.Context) error {
    errCh := make(chan error, 2)

    // Try vsock
    go func() {
        if err := s.ListenVsock(ctx); err != nil {
            log.Printf("vsock listener failed: %v (falling back to unix)", err)
        }
        errCh <- nil
    }()

    // Always start Unix as fallback
    go func() {
        errCh <- s.ListenUnix(ctx)
    }()

    // Wait for context or error
    select {
    case <-ctx.Done():
        return nil
    case err := <-errCh:
        return err
    }
}

func (s *SnapshotServer) handleConnection(conn net.Conn, vmID string) {
    defer conn.Close()

    // Read request
    label, err := DecodeSnapshotRequest(conn)
    if err != nil {
        log.Printf("failed to decode request from %s: %v", vmID, err)
        EncodeSnapshotResponse(conn, false, err.Error())
        return
    }

    log.Printf("Snapshot request from %s: %q", vmID, label)

    // Call handler
    if err := s.handler(vmID, label); err != nil {
        log.Printf("snapshot handler error for %s: %v", vmID, err)
        EncodeSnapshotResponse(conn, false, err.Error())
        return
    }

    // Success
    EncodeSnapshotResponse(conn, true, "")
}

// Close closes all listeners
func (s *SnapshotServer) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

    for _, l := range s.listeners {
        l.Close()
    }
    s.listeners = nil
    return nil
}
```

**Step 4: Run tests**

```bash
go test ./pkg/vsock/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/vsock/listener.go pkg/vsock/listener_test.go
git commit -m "feat: add vsock snapshot server

- vsock listener for VM communication
- Unix socket fallback for testing
- Connection handling with VM ID extraction"
```

---

### Task 9.4: Integrate vsock Server into Daemon

**Files:**
- Modify: `pkg/daemon/daemon.go`
- Create: `pkg/daemon/snapshots.go`

**Step 1: Create snapshot handler**

```go
// pkg/daemon/snapshots.go
package daemon

import (
    "context"
    "fmt"
    "log"
    "strings"

    "github.com/obra/stockyard/pkg/vsock"
)

// SnapshotService handles snapshot requests from VMs
type SnapshotService struct {
    daemon *Daemon
    server *vsock.SnapshotServer
}

// NewSnapshotService creates a new snapshot service
func NewSnapshotService(d *Daemon) *SnapshotService {
    ss := &SnapshotService{daemon: d}
    ss.server = vsock.NewSnapshotServer(ss.handleSnapshot)
    return ss
}

// Start starts the snapshot service
func (ss *SnapshotService) Start(ctx context.Context) error {
    return ss.server.Listen(ctx)
}

// handleSnapshot handles a snapshot request from a VM
func (ss *SnapshotService) handleSnapshot(vmID, label string) error {
    // Map VM ID (CID) to task ID
    taskID, err := ss.resolveTaskID(vmID)
    if err != nil {
        return fmt.Errorf("unknown VM: %w", err)
    }

    log.Printf("Creating snapshot for task %s: %s", taskID, label)

    // Create ZFS snapshot
    snapName, err := ss.daemon.zfs.CreateSnapshot(context.Background(), taskID, label)
    if err != nil {
        return fmt.Errorf("failed to create snapshot: %w", err)
    }

    log.Printf("Created snapshot: %s", snapName)

    // Record in database
    if err := ss.daemon.state.RecordSnapshot(taskID, snapName, label); err != nil {
        log.Printf("Warning: failed to record snapshot in database: %v", err)
        // Don't fail - snapshot was created
    }

    return nil
}

// resolveTaskID maps a VM identifier to a task ID
func (ss *SnapshotService) resolveTaskID(vmID string) (string, error) {
    // For vsock, vmID is like "cid-123"
    // For unix fallback, vmID is "unix-client" (for testing)

    if vmID == "unix-client" {
        // In testing, just use the first running task
        tasks, err := ss.daemon.state.ListTasks("running")
        if err != nil || len(tasks) == 0 {
            return "", fmt.Errorf("no running tasks")
        }
        return tasks[0].ID, nil
    }

    // Extract CID and look up in task table
    // The VM CID is stored when we create the VM
    if strings.HasPrefix(vmID, "cid-") {
        // TODO: Implement CID to task ID mapping
        // For now, scan running tasks
        tasks, err := ss.daemon.state.ListTasks("running")
        if err != nil {
            return "", err
        }
        if len(tasks) == 0 {
            return "", fmt.Errorf("no running tasks")
        }
        // TODO: Match by CID
        return tasks[0].ID, nil
    }

    return "", fmt.Errorf("unknown VM ID format: %s", vmID)
}
```

**Step 2: Add RecordSnapshot to state**

Add to `pkg/daemon/state.go`:

```go
// RecordSnapshot records a snapshot in the database
func (s *State) RecordSnapshot(taskID, name, label string) error {
    _, err := s.db.Exec(
        `INSERT INTO snapshots (task_id, name, label) VALUES (?, ?, ?)`,
        taskID, name, label,
    )
    return err
}

// ListTaskSnapshots lists all snapshots for a task
func (s *State) ListTaskSnapshots(taskID string) ([]SnapshotRecord, error) {
    rows, err := s.db.Query(
        `SELECT name, label, created_at FROM snapshots WHERE task_id = ? ORDER BY created_at DESC`,
        taskID,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var snapshots []SnapshotRecord
    for rows.Next() {
        var snap SnapshotRecord
        if err := rows.Scan(&snap.Name, &snap.Label, &snap.CreatedAt); err != nil {
            return nil, err
        }
        snapshots = append(snapshots, snap)
    }
    return snapshots, nil
}

// SnapshotRecord represents a snapshot in the database
type SnapshotRecord struct {
    Name      string
    Label     string
    CreatedAt time.Time
}

// DeleteTask also deletes associated snapshots
func (s *State) DeleteTask(id string) error {
    tx, err := s.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    if _, err := tx.Exec(`DELETE FROM snapshots WHERE task_id = ?`, id); err != nil {
        return err
    }
    if _, err := tx.Exec(`DELETE FROM tasks WHERE id = ?`, id); err != nil {
        return err
    }

    return tx.Commit()
}
```

**Step 3: Update daemon to start snapshot service**

Add to `pkg/daemon/daemon.go`:

```go
// Add field to Daemon struct:
snapshots *SnapshotService

// In New(), after creating state:
// Note: SnapshotService is created in Start()

// Update Start() to include:
func (d *Daemon) Start(ctx context.Context) error {
    // ... existing code ...

    // Start snapshot service
    d.snapshots = NewSnapshotService(d)
    go func() {
        if err := d.snapshots.Start(ctx); err != nil {
            log.Printf("Snapshot service error: %v", err)
        }
    }()

    // ... rest of Start() ...
}
```

**Step 4: Verify compilation**

```bash
go build ./pkg/daemon/...
```

**Step 5: Commit**

```bash
git add pkg/daemon/snapshots.go pkg/daemon/state.go pkg/daemon/daemon.go
git commit -m "feat: integrate vsock snapshot service into daemon

- SnapshotService handles requests from VMs
- Snapshot recording in database
- VM ID to task ID resolution"
```

---

### Task 9.5: Test End-to-End Snapshot Flow

**Files:**
- Create: `pkg/vsock/integration_test.go`

**Step 1: Create integration test**

```go
// pkg/vsock/integration_test.go
//go:build integration

package vsock

import (
    "context"
    "net"
    "os"
    "path/filepath"
    "testing"
    "time"
)

func TestIntegration_SnapshotRequest(t *testing.T) {
    if os.Getenv("INTEGRATION_TEST") == "" {
        t.Skip("skipping integration test; set INTEGRATION_TEST=1")
    }

    tmpDir := t.TempDir()
    sockPath := filepath.Join(tmpDir, "test.sock")

    snapshots := make(chan string, 10)
    handler := func(vmID, label string) error {
        snapshots <- label
        return nil
    }

    server := NewSnapshotServer(handler)
    server.UnixSocketPath = sockPath

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    go server.ListenUnix(ctx)

    // Wait for server
    time.Sleep(100 * time.Millisecond)

    // Simulate multiple snapshot requests
    labels := []string{"edit-main.py", "bash-npm-test", "write-config.json"}

    for _, label := range labels {
        conn, err := net.Dial("unix", sockPath)
        if err != nil {
            t.Fatalf("dial: %v", err)
        }

        if err := EncodeSnapshotRequest(conn, label); err != nil {
            conn.Close()
            t.Fatalf("encode: %v", err)
        }

        conn.SetReadDeadline(time.Now().Add(5 * time.Second))
        success, _, err := DecodeSnapshotResponse(conn)
        conn.Close()

        if err != nil {
            t.Fatalf("decode: %v", err)
        }
        if !success {
            t.Errorf("expected success for %q", label)
        }
    }

    // Verify all snapshots received
    for _, expected := range labels {
        select {
        case got := <-snapshots:
            if got != expected {
                t.Errorf("snapshot: got %q, want %q", got, expected)
            }
        case <-time.After(time.Second):
            t.Errorf("timeout waiting for snapshot %q", expected)
        }
    }
}
```

**Step 2: Run integration test**

```bash
INTEGRATION_TEST=1 go test ./pkg/vsock/... -v -tags=integration -run TestIntegration
```

**Step 3: Commit**

```bash
git add pkg/vsock/integration_test.go
git commit -m "test: add vsock integration tests

- End-to-end snapshot request testing
- Multiple sequential requests"
```

---

## Phase 10: Tailscale Integration

### Task 10.1: Create Tailscale Package

**Files:**
- Create: `pkg/tailscale/tailscale.go`
- Create: `pkg/tailscale/tailscale_test.go`

**Step 1: Write test**

```go
// pkg/tailscale/tailscale_test.go
package tailscale

import (
    "testing"
)

func TestBuildHostname(t *testing.T) {
    tests := []struct {
        taskID   string
        expected string
    }{
        {"task-abc123", "stockyard-task-abc123"},
        {"vm-xyz", "stockyard-vm-xyz"},
    }

    for _, tt := range tests {
        got := BuildHostname(tt.taskID)
        if got != tt.expected {
            t.Errorf("BuildHostname(%q) = %q, want %q", tt.taskID, got, tt.expected)
        }
    }
}

func TestValidateAuthKey(t *testing.T) {
    tests := []struct {
        key     string
        wantErr bool
    }{
        {"tskey-auth-xxx", false},
        {"tskey-xxx", false},
        {"", true},
        {"invalid", true},
    }

    for _, tt := range tests {
        err := ValidateAuthKey(tt.key)
        if (err != nil) != tt.wantErr {
            t.Errorf("ValidateAuthKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
        }
    }
}
```

**Step 2: Run test**

```bash
go test ./pkg/tailscale/... -v
```

Expected: FAIL

**Step 3: Implement tailscale package**

```go
// pkg/tailscale/tailscale.go
package tailscale

import (
    "bytes"
    "context"
    "fmt"
    "os/exec"
    "strings"
)

// BuildHostname generates a Tailscale hostname for a task
func BuildHostname(taskID string) string {
    return fmt.Sprintf("stockyard-%s", taskID)
}

// ValidateAuthKey validates a Tailscale auth key format
func ValidateAuthKey(key string) error {
    if key == "" {
        return fmt.Errorf("auth key is empty")
    }
    if !strings.HasPrefix(key, "tskey-") {
        return fmt.Errorf("invalid auth key format: should start with 'tskey-'")
    }
    return nil
}

// Manager handles Tailscale operations
type Manager struct {
    authKey string
}

// NewManager creates a new Tailscale manager
func NewManager(authKey string) (*Manager, error) {
    if err := ValidateAuthKey(authKey); err != nil {
        return nil, err
    }
    return &Manager{authKey: authKey}, nil
}

// GenerateCloudInitScript generates the cloud-init script for Tailscale setup
func (m *Manager) GenerateCloudInitScript(hostname string) string {
    return fmt.Sprintf(`tailscale up --authkey=%s --hostname=%s --accept-routes --ssh`, m.authKey, hostname)
}

// GetAuthKey returns the auth key (for cloud-init injection)
func (m *Manager) GetAuthKey() string {
    return m.authKey
}

// Status represents Tailscale status
type Status struct {
    BackendState string
    Self         *Peer
    Peers        []Peer
}

// Peer represents a Tailscale peer
type Peer struct {
    HostName  string
    DNSName   string
    TailscaleIPs []string
    Online    bool
}

// GetStatus gets the status of a Tailscale node (from host perspective)
func GetStatus(ctx context.Context, hostname string) (*Status, error) {
    cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return nil, fmt.Errorf("tailscale status: %w: %s", err, stderr.String())
    }

    // Parse JSON output
    // Note: Full implementation would parse the JSON
    // For now, return basic status
    return &Status{
        BackendState: "Running",
    }, nil
}

// WaitForNode waits for a node to appear in Tailscale
func WaitForNode(ctx context.Context, hostname string, timeout int) error {
    // In practice, check tailscale status --json for the node
    // For now, this is a placeholder
    return nil
}

// RemoveNode removes a node from the Tailscale network
// Requires admin API access
func RemoveNode(ctx context.Context, hostname string) error {
    // This would use the Tailscale admin API
    // Placeholder for now
    return nil
}
```

**Step 4: Run tests**

```bash
go test ./pkg/tailscale/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/tailscale/
git commit -m "feat: add Tailscale integration package

- Hostname generation for VMs
- Auth key validation
- Cloud-init script generation
- Status checking (placeholder)"
```

---

### Task 10.2: Integrate Tailscale into Cloud-Init

**Files:**
- Modify: `pkg/flintlock/cloudinit.go`

**Step 1: Update CloudInitConfig**

```go
// Add to pkg/flintlock/cloudinit.go

import (
    "github.com/obra/stockyard/pkg/tailscale"
)

// Update CloudInitConfig struct:
type CloudInitConfig struct {
    Hostname          string
    Environment       map[string]string
    SSHAuthorizedKeys []string
    TailscaleAuthKey  string
    TailscaleHostname string  // Add this field
    WorkspacePath     string
    PostCreateScript  string
    GitRepo           string  // Add this field
    GitRef            string  // Add this field
}

// Update Generate() method to use TailscaleHostname:
func (c *CloudInitConfig) Generate() (string, error) {
    // ... existing code ...

    // Tailscale setup - use hostname if provided
    if c.TailscaleAuthKey != "" {
        tsHostname := c.TailscaleHostname
        if tsHostname == "" {
            tsHostname = c.Hostname
        }
        data.Runcmd = append(data.Runcmd,
            fmt.Sprintf("tailscale up --authkey=%s --hostname=%s --accept-routes --ssh",
                c.TailscaleAuthKey, tsHostname),
        )
    }

    // ... rest of method ...
}
```

**Step 2: Update task manager to use Tailscale hostname**

Add to `pkg/daemon/tasks.go` in CreateTask:

```go
// Generate Tailscale hostname
tailscaleHostname := tailscale.BuildHostname(taskID)

// Generate cloud-init with Tailscale hostname
cloudInit := &flintlock.CloudInitConfig{
    Hostname:          fmt.Sprintf("stockyard-%s", taskID),
    TailscaleHostname: tailscaleHostname,  // Add this
    // ... rest of config ...
}

// Update task record to include Tailscale hostname
task := &Task{
    ID:                taskID,
    TailscaleHostname: tailscaleHostname,  // Add field to Task struct
    // ... rest of task ...
}
```

**Step 3: Add TailscaleHostname to Task struct**

Add to `pkg/daemon/state.go`:

```go
// Update Task struct:
type Task struct {
    ID                string
    Name              string
    Repo              string
    Ref               string
    Command           string
    Status            string
    VMID              string
    TailscaleHostname string  // Add this field
    CreatedAt         time.Time
    StoppedAt         *time.Time
}

// Update migration to include new column (if needed):
// ALTER TABLE tasks ADD COLUMN tailscale_hostname TEXT;
```

**Step 4: Commit**

```bash
git add pkg/flintlock/cloudinit.go pkg/daemon/tasks.go pkg/daemon/state.go
git commit -m "feat: integrate Tailscale hostname into cloud-init

- Dedicated Tailscale hostname field
- Task records include Tailscale hostname
- Cloud-init generates proper tailscale up command"
```

---

### Task 10.3: Add Tailscale Auth Key from Secrets

**Files:**
- Modify: `pkg/daemon/tasks.go`

**Step 1: Update CreateTask to fetch Tailscale auth key**

Update `pkg/daemon/tasks.go` CreateTask method:

```go
// Get Tailscale auth key if enabled
var tailscaleKey string
var tailscaleHostname string
if !req.NoTailscale {
    key, err := tm.daemon.secrets.GetSecret(ctx, "tailscale-auth-key")
    if err != nil {
        log.Printf("Warning: could not get Tailscale auth key: %v", err)
        // Continue without Tailscale
    } else if err := tailscale.ValidateAuthKey(key); err != nil {
        log.Printf("Warning: invalid Tailscale auth key: %v", err)
    } else {
        tailscaleKey = key
        tailscaleHostname = tailscale.BuildHostname(taskID)
    }
}
```

**Step 2: Commit**

```bash
git add pkg/daemon/tasks.go
git commit -m "feat: fetch Tailscale auth key from secrets provider

- Get tailscale-auth-key from 1Password
- Validate key format
- Graceful fallback if key missing"
```

---

### Task 10.4: Add attach Command (SSH Wrapper)

**Files:**
- Create: `cmd/stockyard/attach.go`

**Step 1: Create attach command**

```go
// cmd/stockyard/attach.go
package main

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "syscall"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var attachCmd = &cobra.Command{
    Use:   "attach <task-id>",
    Short: "Attach to a running task via SSH",
    Long:  `Attach to a running task's VM via SSH through Tailscale.`,
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        // Get task details
        task, err := c.GetTask(context.Background(), taskID)
        if err != nil {
            return fmt.Errorf("failed to get task: %w", err)
        }

        if task == nil {
            return fmt.Errorf("task not found: %s", taskID)
        }

        if task.Status != "running" {
            return fmt.Errorf("task is not running (status: %s)", task.Status)
        }

        if task.TailscaleHostname == "" {
            return fmt.Errorf("task has no Tailscale hostname (was --no-tailscale used?)")
        }

        // Build SSH command
        sshHost := task.TailscaleHostname
        sshUser := "vscode"

        fmt.Printf("Connecting to %s@%s...\n", sshUser, sshHost)

        // Exec SSH (replaces current process)
        sshPath, err := exec.LookPath("ssh")
        if err != nil {
            return fmt.Errorf("ssh not found: %w", err)
        }

        sshArgs := []string{"ssh", "-o", "StrictHostKeyChecking=accept-new", fmt.Sprintf("%s@%s", sshUser, sshHost)}

        return syscall.Exec(sshPath, sshArgs, os.Environ())
    },
}

func init() {
    rootCmd.AddCommand(attachCmd)
}
```

**Step 2: Add GetTask to client**

Add to `pkg/client/client.go`:

```go
// GetTask gets a task by ID
func (c *Client) GetTask(ctx context.Context, taskID string) (*pb.Task, error) {
    resp, err := c.client.GetTask(ctx, &pb.GetTaskRequest{TaskId: taskID})
    if err != nil {
        return nil, err
    }
    return resp.Task, nil
}
```

**Step 3: Verify compilation**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard attach --help
```

**Step 4: Commit**

```bash
git add cmd/stockyard/attach.go pkg/client/client.go
git commit -m "feat: add attach command for SSH access

- SSH wrapper via Tailscale hostname
- Connects as vscode user
- Validates task is running and has Tailscale"
```

---

### Task 10.5: Test Tailscale SSH Access

This is a manual integration test:

**Step 1: Prerequisites**

- Tailscale installed on test machine
- Tailscale auth key in 1Password (op://Stockyard/{instance}/tailscale-auth-key)
- Flintlock and Firecracker set up

**Step 2: Create test task**

```bash
./bin/stockyard run --repo github.com/obra/test-repo --ref main -- bash -c "sleep 3600"
```

**Step 3: Verify Tailscale hostname**

```bash
./bin/stockyard list
# Should show tailscale hostname like stockyard-task-xxx
```

**Step 4: Test SSH**

```bash
./bin/stockyard attach <task-id>
# Should SSH into the VM
```

**Step 5: Verify from Tailscale**

```bash
tailscale status
# Should show the VM as a peer
```

---

**End of Part 5. Continue with Part 6: CLI Commands & Integration Tests.**

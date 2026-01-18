# Stockyard Code Quality and Feature Completion Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fix code quality issues (duplicate code, hardcoded paths, error handling) and complete missing features (restart command, CID mapping, owner tracking).

**Architecture:** Extract shared utilities, add config-driven paths, improve error visibility, add database schema migrations for CID and owner tracking, implement restart via stop+start pattern.

**Tech Stack:** Go, SQLite, Cobra CLI, gRPC

---

## Task 1: Extract Shared VM Utilities

**Files:**
- Create: `pkg/vmutil/vmutil.go`
- Create: `pkg/vmutil/vmutil_test.go`
- Modify: `cmd/stockyard/gc.go:291-307`
- Modify: `cmd/stockyard/resources.go:216-231`

**Context:** Both `gc.go` and `resources.go` have identical `isVMRunning()` implementations that check if a Firecracker process is running by reading a PID file and sending signal 0. This duplication risks bugs if one is updated and the other isn't.

**Step 1: Write the failing test for IsVMRunning**

Create `pkg/vmutil/vmutil_test.go`:

```go
// pkg/vmutil/vmutil_test.go
package vmutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIsVMRunning_NoPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	if IsVMRunning(tmpDir) {
		t.Error("expected false for missing pid file")
	}
}

func TestIsVMRunning_InvalidPid(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte("notanumber"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsVMRunning(tmpDir) {
		t.Error("expected false for invalid pid")
	}
}

func TestIsVMRunning_NonexistentPid(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsVMRunning(tmpDir) {
		t.Error("expected false for nonexistent pid")
	}
}

func TestIsVMRunning_RunningProcess(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsVMRunning(tmpDir) {
		t.Error("expected true for running process")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/vmutil/... -v`
Expected: FAIL with "package vmutil is not in std"

**Step 3: Write the implementation**

Create `pkg/vmutil/vmutil.go`:

```go
// Package vmutil provides shared utilities for VM management.
package vmutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// IsVMRunning checks if a VM in the given directory is running by checking
// if the process in firecracker.pid exists and is signalable.
func IsVMRunning(vmDir string) bool {
	pidFile := filepath.Join(vmDir, "firecracker.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}

	// Check if process exists by sending signal 0
	err = syscall.Kill(pid, 0)
	return err == nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/vmutil/... -v`
Expected: PASS

**Step 5: Update gc.go to use shared utility**

In `cmd/stockyard/gc.go`, replace the `isVMRunning` method:

```go
// Add import
import "github.com/obra/stockyard/pkg/vmutil"

// Delete lines 291-307 (the isVMRunning method)

// Update line 190 (and similar) from:
//   if gc.isVMRunning(vmDir) {
// to:
//   if vmutil.IsVMRunning(vmDir) {
```

**Step 6: Update resources.go to use shared utility**

In `cmd/stockyard/resources.go`, replace the `isVMRunning` method:

```go
// Add import
import "github.com/obra/stockyard/pkg/vmutil"

// Delete lines 216-231 (the isVMRunning method)

// Update line 188 (and similar) from:
//   if rc.isVMRunning(vmDir) {
// to:
//   if vmutil.IsVMRunning(vmDir) {
```

**Step 7: Run all tests to verify refactoring worked**

Run: `go test ./cmd/stockyard/... ./pkg/vmutil/... -v`
Expected: All tests PASS

**Step 8: Commit**

```bash
git add pkg/vmutil/ cmd/stockyard/gc.go cmd/stockyard/resources.go
git commit -m "refactor: extract shared IsVMRunning utility to pkg/vmutil"
```

---

## Task 2: Add Config-Driven Paths

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `cmd/stockyard/gc.go:60-61`
- Modify: `cmd/stockyard/resources.go:63-64`
- Modify: `pkg/daemon/tasks.go:291`
- Modify: `pkg/daemon/daemon.go:104`

**Context:** Multiple files hardcode `/var/lib/stockyard` paths instead of deriving them from config. This makes testing difficult and prevents flexible deployments.

**Step 1: Add DataDir to config**

In `pkg/config/config.go`, add a new field to DaemonConfig:

```go
type DaemonConfig struct {
	SocketPath string `json:"socket_path"`
	DataDir    string `json:"data_dir"`
}
```

Update `DefaultConfig()`:

```go
Daemon: DaemonConfig{
	SocketPath: "/var/run/stockyard/stockyard.sock",
	DataDir:    "/var/lib/stockyard",
},
```

**Step 2: Add helper methods to Config**

Add to `pkg/config/config.go`:

```go
// VMDir returns the path to VM state directories.
func (c *Config) VMDir() string {
	return filepath.Join(c.Daemon.DataDir, "vms", "stockyard")
}

// DHCPLeaseFile returns the path to the DHCP lease file.
func (c *Config) DHCPLeaseFile() string {
	return filepath.Join(c.Daemon.DataDir, "dnsmasq.leases")
}
```

Add `"path/filepath"` to imports.

**Step 3: Update gc.go to use config paths**

In `cmd/stockyard/gc.go`, change lines 60-61 from:

```go
vmDir:        "/var/lib/stockyard/vms/stockyard",
```

to:

```go
vmDir:        cfg.VMDir(),
```

**Step 4: Update resources.go to use config paths**

In `cmd/stockyard/resources.go`, change lines 63-64 from:

```go
vmDir:     "/var/lib/stockyard/vms/stockyard",
leaseFile: "/var/lib/stockyard/dnsmasq.leases",
```

to:

```go
vmDir:     cfg.VMDir(),
leaseFile: cfg.DHCPLeaseFile(),
```

**Step 5: Update tasks.go to use config**

In `pkg/daemon/tasks.go`, the `GetVMMAC` function at line 291 needs access to config. This requires passing the data dir through the daemon. For now, add a field to TaskManager:

```go
type TaskManager struct {
	daemon  *Daemon
	fc      *firecracker.Client
	dataDir string
}
```

Update `NewTaskManager` to accept dataDir:

```go
func NewTaskManager(d *Daemon, fcConfig *FirecrackerConfig, dataDir string) *TaskManager {
	tm := &TaskManager{
		daemon:  d,
		dataDir: dataDir,
	}
	// ... rest unchanged
}
```

Update `GetVMMAC`:

```go
func (tm *TaskManager) GetVMMAC(namespace, vmID string) (string, error) {
	macPath := filepath.Join(tm.dataDir, "vms", namespace, vmID, "mac_addr")
	// ... rest unchanged
}
```

**Step 6: Update daemon.go to pass dataDir**

In `pkg/daemon/daemon.go`, update the TaskManager creation to pass the data dir from config.

**Step 7: Run tests**

Run: `go test ./... -v`
Expected: PASS

**Step 8: Commit**

```bash
git add pkg/config/config.go cmd/stockyard/gc.go cmd/stockyard/resources.go pkg/daemon/tasks.go pkg/daemon/daemon.go
git commit -m "refactor: use config-driven paths instead of hardcoded /var/lib/stockyard"
```

---

## Task 3: Add CID Column for Snapshot Mapping

**Files:**
- Modify: `pkg/daemon/state.go:83-126` (schema migration)
- Modify: `pkg/daemon/state.go:141-162` (CreateTask)
- Modify: `pkg/daemon/state.go:16-27` (Task struct)
- Modify: `pkg/daemon/snapshots.go:62-93` (resolveTaskID)
- Create: `pkg/daemon/state_test.go` (add CID tests)

**Context:** The snapshot service receives VM requests via vsock with a CID (Context ID). Currently there's no mapping from CID to task ID, so it falls back to using the first running task. This is a bug when multiple VMs are running.

**Step 1: Write failing test for CID lookup**

Add to `pkg/daemon/state_test.go`:

```go
func TestState_GetTaskByCID(t *testing.T) {
	s, err := NewStateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	task := &Task{
		ID:        "task-123",
		Repo:      "github.com/test/repo",
		Ref:       "main",
		Command:   "test",
		Status:    "running",
		CID:       100,
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	found, err := s.GetTaskByCID(100)
	if err != nil {
		t.Fatalf("GetTaskByCID failed: %v", err)
	}
	if found.ID != "task-123" {
		t.Errorf("expected task-123, got %s", found.ID)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/... -run TestState_GetTaskByCID -v`
Expected: FAIL (CID field doesn't exist, GetTaskByCID doesn't exist)

**Step 3: Add CID field to Task struct**

In `pkg/daemon/state.go`, update the Task struct:

```go
type Task struct {
	ID                string
	Name              string
	Repo              string
	Ref               string
	Command           string
	Status            string
	VMID              string
	CID               uint32 // Firecracker vsock Context ID
	TailscaleHostname string
	CreatedAt         time.Time
	StoppedAt         *time.Time
}
```

**Step 4: Add CID column to schema migration**

In `pkg/daemon/state.go`, add to the migrations slice in `migrate()`:

```go
migrations := []string{
	`ALTER TABLE tasks ADD COLUMN tailscale_hostname TEXT`,
	`ALTER TABLE tasks ADD COLUMN cid INTEGER DEFAULT 0`,
}
```

**Step 5: Update CreateTask to save CID**

Update the INSERT query in `CreateTask`:

```go
query := `
INSERT INTO tasks (id, name, repo, ref, command, status, vmid, cid, tailscale_hostname, created_at, stopped_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`
_, err := s.db.Exec(query,
	task.ID,
	task.Name,
	task.Repo,
	task.Ref,
	task.Command,
	task.Status,
	task.VMID,
	task.CID,
	task.TailscaleHostname,
	task.CreatedAt,
	task.StoppedAt,
)
```

**Step 6: Update GetTask and ListTasks to read CID**

Add `cid` to the SELECT queries and scan into `task.CID`.

**Step 7: Add GetTaskByCID method**

```go
// GetTaskByCID retrieves a task by its Firecracker CID.
func (s *State) GetTaskByCID(cid uint32) (*Task, error) {
	query := `
	SELECT id, name, repo, ref, command, status, vmid, cid, tailscale_hostname, created_at, stopped_at
	FROM tasks
	WHERE cid = ? AND status = 'running'
	`
	row := s.db.QueryRow(query, cid)
	// ... same scanning logic as GetTask
}
```

**Step 8: Add UpdateTaskCID method**

```go
// UpdateTaskCID updates the CID of a task.
func (s *State) UpdateTaskCID(id string, cid uint32) error {
	query := `UPDATE tasks SET cid = ? WHERE id = ?`
	result, err := s.db.Exec(query, cid, id)
	if err != nil {
		return fmt.Errorf("failed to update task CID: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}
```

**Step 9: Update snapshots.go to use CID lookup**

In `pkg/daemon/snapshots.go`, update `resolveTaskID`:

```go
func (ss *SnapshotService) resolveTaskID(vmID string) (string, error) {
	if vmID == "unix-client" {
		// Testing fallback
		tasks, err := ss.daemon.state.ListTasks("running")
		if err != nil || len(tasks) == 0 {
			return "", fmt.Errorf("no running tasks")
		}
		return tasks[0].ID, nil
	}

	if strings.HasPrefix(vmID, "cid-") {
		cidStr := strings.TrimPrefix(vmID, "cid-")
		cid, err := strconv.ParseUint(cidStr, 10, 32)
		if err != nil {
			return "", fmt.Errorf("invalid CID: %s", vmID)
		}

		task, err := ss.daemon.state.GetTaskByCID(uint32(cid))
		if err != nil {
			return "", err
		}
		return task.ID, nil
	}

	return "", fmt.Errorf("unknown VM ID format: %s", vmID)
}
```

**Step 10: Run tests**

Run: `go test ./pkg/daemon/... -v`
Expected: PASS

**Step 11: Commit**

```bash
git add pkg/daemon/state.go pkg/daemon/state_test.go pkg/daemon/snapshots.go
git commit -m "feat: add CID tracking for proper snapshot VM identification"
```

---

## Task 4: Add Owner Tracking for Tasks

**Files:**
- Modify: `pkg/daemon/state.go` (schema, Task struct, queries)
- Modify: `pkg/daemon/tasks.go` (set owner on create)
- Modify: `pkg/dashboard/server.go:~line with "(unknown)"`
- Modify: `pkg/api/v1/stockyard.proto` (add owner field)

**Context:** The dashboard shows "(unknown)" for task owner because the field isn't tracked. Owner should come from the Tailscale WhoIs when creating tasks via the dashboard, or from the CLI user.

**Step 1: Add Owner field to Task struct**

In `pkg/daemon/state.go`:

```go
type Task struct {
	ID                string
	Name              string
	Repo              string
	Ref               string
	Command           string
	Status            string
	VMID              string
	CID               uint32
	Owner             string // Username who created the task
	TailscaleHostname string
	CreatedAt         time.Time
	StoppedAt         *time.Time
}
```

**Step 2: Add owner column migration**

```go
migrations := []string{
	`ALTER TABLE tasks ADD COLUMN tailscale_hostname TEXT`,
	`ALTER TABLE tasks ADD COLUMN cid INTEGER DEFAULT 0`,
	`ALTER TABLE tasks ADD COLUMN owner TEXT DEFAULT ''`,
}
```

**Step 3: Update all queries to include owner**

Update CreateTask, GetTask, GetTaskByCID, and ListTasks to handle the owner field.

**Step 4: Update dashboard server.go**

Replace `owner := "(unknown)"` with:

```go
owner := task.Owner
if owner == "" {
	owner = "(unknown)"
}
```

**Step 5: Run tests**

Run: `go test ./pkg/daemon/... ./pkg/dashboard/... -v`
Expected: PASS

**Step 6: Commit**

```bash
git add pkg/daemon/state.go pkg/dashboard/server.go
git commit -m "feat: add owner tracking for tasks"
```

---

## Task 5: Add Restart Command

**Files:**
- Create: `cmd/stockyard/restart.go`
- Create: `cmd/stockyard/restart_test.go`
- Modify: `pkg/api/v1/stockyard.proto` (add RestartTask RPC)
- Modify: `pkg/daemon/grpc.go` (implement RestartTask)
- Modify: `pkg/daemon/tasks.go` (add RestartTask method)
- Modify: `pkg/client/client.go` (add RestartTask)

**Context:** Users can stop VMs but cannot restart them without re-running from scratch. A restart command would stop the VM and start it again with the same configuration.

**Step 1: Write failing test**

Create `cmd/stockyard/restart_test.go`:

```go
package main

import "testing"

func TestRestartCommand_RequiresTaskID(t *testing.T) {
	err := restartCmd.Execute()
	if err == nil {
		t.Error("expected error for missing task ID")
	}
}

func TestRestartCommand_Help(t *testing.T) {
	if restartCmd.Use != "restart <task-id>" {
		t.Errorf("expected Use 'restart <task-id>', got %q", restartCmd.Use)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./cmd/stockyard/... -run TestRestartCommand -v`
Expected: FAIL (restartCmd undefined)

**Step 3: Create restart.go**

```go
// cmd/stockyard/restart.go
package main

import (
	"context"
	"fmt"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart <task-id>",
	Short: "Restart a stopped task",
	Long:  `Restart a stopped task by starting its VM again with the same configuration.`,
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

		if err := c.RestartTask(context.Background(), taskID); err != nil {
			return fmt.Errorf("failed to restart task: %w", err)
		}

		fmt.Printf("Task %s restarted\n", taskID)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restartCmd)
}
```

**Step 4: Add RestartTask to proto**

In `api/stockyard.proto`, add:

```protobuf
rpc RestartTask(RestartTaskRequest) returns (RestartTaskResponse);

message RestartTaskRequest {
    string task_id = 1;
}

message RestartTaskResponse {}
```

**Step 5: Regenerate proto**

Run: `make proto` or the appropriate protoc command.

**Step 6: Implement RestartTask in grpc.go**

```go
func (s *grpcServer) RestartTask(ctx context.Context, req *pb.RestartTaskRequest) (*pb.RestartTaskResponse, error) {
	if s.daemon.tasks == nil {
		return nil, status.Error(codes.Unavailable, "task manager not initialized")
	}
	if err := s.daemon.tasks.RestartTask(ctx, req.TaskId); err != nil {
		if strings.Contains(err.Error(), "task not found") {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to restart task: %v", err)
	}
	return &pb.RestartTaskResponse{}, nil
}
```

**Step 7: Implement RestartTask in TaskManager**

```go
// RestartTask restarts a stopped task.
func (tm *TaskManager) RestartTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	if task.Status != "stopped" {
		return fmt.Errorf("task %s is not stopped (status: %s)", taskID, task.Status)
	}

	// Re-create the VM with the same configuration
	// This reuses the existing ZFS dataset/workspace
	return tm.startVM(ctx, task)
}
```

Note: This requires extracting the VM start logic from CreateTask into a reusable `startVM` method.

**Step 8: Add RestartTask to client**

In `pkg/client/client.go`:

```go
func (c *Client) RestartTask(ctx context.Context, taskID string) error {
	_, err := c.client.RestartTask(ctx, &pb.RestartTaskRequest{TaskId: taskID})
	return err
}
```

**Step 9: Run tests**

Run: `go test ./cmd/stockyard/... ./pkg/daemon/... ./pkg/client/... -v`
Expected: PASS

**Step 10: Commit**

```bash
git add cmd/stockyard/restart.go cmd/stockyard/restart_test.go api/stockyard.proto pkg/api/v1/ pkg/daemon/grpc.go pkg/daemon/tasks.go pkg/client/client.go
git commit -m "feat: add restart command to restart stopped VMs"
```

---

## Task 6: Improve Tap Interface Matching

**Files:**
- Modify: `cmd/stockyard/resources.go:291-337`
- Modify: `cmd/stockyard/gc.go:249-289`
- Add tests for edge cases

**Context:** The tap interface matching uses `strings.HasPrefix(taskID, id)` where `id` is an 8-character prefix from the tap name. This could false-match if two task IDs share the same prefix.

**Step 1: Write failing test for tap matching**

Add to `cmd/stockyard/resources_test.go`:

```go
func TestResourceCollector_CollectTapInterfaces_PrefixCollision(t *testing.T) {
	// Test that tap-12345678 correctly matches task "12345678-abcd-..."
	// and doesn't incorrectly match "12345678-wxyz-..."
	rc := &ResourceCollector{
		cfg:     config.DefaultConfig(),
		taskIDs: map[string]string{
			"12345678-abcd-1234-5678-abcdef123456": "running",
			"12345678-wxyz-9876-5432-fedcba654321": "stopped",
		},
	}

	// The current implementation would match the first one found,
	// which is non-deterministic. We need a better approach.
}
```

**Step 2: Improve matching logic**

The fix is to store the tap name in the VM directory and look it up directly. The tap name is already written to `tap_name` file in each VM directory.

Update `collectTapInterfaces` to read tap names from VM directories:

```go
func (rc *ResourceCollector) collectTapInterfaces() {
	// Build map of tap name -> task ID from VM directories
	tapToTask := make(map[string]string)
	entries, _ := os.ReadDir(rc.vmDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskID := entry.Name()
		tapFile := filepath.Join(rc.vmDir, taskID, "tap_name")
		if data, err := os.ReadFile(tapFile); err == nil {
			tapName := strings.TrimSpace(string(data))
			tapToTask[tapName] = taskID
		}
	}

	// Now collect tap interfaces and match against known taps
	cmd := exec.Command("ip", "-o", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		name := strings.TrimSuffix(fields[1], ":")
		if !strings.HasPrefix(name, "tap-") {
			continue
		}

		status := "orphan"
		if taskID, known := tapToTask[name]; known {
			if taskStatus, ok := rc.taskIDs[taskID]; ok {
				status = taskStatus
				if status == "running" {
					status = "active"
				}
			}
		}

		rc.resources = append(rc.resources, Resource{
			ID:     name,
			Type:   "tap",
			Status: status,
		})
	}
}
```

**Step 3: Apply same fix to gc.go**

Update `findOrphanTaps` with the same pattern.

**Step 4: Run tests**

Run: `go test ./cmd/stockyard/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add cmd/stockyard/resources.go cmd/stockyard/gc.go cmd/stockyard/resources_test.go
git commit -m "fix: improve tap interface matching using tap_name file lookup"
```

---

## Task 7: Add Error Logging for Silent Failures

**Files:**
- Modify: `cmd/stockyard/resources.go` (multiple functions)
- Modify: `cmd/stockyard/gc.go` (multiple functions)

**Context:** Many functions silently `return` on errors, which hides problems. Add logging for debugging without changing behavior.

**Step 1: Add verbose flag**

Add a `--verbose` flag to both commands:

```go
var resourcesVerbose bool

func init() {
	resourcesCmd.Flags().BoolVarP(&resourcesVerbose, "verbose", "v", false, "Show verbose output including errors")
	rootCmd.AddCommand(resourcesCmd)
}
```

**Step 2: Update silent returns to log in verbose mode**

For example, in `loadDHCPLeases`:

```go
func (rc *ResourceCollector) loadDHCPLeases() {
	file, err := os.Open(rc.leaseFile)
	if err != nil {
		if rc.verbose {
			fmt.Fprintf(os.Stderr, "Warning: could not open DHCP lease file: %v\n", err)
		}
		return
	}
	defer file.Close()
	// ...
}
```

Add `verbose bool` field to `ResourceCollector` and `GarbageCollector`.

**Step 3: Run tests**

Run: `go test ./cmd/stockyard/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add cmd/stockyard/resources.go cmd/stockyard/gc.go
git commit -m "feat: add --verbose flag to show warnings for silent failures"
```

---

## Task 8: Remove GetLogs TODO (Document Current Behavior)

**Files:**
- Modify: `pkg/daemon/grpc.go:157-159`

**Context:** The `GetLogs` gRPC method is marked as unimplemented, but the CLI `logs` command actually works via SSH (using Tailscale). The TODO is misleading.

**Step 1: Update the comment**

Change the GetLogs implementation to have a clearer comment:

```go
func (s *grpcServer) GetLogs(req *pb.GetLogsRequest, stream grpc.ServerStreamingServer[pb.LogEntry]) error {
	// Note: Log streaming is handled via SSH through Tailscale in the CLI.
	// This gRPC endpoint is not used by the stockyard CLI.
	// It could be implemented for programmatic access if needed.
	return status.Error(codes.Unimplemented, "use SSH via Tailscale for log access")
}
```

**Step 2: Commit**

```bash
git add pkg/daemon/grpc.go
git commit -m "docs: clarify GetLogs is intentionally unimplemented (logs use SSH)"
```

---

## Task 9: Improve ZFS Dataset Orphan Detection

**Files:**
- Modify: `cmd/stockyard/resources.go:243-289`
- Modify: `cmd/stockyard/gc.go:204-247`

**Context:** The orphan detection only checks the last path component as task ID. Nested datasets (like `tank/stockyard/vms/taskid/snapshot`) could be misidentified.

**Step 1: Write failing test**

```go
func TestResourceCollector_CollectZFSDatasets_NestedDatasets(t *testing.T) {
	// Mock ZFS output with nested datasets
	// tank/stockyard/vms/task123
	// tank/stockyard/vms/task123/child  <- should not be treated as orphan "child"
}
```

**Step 2: Fix detection logic**

Only consider direct children of the base path, not nested datasets:

```go
func (rc *ResourceCollector) collectZFSDatasetsFromPath(basePath, resourceType string) {
	cmd := exec.Command("zfs", "list", "-H", "-r", "-d", "1", "-o", "name,used", basePath)
	// -d 1 limits depth to direct children only
	// ...
}
```

**Step 3: Run tests**

Run: `go test ./cmd/stockyard/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add cmd/stockyard/resources.go cmd/stockyard/gc.go
git commit -m "fix: limit ZFS dataset collection to direct children only"
```

---

## Summary

| Task | Description | Estimated Complexity |
|------|-------------|---------------------|
| 1 | Extract shared VM utilities | Low |
| 2 | Config-driven paths | Medium |
| 3 | CID column for snapshots | Medium |
| 4 | Owner tracking | Low |
| 5 | Restart command | High |
| 6 | Improve tap matching | Medium |
| 7 | Verbose error logging | Low |
| 8 | Document GetLogs | Trivial |
| 9 | Fix ZFS dataset detection | Low |

Total: 9 tasks

---

## Execution Notes

- Tasks 1-2 are foundational refactoring and should be done first
- Tasks 3-4 involve database schema changes and should be done together
- Task 5 (restart) is the most complex and depends on understanding the VM lifecycle
- Tasks 6-9 are independent fixes that can be done in any order

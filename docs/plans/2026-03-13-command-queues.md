# Command Queues Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable programmatic command execution inside VMs via named queues managed by the daemon.

**Architecture:** Extend the existing vsock shell protocol to accept arbitrary commands. Add queue management to the daemon that opens vsock connections, runs commands sequentially or concurrently per queue, and persists output. Expose via gRPC and CLI.

**Tech Stack:** Go, protobuf/gRPC, SQLite, vsock, PTY

**Spec:** `docs/specs/command-queues.md`

**Important notes:**
- After Chunk 1, the dashboard terminal will be broken (it sends OpenMessage without a command). Do not deploy Chunks 1-5 without also deploying Chunk 6. This is fine — we are the only user and can coordinate deployment.
- `CreateTaskRequest.Env` currently serves double duty: it carries both VM-level secrets (injected via cloud-init) and was intended for command-level env. When removing the deprecated `Command` and `Env` fields from the proto, keep `Env` on `CreateTaskRequest` for VM-level secrets (renamed to `vm_env` for clarity). Command-level env moves to `QueueCommand`. See Task 8 for details.
- `GetCommandStatus` and `StreamCommandOutput` use `command_id` only (no `task_id`). Command IDs are globally unique, so `task_id` is redundant. This deviates from the spec table but simplifies the API.

---

## File Map

**VM side (stockyard-shell):**
- Modify: `pkg/shell/protocol.go` — add `Command` and `Env` fields to `OpenMessage`
- Modify: `pkg/shell/session.go` — accept command+env, exec arbitrary commands with privilege dropping
- Modify: `cmd/stockyard-shell/main.go` — pass command/env from OpenMessage to NewSession
- Modify: `pkg/shell/protocol_test.go` — test new fields
- Modify: `pkg/shell/session_test.go` — test command execution and privilege dropping

**Daemon state:**
- Modify: `pkg/daemon/state.go` — add queues and commands tables, CRUD methods

**Daemon queue manager:**
- Create: `pkg/daemon/queue_manager.go` — queue lifecycle, command scheduling, vsock execution, output persistence
- Create: `pkg/daemon/queue_manager_test.go`

**Proto + gRPC:**
- Modify: `api/stockyard.proto` — new messages and RPCs
- Modify: `pkg/daemon/grpc.go` — implement new RPC handlers

**CLI:**
- Create: `cmd/stockyard/exec.go` — `stockyard exec` command
- Create: `cmd/stockyard/queue.go` — `stockyard queue` subcommands
- Create: `cmd/stockyard/command.go` — `stockyard command` subcommands

**Dashboard:**
- Modify: `pkg/dashboard/vsock_session.go` — add Command/Env to SendOpen
- Modify: `pkg/dashboard/terminal_handler.go` — route through admin queue

**Cleanup:**
- Modify: `pkg/daemon/tasks.go` — extend DestroyTask to clean up queues/commands/output files

---

## Chunk 1: VM Side — Protocol and Session

### Task 1: Extend OpenMessage with Command and Env

**Files:**
- Modify: `pkg/shell/protocol.go`
- Modify: `pkg/shell/protocol_test.go`

- [ ] **Step 1: Write test for OpenMessage with command and env**

In `pkg/shell/protocol_test.go`, add:

```go
func TestOpenMessageWithCommand(t *testing.T) {
	msg := OpenMessage{
		User:    "mooby",
		Term:    "xterm-256color",
		Cols:    120,
		Rows:    40,
		Command: []string{"claude", "-p", "implement OAuth"},
		Env:     map[string]string{"CLAUDE_MODEL": "opus"},
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded OpenMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.Command) != 3 || decoded.Command[0] != "claude" {
		t.Errorf("command = %v, want [claude -p implement OAuth]", decoded.Command)
	}
	if decoded.Env["CLAUDE_MODEL"] != "opus" {
		t.Errorf("env CLAUDE_MODEL = %q, want %q", decoded.Env["CLAUDE_MODEL"], "opus")
	}
}

func TestOpenMessageCommandRequired(t *testing.T) {
	msg := OpenMessage{
		User: "mooby",
		Term: "xterm",
		Cols: 80,
		Rows: 24,
		// Command intentionally empty
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded OpenMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.Command) != 0 {
		t.Errorf("expected empty command, got %v", decoded.Command)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/shell/ -run TestOpenMessage -v`
Expected: Compilation error — `Command` and `Env` fields don't exist on `OpenMessage`.

- [ ] **Step 3: Add Command and Env fields to OpenMessage**

In `pkg/shell/protocol.go`, update the `OpenMessage` struct:

```go
type OpenMessage struct {
	User    string            `json:"user"`
	Term    string            `json:"term"`
	Cols    int               `json:"cols"`
	Rows    int               `json:"rows"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/shell/ -run TestOpenMessage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/shell/protocol.go pkg/shell/protocol_test.go
git commit -m "Add Command and Env fields to OpenMessage"
```

### Task 2: Update NewSession to exec arbitrary commands with privilege dropping

**Files:**
- Modify: `pkg/shell/session.go`
- Modify: `pkg/shell/session_test.go`

- [ ] **Step 1: Write test for NewSession with a command**

In `pkg/shell/session_test.go`, add:

```go
func TestNewSessionRequiresCommand(t *testing.T) {
	_, err := NewSession("", "xterm", 80, 24, nil, nil)
	if err == nil {
		t.Error("expected error for nil command")
	}
	_, err = NewSession("", "xterm", 80, 24, []string{}, nil)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestNewSessionWithCommand(t *testing.T) {
	// This test runs a simple command (echo) to verify
	// NewSession can exec arbitrary commands.
	// Note: Does not test privilege dropping (requires root).
	cmd := []string{"echo", "hello from stockyard"}
	session, err := NewSession("", "xterm", 80, 24, cmd, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	// Read output from PTY
	buf := make([]byte, 4096)
	n, err := session.PTY().Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "hello from stockyard") {
		t.Errorf("output = %q, want to contain %q", output, "hello from stockyard")
	}

	exitCode, err := session.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
}

func TestNewSessionWithEnv(t *testing.T) {
	env := map[string]string{"STOCKYARD_TEST_VAR": "test_value_123"}
	cmd := []string{"sh", "-c", "echo $STOCKYARD_TEST_VAR"}
	session, err := NewSession("", "xterm", 80, 24, cmd, env)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	buf := make([]byte, 4096)
	n, err := session.PTY().Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "test_value_123") {
		t.Errorf("output = %q, want to contain %q", output, "test_value_123")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/shell/ -run "TestNewSessionWith" -v`
Expected: Compilation error — `NewSession` signature doesn't match.

- [ ] **Step 3: Update NewSession to accept command and env**

In `pkg/shell/session.go`, update `NewSession`:

```go
// NewSession creates a new session that executes the given command.
// If running as root and username is provided, drops privileges to that user.
// Environment variables from env are merged on top of the system environment.
func NewSession(username, term string, cols, rows int, command []string, env map[string]string) (*Session, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("command is required")
	}

	cmd := exec.Command(command[0], command[1:]...)

	// Build environment: start with system env, overlay with provided env
	cmdEnv := os.Environ()
	cmdEnv = append(cmdEnv, "TERM="+term)
	for k, v := range env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	cmd.Env = cmdEnv

	// Drop privileges if running as root and username is specified
	if username != "" && os.Getuid() == 0 {
		u, err := user.Lookup(username)
		if err != nil {
			return nil, fmt.Errorf("lookup user %q: %w", username, err)
		}
		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse uid: %w", err)
		}
		gid, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse gid: %w", err)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid: uint32(uid),
				Gid: uint32(gid),
			},
		}
		cmd.Dir = u.HomeDir
	}

	// Start with PTY
	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	return &Session{
		user:    username,
		cmd:     cmd,
		ptyFile: ptyFile,
	}, nil
}
```

Remove the `ValidateUser` function (no longer needed as a separate check).

Add imports: `"os/user"`, `"strconv"`, `"syscall"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/shell/ -run "TestNewSessionWith" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/shell/session.go pkg/shell/session_test.go
git commit -m "Update NewSession to exec arbitrary commands with privilege dropping"
```

### Task 3: Update stockyard-shell to use new NewSession signature

**Files:**
- Modify: `cmd/stockyard-shell/main.go`

- [ ] **Step 1: Update handleConnection to pass command and env to NewSession**

In `cmd/stockyard-shell/main.go`, update `handleConnection`. Replace the `NewSession` call:

```go
// Validate command is present
if len(openMsg.Command) == 0 {
	log.Printf("No command specified in open message")
	sendError(conn, "command is required")
	return
}

log.Printf("Executing command for user %q: %v (term=%s, size=%dx%d)",
	openMsg.User, openMsg.Command, openMsg.Term, openMsg.Cols, openMsg.Rows)

// Create session with the specified command
session, err := shell.NewSession(openMsg.User, openMsg.Term, openMsg.Cols, openMsg.Rows, openMsg.Command, openMsg.Env)
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go build ./cmd/stockyard-shell/`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add cmd/stockyard-shell/main.go
git commit -m "Update stockyard-shell to pass command from OpenMessage to NewSession"
```

---

## Chunk 2: Daemon State — SQLite Schema for Queues and Commands

### Task 4: Add queue and command tables to SQLite

**Files:**
- Modify: `pkg/daemon/state.go`
- Modify: `pkg/daemon/state_test.go`

- [ ] **Step 1: Write tests for queue and command CRUD**

In `pkg/daemon/state_test.go`, add:

```go
func TestCreateAndGetQueue(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	q := &Queue{
		TaskID:     "task-1",
		Name:       "default",
		Mode:       "serial",
		Protected:  true,
		Status:     "active",
		CreatedAt:  time.Now(),
	}
	if err := state.CreateQueue(q); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	got, err := state.GetQueue("task-1", "default")
	if err != nil {
		t.Fatalf("GetQueue: %v", err)
	}
	if got.Mode != "serial" {
		t.Errorf("mode = %q, want %q", got.Mode, "serial")
	}
	if !got.Protected {
		t.Error("expected protected = true")
	}
}

func TestListQueues(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	state.CreateQueue(&Queue{TaskID: "task-1", Name: "default", Mode: "serial", Protected: true, Status: "active", CreatedAt: time.Now()})
	state.CreateQueue(&Queue{TaskID: "task-1", Name: "admin", Mode: "concurrent", Protected: true, Status: "active", CreatedAt: time.Now()})

	queues, err := state.ListQueues("task-1")
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}
	if len(queues) != 2 {
		t.Errorf("got %d queues, want 2", len(queues))
	}
}

func TestDestroyProtectedQueue(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	state.CreateQueue(&Queue{TaskID: "task-1", Name: "default", Mode: "serial", Protected: true, Status: "active", CreatedAt: time.Now()})

	err = state.DestroyQueue("task-1", "default")
	if err == nil {
		t.Error("expected error destroying protected queue")
	}
}

func TestCreateAndGetCommand(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	cmd := &Command{
		ID:            "cmd-1",
		TaskID:        "task-1",
		QueueName:     "default",
		Command:       []string{"echo", "hello"},
		Status:        "pending",
		StopOnFailure: true,
		CreatedAt:     time.Now(),
	}
	if err := state.CreateCommand(cmd); err != nil {
		t.Fatalf("CreateCommand: %v", err)
	}

	got, err := state.GetCommand("cmd-1")
	if err != nil {
		t.Fatalf("GetCommand: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("status = %q, want %q", got.Status, "pending")
	}
	if !got.StopOnFailure {
		t.Error("expected stop_on_failure = true")
	}
}

func TestListCommandsByQueue(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	for i, name := range []string{"cmd-1", "cmd-2", "cmd-3"} {
		state.CreateCommand(&Command{
			ID: name, TaskID: "task-1", QueueName: "default",
			Command: []string{"echo", name}, Status: "pending",
			StopOnFailure: true, CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	cmds, err := state.ListCommands("task-1", "default")
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	if len(cmds) != 3 {
		t.Errorf("got %d commands, want 3", len(cmds))
	}
}

func TestUpdateCommandStatus(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	state.CreateCommand(&Command{
		ID: "cmd-1", TaskID: "task-1", QueueName: "default",
		Command: []string{"echo"}, Status: "pending",
		StopOnFailure: true, CreatedAt: time.Now(),
	})

	if err := state.UpdateCommandStatus("cmd-1", "running"); err != nil {
		t.Fatalf("UpdateCommandStatus: %v", err)
	}

	got, err := state.GetCommand("cmd-1")
	if err != nil {
		t.Fatalf("GetCommand: %v", err)
	}
	if got.Status != "running" {
		t.Errorf("status = %q, want %q", got.Status, "running")
	}
}

func TestUpdateCommandExitCode(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	state.CreateCommand(&Command{
		ID: "cmd-1", TaskID: "task-1", QueueName: "default",
		Command: []string{"echo"}, Status: "running",
		StopOnFailure: true, CreatedAt: time.Now(),
	})

	exitCode := 1
	if err := state.UpdateCommandExit("cmd-1", exitCode); err != nil {
		t.Fatalf("UpdateCommandExit: %v", err)
	}

	got, err := state.GetCommand("cmd-1")
	if err != nil {
		t.Fatalf("GetCommand: %v", err)
	}
	if got.ExitCode == nil || *got.ExitCode != 1 {
		t.Errorf("exit_code = %v, want 1", got.ExitCode)
	}
}

func TestDeleteCommandsByTask(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	state.CreateCommand(&Command{ID: "cmd-1", TaskID: "task-1", QueueName: "default", Command: []string{"echo"}, Status: "pending", CreatedAt: time.Now()})
	state.CreateCommand(&Command{ID: "cmd-2", TaskID: "task-1", QueueName: "default", Command: []string{"echo"}, Status: "pending", CreatedAt: time.Now()})

	if err := state.DeleteCommandsByTask("task-1"); err != nil {
		t.Fatalf("DeleteCommandsByTask: %v", err)
	}

	cmds, err := state.ListCommands("task-1", "default")
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("got %d commands, want 0", len(cmds))
	}
}

func TestFlushQueue(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	state.CreateCommand(&Command{ID: "cmd-1", TaskID: "task-1", QueueName: "default", Command: []string{"a"}, Status: "completed", CreatedAt: time.Now()})
	state.CreateCommand(&Command{ID: "cmd-2", TaskID: "task-1", QueueName: "default", Command: []string{"b"}, Status: "pending", CreatedAt: time.Now()})
	state.CreateCommand(&Command{ID: "cmd-3", TaskID: "task-1", QueueName: "default", Command: []string{"c"}, Status: "pending", CreatedAt: time.Now()})

	if err := state.FlushQueueCommands("task-1", "default"); err != nil {
		t.Fatalf("FlushQueueCommands: %v", err)
	}

	cmds, err := state.ListCommands("task-1", "default")
	if err != nil {
		t.Fatalf("ListCommands: %v", err)
	}
	// Only completed command should remain
	if len(cmds) != 1 {
		t.Errorf("got %d commands after flush, want 1 (completed one)", len(cmds))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/daemon/ -run "TestCreate.*Queue|TestList.*Queue|TestDestroy.*Queue|TestCreate.*Command|TestList.*Command|TestUpdate.*Command|TestDelete.*Command|TestFlush" -v`
Expected: Compilation errors — `Queue`, `Command` types and methods don't exist.

- [ ] **Step 3: Add Queue and Command types and schema**

In `pkg/daemon/state.go`, add the types:

```go
// Queue represents a named command queue for a task.
type Queue struct {
	TaskID    string
	Name      string
	Mode      string // "serial" or "concurrent"
	Protected bool
	Status    string // "active" or "stopped"
	CreatedAt time.Time
}

// Command represents a queued command for execution.
type Command struct {
	ID            string
	TaskID        string
	QueueName     string
	Command       []string
	Env           map[string]string
	Status        string // "pending", "running", "completed", "failed"
	ExitCode      *int
	StopOnFailure bool
	OutputPath    string
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
}
```

Add to the `migrate()` function:

```go
CREATE TABLE IF NOT EXISTS queues (
    task_id TEXT NOT NULL,
    name TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'serial',
    protected BOOLEAN NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL,
    PRIMARY KEY (task_id, name)
);

CREATE TABLE IF NOT EXISTS commands (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    queue_name TEXT NOT NULL,
    command TEXT NOT NULL,
    env TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    exit_code INTEGER,
    stop_on_failure BOOLEAN NOT NULL DEFAULT 1,
    output_path TEXT,
    created_at DATETIME NOT NULL,
    started_at DATETIME,
    finished_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_commands_task_queue ON commands(task_id, queue_name);
CREATE INDEX IF NOT EXISTS idx_commands_status ON commands(status);
```

Note: `command` is stored as JSON-encoded `[]string`. `env` is stored as JSON-encoded `map[string]string`.

- [ ] **Step 4: Implement CRUD methods**

Add the following methods to `State` in `pkg/daemon/state.go`:

- `CreateQueue(q *Queue) error`
- `GetQueue(taskID, name string) (*Queue, error)`
- `ListQueues(taskID string) ([]*Queue, error)`
- `UpdateQueueStatus(taskID, name, status string) error`
- `DestroyQueue(taskID, name string) error` — fails if `protected`
- `DeleteQueuesByTask(taskID string) error`
- `CreateCommand(c *Command) error` — JSON-encode `Command` and `Env` fields
- `GetCommand(id string) (*Command, error)` — JSON-decode `Command` and `Env` fields
- `ListCommands(taskID, queueName string) ([]*Command, error)` — ordered by `created_at`
- `UpdateCommandStatus(id, status string) error` — sets `started_at` when status becomes `running`
- `UpdateCommandExit(id string, exitCode int) error` — sets `exit_code` and `finished_at`
- `FlushQueueCommands(taskID, queueName string) error` — deletes pending commands only
- `DeleteCommandsByTask(taskID string) error`

Use `encoding/json` for marshaling/unmarshaling `command` and `env` columns.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/daemon/ -run "TestCreate.*Queue|TestList.*Queue|TestDestroy.*Queue|TestCreate.*Command|TestList.*Command|TestUpdate.*Command|TestDelete.*Command|TestFlush" -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/daemon/state.go pkg/daemon/state_test.go
git commit -m "Add SQLite schema and CRUD for queues and commands"
```

---

## Chunk 3: Daemon — Queue Manager

### Task 5: Implement QueueManager

**Files:**
- Create: `pkg/daemon/queue_manager.go`
- Create: `pkg/daemon/queue_manager_test.go`

The QueueManager is responsible for:
- Creating built-in queues (`default`, `admin`) when a task starts
- Accepting commands and scheduling them per queue mode
- Opening vsock connections to the VM's stockyard-shell
- Reading output and persisting to disk
- Tracking command lifecycle (pending → running → completed/failed)
- Stopping serial queues on failure when `stop_on_failure` is true

- [ ] **Step 1: Write tests for QueueManager core logic**

In `pkg/daemon/queue_manager_test.go`, write tests for:

- `TestQueueManagerInitBuiltinQueues` — verifies `default` (serial, protected) and `admin` (concurrent, protected) are created
- `TestQueueManagerCreateCustomQueue` — creating a new queue with chosen mode
- `TestQueueManagerQueueCommand` — submitting a command returns a command ID
- `TestQueueManagerDestroyProtectedQueue` — cannot destroy built-in queues
- `TestQueueManagerDestroyCustomQueue` — can destroy user-created queues
- `TestQueueManagerFlushQueue` — clears only pending commands

Use `NewStateInMemory()` for tests. The vsock execution part can be tested with a mock or integration test — unit tests should focus on state management and scheduling logic.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/daemon/ -run TestQueueManager -v`
Expected: Compilation error — `QueueManager` doesn't exist.

- [ ] **Step 3: Implement QueueManager**

Create `pkg/daemon/queue_manager.go` with:

```go
type QueueManager struct {
    state   *State
    dataDir string
    mu      sync.Mutex
}

func NewQueueManager(state *State, dataDir string) *QueueManager

// InitQueues creates the built-in queues for a task
func (qm *QueueManager) InitQueues(taskID string) error

// CreateQueue creates a custom queue
func (qm *QueueManager) CreateQueue(taskID, name, mode string) error

// QueueCommand appends a command to a queue and returns the command ID.
// For serial queues, triggers execution if no command is running.
// For concurrent queues, triggers execution immediately.
func (qm *QueueManager) QueueCommand(taskID, queueName string, command []string, env map[string]string, stopOnFailure bool) (string, error)

// GetCommandStatus returns command info
func (qm *QueueManager) GetCommandStatus(commandID string) (*Command, error)

// GetQueueStatus returns queue info with its commands
func (qm *QueueManager) GetQueueStatus(taskID, queueName string) (*Queue, []*Command, error)

// ListQueues returns all queues for a task
func (qm *QueueManager) ListQueues(taskID string) ([]*Queue, error)

// FlushQueue clears pending commands
func (qm *QueueManager) FlushQueue(taskID, queueName string) error

// DestroyQueue removes a non-protected queue
func (qm *QueueManager) DestroyQueue(taskID, queueName string) error

// CleanupTask removes all queues, commands, and output files for a task
func (qm *QueueManager) CleanupTask(taskID string) error
```

Command ID generation: use `fmt.Sprintf("cmd-%s", firecracker.GenerateVMID())` or a UUID.

Output path: `filepath.Join(dataDir, "tasks", taskID, "commands", commandID, "output.log")`

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/daemon/ -run TestQueueManager -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/queue_manager.go pkg/daemon/queue_manager_test.go
git commit -m "Add QueueManager for command scheduling and lifecycle"
```

### Task 6: Implement vsock command execution in QueueManager

**Files:**
- Modify: `pkg/daemon/queue_manager.go`

This is the core execution loop. When a command needs to run:

1. Look up the task's vsock path from state
2. Connect to Firecracker's vsock UDS
3. Send `CONNECT 52\n`, read `OK` response
4. Send `MsgOpen` with command, env, and user
5. Read `MsgData` messages, write to output file
6. On `MsgExit`, record exit code, update status
7. For serial queues: check `stop_on_failure`, run next or stop
8. For concurrent queues: just record the result

- [ ] **Step 1: Implement executeCommand method**

Add to `pkg/daemon/queue_manager.go`:

```go
// executeCommand opens a vsock connection, runs a command, and persists output.
// This runs in a goroutine. On completion, it updates state and triggers
// the next command for serial queues.
func (qm *QueueManager) executeCommand(taskID, commandID string)
```

The method should:
- Get the task's vsock path from state: `qm.state.GetTask(taskID)` → `task.VsockPath`
- Create output directory: `os.MkdirAll(outputDir, 0755)`
- Open output file for writing
- Connect to vsock UDS using the same pattern as `terminal_handler.go:createVsockSession`:
  - `net.Dial("unix", vsockPath)`
  - Write `CONNECT 52\n`
  - Read `OK` response
- Send `MsgOpen` with command, env, user (from config)
- Loop reading `MsgData` → write to file, `MsgExit` → record exit code
- After exit: update command status, check stop_on_failure, trigger next command

- [ ] **Step 2: Implement triggerNext for serial queues**

```go
// triggerNext checks if the next pending command in a serial queue should run.
func (qm *QueueManager) triggerNext(taskID, queueName string)
```

- Get queue from state
- If queue status is "stopped", return
- Find next pending command in queue
- If found, call `executeCommand` in a goroutine

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go build ./pkg/daemon/`
Expected: Success

- [ ] **Step 4: Commit**

```bash
git add pkg/daemon/queue_manager.go
git commit -m "Add vsock command execution to QueueManager"
```

### Task 7: Wire QueueManager into Daemon

**Files:**
- Modify: `pkg/daemon/daemon.go`
- Modify: `pkg/daemon/tasks.go`

- [ ] **Step 1: Add QueueManager to Daemon struct**

In `pkg/daemon/daemon.go`, add `queueManager *QueueManager` field to the `Daemon` struct. Initialize it in `New()`:

```go
d.queueManager = NewQueueManager(state, cfg.Daemon.DataDir)
```

Add accessor:

```go
func (d *Daemon) QueueManager() *QueueManager {
    return d.queueManager
}
```

- [ ] **Step 2: Initialize built-in queues in CreateTask**

In `pkg/daemon/tasks.go`, after the task is recorded in the database (after `tm.daemon.state.CreateTask(task)`), add:

```go
// Initialize built-in command queues
if err := tm.daemon.queueManager.InitQueues(taskID); err != nil {
    log.Printf("Warning: failed to initialize queues for task %s: %v", taskID, err)
}
```

- [ ] **Step 3: Add cleanup to DestroyTask**

In `pkg/daemon/tasks.go`, in `DestroyTask`, before deleting the task from the database, add:

```go
// Clean up command queues and output files
if err := tm.daemon.queueManager.CleanupTask(taskID); err != nil {
    fmt.Printf("Warning: failed to cleanup queues for %s: %v\n", taskID, err)
}
```

- [ ] **Step 4: Verify compilation**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go build ./cmd/stockyardd/`
Expected: Success

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/daemon.go pkg/daemon/tasks.go
git commit -m "Wire QueueManager into Daemon lifecycle"
```

---

## Chunk 4: Proto and gRPC

### Task 8: Add queue and command RPCs to proto

**Files:**
- Modify: `api/stockyard.proto`

- [ ] **Step 1: Add new messages and RPCs**

Add to `api/stockyard.proto`:

```protobuf
// Queue management
rpc CreateQueue(CreateQueueRequest) returns (CreateQueueResponse);
rpc ListQueues(ListQueuesRequest) returns (ListQueuesResponse);
rpc GetQueueStatus(GetQueueStatusRequest) returns (GetQueueStatusResponse);
rpc FlushQueue(FlushQueueRequest) returns (FlushQueueResponse);
rpc DestroyQueue(DestroyQueueRequest) returns (DestroyQueueResponse);

// Command execution
rpc QueueCommand(QueueCommandRequest) returns (QueueCommandResponse);
rpc GetCommandStatus(GetCommandStatusRequest) returns (GetCommandStatusResponse);
rpc StreamCommandOutput(StreamCommandOutputRequest) returns (stream CommandOutputChunk);
```

Add message definitions:

```protobuf
message CreateQueueRequest {
    string task_id = 1;
    string queue_name = 2;
    string mode = 3; // "serial" or "concurrent", default "serial"
}
message CreateQueueResponse {}

message ListQueuesRequest {
    string task_id = 1;
}
message ListQueuesResponse {
    repeated QueueInfo queues = 1;
}

message GetQueueStatusRequest {
    string task_id = 1;
    string queue_name = 2;
}
message GetQueueStatusResponse {
    QueueInfo queue = 1;
    repeated CommandInfo commands = 2;
}

message FlushQueueRequest {
    string task_id = 1;
    string queue_name = 2;
}
message FlushQueueResponse {}

message DestroyQueueRequest {
    string task_id = 1;
    string queue_name = 2;
}
message DestroyQueueResponse {}

message QueueCommandRequest {
    string task_id = 1;
    string queue_name = 2;
    repeated string command = 3;
    map<string, string> env = 4;
    bool stop_on_failure = 5;
}
message QueueCommandResponse {
    string command_id = 1;
}

message GetCommandStatusRequest {
    string command_id = 1;
}
message GetCommandStatusResponse {
    CommandInfo command = 1;
}

message StreamCommandOutputRequest {
    string command_id = 1;
    bool follow = 2;
}
message CommandOutputChunk {
    bytes data = 1;
}

message QueueInfo {
    string name = 1;
    string mode = 2;
    bool protected = 3;
    string status = 4;
}

message CommandInfo {
    string id = 1;
    string queue_name = 2;
    repeated string command = 3;
    string status = 4;
    int32 exit_code = 5;
    bool stop_on_failure = 6;
    string created_at = 7;
    string started_at = 8;
    string finished_at = 9;
}
```

In `CreateTaskRequest`:
- Remove the `command` field (mark as `reserved 2`).
- Rename `env` to `vm_env` (field number 3) — this carries VM-level secrets injected via cloud-init, not command-level env. Update `cmd/stockyard/run.go` (the `--env` flag populates this) and `pkg/daemon/grpc.go` (reads `req.VmEnv` instead of `req.Env`) and `pkg/daemon/tasks.go` (`CreateTaskRequest.Env` field).

- [ ] **Step 2: Regenerate proto**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && make proto`
Expected: New Go files generated in `pkg/api/v1/`

- [ ] **Step 3: Fix any compilation errors from deprecated field removal**

Update `cmd/stockyard/run.go` and `pkg/daemon/grpc.go` to remove references to `CreateTaskRequest.Command` and `CreateTaskRequest.Env` if removed. If marked reserved instead, no changes needed.

- [ ] **Step 4: Commit**

```bash
git add api/stockyard.proto pkg/api/v1/ cmd/stockyard/run.go pkg/daemon/grpc.go
git commit -m "Add queue and command RPCs to proto, deprecate CreateTaskRequest.Command"
```

### Task 9: Implement gRPC handlers for queue and command RPCs

**Files:**
- Modify: `pkg/daemon/grpc.go`

- [ ] **Step 1: Implement queue management RPCs**

Add to `pkg/daemon/grpc.go`:

- `CreateQueue` — calls `queueManager.CreateQueue`
- `ListQueues` — calls `queueManager.ListQueues`
- `GetQueueStatus` — calls `queueManager.GetQueueStatus`
- `FlushQueue` — calls `queueManager.FlushQueue`
- `DestroyQueue` — calls `queueManager.DestroyQueue`

- [ ] **Step 2: Implement command execution RPCs**

- `QueueCommand` — calls `queueManager.QueueCommand`, returns command ID
- `GetCommandStatus` — calls `queueManager.GetCommandStatus`
- `StreamCommandOutput` — opens the command's output file, reads and streams chunks. If `follow` is true, tails the file until the command completes.

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go build ./cmd/stockyardd/`
Expected: Success

- [ ] **Step 4: Commit**

```bash
git add pkg/daemon/grpc.go
git commit -m "Implement gRPC handlers for queue and command RPCs"
```

---

## Chunk 5: CLI Commands

### Task 10: Add `stockyard exec` command

**Files:**
- Create: `cmd/stockyard/exec.go`

- [ ] **Step 1: Implement exec command**

`stockyard exec <task-id> [--queue=default] [--no-stop-on-failure] [--env KEY=val] -- <command...>`

Calls `QueueCommand` gRPC and prints the command ID. Default queue is `default`.

- [ ] **Step 2: Verify it compiles and shows help**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go build -o bin/stockyard ./cmd/stockyard && bin/stockyard exec --help`
Expected: Shows usage

- [ ] **Step 3: Commit**

```bash
git add cmd/stockyard/exec.go
git commit -m "Add stockyard exec CLI command"
```

### Task 11: Add `stockyard queue` subcommands

**Files:**
- Create: `cmd/stockyard/queue.go`

- [ ] **Step 1: Implement queue subcommands**

- `stockyard queue create <task-id> <name> [--mode=serial]`
- `stockyard queue list <task-id>`
- `stockyard queue status <task-id> [queue-name]` — defaults to `default`
- `stockyard queue flush <task-id> <queue-name>`
- `stockyard queue destroy <task-id> <queue-name>`

- [ ] **Step 2: Verify it compiles and shows help**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go build -o bin/stockyard ./cmd/stockyard && bin/stockyard queue --help`
Expected: Shows subcommands

- [ ] **Step 3: Commit**

```bash
git add cmd/stockyard/queue.go
git commit -m "Add stockyard queue CLI subcommands"
```

### Task 12: Add `stockyard command` subcommands

**Files:**
- Create: `cmd/stockyard/command.go`

- [ ] **Step 1: Implement command subcommands**

- `stockyard command status <task-id> <command-id>`
- `stockyard command logs <task-id> <command-id> [--follow]`

`logs` calls `StreamCommandOutput` and prints to stdout. With `--follow`, keeps streaming until the command completes.

- [ ] **Step 2: Verify it compiles and shows help**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go build -o bin/stockyard ./cmd/stockyard && bin/stockyard command --help`
Expected: Shows subcommands

- [ ] **Step 3: Commit**

```bash
git add cmd/stockyard/command.go
git commit -m "Add stockyard command CLI subcommands"
```

---

## Chunk 6: Dashboard Integration

### Task 13: Update dashboard to route through admin queue

**Files:**
- Modify: `pkg/dashboard/vsock_session.go`
- Modify: `pkg/dashboard/terminal_handler.go`

- [ ] **Step 1: Add Command and Env to VsockSession.SendOpen**

In `pkg/dashboard/vsock_session.go`, update `SendOpen` to accept and send command:

```go
func (s *VsockSession) SendOpen(term string, cols, rows int, command []string, env map[string]string) error {
	msg := shell.OpenMessage{
		User:    s.User,
		Term:    term,
		Cols:    cols,
		Rows:    rows,
		Command: command,
		Env:     env,
	}
	// ...
}
```

- [ ] **Step 2: Update TerminalHandler to route through admin queue**

In `pkg/dashboard/terminal_handler.go`, update `ServeHTTP` to use the daemon's QueueCommand on the `admin` queue instead of directly creating a vsock session. The terminal handler should:
1. Call `QueueCommand(taskID, "admin", ["login", "-f", user], {}, false)` to get a command ID
2. Use the existing vsock session mechanism to connect to the command

The dashboard needs a live bidirectional connection (WebSocket ↔ vsock), which is different from the QueueManager's batch pattern (write output to file). The approach: the dashboard bypasses QueueManager for the actual vsock I/O (keeping its existing WebSocket-to-vsock bridge) but registers the session as a command in the admin queue for tracking. Concretely:
1. Call `queueManager.QueueCommand(taskID, "admin", ["login", "-f", user], nil, false)` to get a command ID and record the session.
2. Continue using the existing direct vsock connection pattern for live I/O.
3. On session close, update the command status via `state.UpdateCommandStatus` / `state.UpdateCommandExit`.
This means the admin queue tracks what's happening but doesn't manage the connections. That's fine — the admin queue's value is visibility, not orchestration.

- [ ] **Step 3: Update all callers of SendOpen**

Update the call in `terminal_handler.go:createVsockSession` to pass the login command:

```go
if err := session.SendOpen("xterm-256color", cols, rows, []string{"login", "-f", user}, nil); err != nil {
```

- [ ] **Step 4: Fix tests**

Update any tests in `pkg/dashboard/` that call `SendOpen` to pass the new parameters.

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/dashboard/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/dashboard/vsock_session.go pkg/dashboard/terminal_handler.go pkg/dashboard/*_test.go
git commit -m "Update dashboard to send explicit login command in OpenMessage"
```

---

## Chunk 7: Security Review

### Task 14: Security review of privilege dropping

- [ ] **Step 1: Dispatch a security-focused reviewer**

Have a reviewer examine:
- `pkg/shell/session.go` — privilege dropping via `SysProcAttr.Credential`
- Verify that env injection cannot be used to escalate privileges (e.g., `LD_PRELOAD`)
- Verify that the command cannot escape the PTY or access root-owned resources
- Consider whether `cmd.Dir` should be set to the VM user's home or workspace

- [ ] **Step 2: Address findings**

Fix any issues identified by the review.

- [ ] **Step 3: Commit**

```bash
git add pkg/shell/session.go
git commit -m "Address security review findings for privilege dropping"
```

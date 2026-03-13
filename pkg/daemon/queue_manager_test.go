package daemon

import (
	"strings"
	"testing"
	"time"
)

// newTestQueueManager creates a QueueManager backed by an in-memory State.
func newTestQueueManager(t *testing.T) (*QueueManager, *State) {
	t.Helper()
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	qm := NewQueueManager(state, t.TempDir())
	return qm, state
}

// createTestTask inserts a minimal task into state and returns its ID.
func createTestTask(t *testing.T, state *State, id string) {
	t.Helper()
	err := state.CreateTask(&Task{
		ID:        id,
		Command:   "test",
		Status:    "running",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// TestQueueManagerInitBuiltinQueues verifies that InitQueues creates the
// expected "default" (serial, protected) and "admin" (concurrent, protected)
// queues.
func TestQueueManagerInitBuiltinQueues(t *testing.T) {
	qm, state := newTestQueueManager(t)
	defer state.Close()

	taskID := "task-init"
	createTestTask(t, state, taskID)

	if err := qm.InitQueues(taskID); err != nil {
		t.Fatalf("InitQueues: %v", err)
	}

	queues, err := qm.ListQueues(taskID)
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}
	if len(queues) != 2 {
		t.Fatalf("expected 2 queues, got %d", len(queues))
	}

	byName := make(map[string]*Queue)
	for _, q := range queues {
		byName[q.Name] = q
	}

	def, ok := byName["default"]
	if !ok {
		t.Fatal("default queue not found")
	}
	if def.Mode != "serial" {
		t.Errorf("default queue mode: got %q, want serial", def.Mode)
	}
	if !def.Protected {
		t.Error("default queue should be protected")
	}
	if def.Status != "active" {
		t.Errorf("default queue status: got %q, want active", def.Status)
	}

	admin, ok := byName["admin"]
	if !ok {
		t.Fatal("admin queue not found")
	}
	if admin.Mode != "concurrent" {
		t.Errorf("admin queue mode: got %q, want concurrent", admin.Mode)
	}
	if !admin.Protected {
		t.Error("admin queue should be protected")
	}
	if admin.Status != "active" {
		t.Errorf("admin queue status: got %q, want active", admin.Status)
	}
}

// TestQueueManagerCreateCustomQueue verifies that a custom queue is created
// with the requested mode and is not protected.
func TestQueueManagerCreateCustomQueue(t *testing.T) {
	qm, state := newTestQueueManager(t)
	defer state.Close()

	taskID := "task-custom"
	createTestTask(t, state, taskID)

	if err := qm.CreateQueue(taskID, "build", "serial"); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	q, _, err := qm.GetQueueStatus(taskID, "build")
	if err != nil {
		t.Fatalf("GetQueueStatus: %v", err)
	}
	if q.Mode != "serial" {
		t.Errorf("mode: got %q, want serial", q.Mode)
	}
	if q.Protected {
		t.Error("custom queue should not be protected")
	}
	if q.Status != "active" {
		t.Errorf("status: got %q, want active", q.Status)
	}

	// Also test concurrent mode
	if err := qm.CreateQueue(taskID, "parallel", "concurrent"); err != nil {
		t.Fatalf("CreateQueue concurrent: %v", err)
	}
	qc, _, err := qm.GetQueueStatus(taskID, "parallel")
	if err != nil {
		t.Fatalf("GetQueueStatus parallel: %v", err)
	}
	if qc.Mode != "concurrent" {
		t.Errorf("concurrent mode: got %q, want concurrent", qc.Mode)
	}
}

// TestQueueManagerQueueCommand verifies that submitting a command returns a
// non-empty command ID and that the command is persisted in pending state.
func TestQueueManagerQueueCommand(t *testing.T) {
	qm, state := newTestQueueManager(t)
	defer state.Close()

	taskID := "task-queue-cmd"
	createTestTask(t, state, taskID)

	// Create a serial queue so no vsock execution is triggered
	// (task has no vsock path, so executeCommand would fail quickly, but we
	// want to test the state side only — so we create a queue and rely on the
	// fact that the goroutine will fail silently when there's no vsock).
	if err := qm.CreateQueue(taskID, "test-q", "serial"); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	cmdID, err := qm.QueueCommand(taskID, "test-q", []string{"echo", "hello"}, nil, true)
	if err != nil {
		t.Fatalf("QueueCommand: %v", err)
	}
	if cmdID == "" {
		t.Fatal("expected non-empty command ID")
	}
	if !strings.HasPrefix(cmdID, "cmd-") {
		t.Errorf("command ID should start with cmd-, got %q", cmdID)
	}

	// Retrieve the command and check initial state
	cmd, err := qm.GetCommandStatus(cmdID)
	if err != nil {
		t.Fatalf("GetCommandStatus: %v", err)
	}
	if cmd.TaskID != taskID {
		t.Errorf("task ID: got %q, want %q", cmd.TaskID, taskID)
	}
	if cmd.QueueName != "test-q" {
		t.Errorf("queue name: got %q, want test-q", cmd.QueueName)
	}
	// Status could be "pending" or "running" depending on goroutine scheduling,
	// but it must not be empty.
	if cmd.Status == "" {
		t.Error("command status should not be empty")
	}
}

// TestQueueManagerDestroyProtectedQueue verifies that built-in (protected)
// queues cannot be destroyed.
func TestQueueManagerDestroyProtectedQueue(t *testing.T) {
	qm, state := newTestQueueManager(t)
	defer state.Close()

	taskID := "task-protect"
	createTestTask(t, state, taskID)

	if err := qm.InitQueues(taskID); err != nil {
		t.Fatalf("InitQueues: %v", err)
	}

	if err := qm.DestroyQueue(taskID, "default"); err == nil {
		t.Error("expected error destroying protected default queue, got nil")
	}
	if err := qm.DestroyQueue(taskID, "admin"); err == nil {
		t.Error("expected error destroying protected admin queue, got nil")
	}

	// Queues should still exist
	queues, err := qm.ListQueues(taskID)
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}
	if len(queues) != 2 {
		t.Errorf("expected 2 queues after failed destroy, got %d", len(queues))
	}
}

// TestQueueManagerDestroyCustomQueue verifies that a user-created queue can be
// destroyed and is removed from state.
func TestQueueManagerDestroyCustomQueue(t *testing.T) {
	qm, state := newTestQueueManager(t)
	defer state.Close()

	taskID := "task-destroy-custom"
	createTestTask(t, state, taskID)

	if err := qm.CreateQueue(taskID, "temp", "serial"); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if err := qm.DestroyQueue(taskID, "temp"); err != nil {
		t.Fatalf("DestroyQueue: %v", err)
	}

	// Queue should no longer exist
	if _, err := state.GetQueue(taskID, "temp"); err == nil {
		t.Error("expected error getting destroyed queue, got nil")
	}
}

// TestQueueManagerFlushQueue verifies that FlushQueue removes only pending
// commands and leaves non-pending ones intact.
func TestQueueManagerFlushQueue(t *testing.T) {
	qm, state := newTestQueueManager(t)
	defer state.Close()

	taskID := "task-flush"
	createTestTask(t, state, taskID)

	if err := qm.CreateQueue(taskID, "flush-q", "serial"); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	// Insert commands directly via state so we control their status
	now := time.Now()
	completedCmd := &Command{
		ID:        "cmd-completed",
		TaskID:    taskID,
		QueueName: "flush-q",
		Command:   []string{"echo", "done"},
		Status:    "completed",
		CreatedAt: now,
	}
	pendingCmd := &Command{
		ID:        "cmd-pending",
		TaskID:    taskID,
		QueueName: "flush-q",
		Command:   []string{"echo", "pending"},
		Status:    "pending",
		CreatedAt: now.Add(time.Millisecond),
	}
	state.CreateCommand(completedCmd)
	state.CreateCommand(pendingCmd)

	if err := qm.FlushQueue(taskID, "flush-q"); err != nil {
		t.Fatalf("FlushQueue: %v", err)
	}

	_, cmds, err := qm.GetQueueStatus(taskID, "flush-q")
	if err != nil {
		t.Fatalf("GetQueueStatus: %v", err)
	}

	for _, c := range cmds {
		if c.Status == "pending" {
			t.Errorf("pending command %s should have been flushed", c.ID)
		}
	}

	found := false
	for _, c := range cmds {
		if c.ID == "cmd-completed" {
			found = true
		}
	}
	if !found {
		t.Error("completed command should not have been flushed")
	}
}

// TestQueueManagerCleanupTask verifies that CleanupTask removes all queues
// and commands for a task.
func TestQueueManagerCleanupTask(t *testing.T) {
	qm, state := newTestQueueManager(t)
	defer state.Close()

	taskID := "task-cleanup"
	createTestTask(t, state, taskID)

	if err := qm.InitQueues(taskID); err != nil {
		t.Fatalf("InitQueues: %v", err)
	}

	// Add a command to the default queue
	now := time.Now()
	cmd := &Command{
		ID:        "cmd-to-clean",
		TaskID:    taskID,
		QueueName: "default",
		Command:   []string{"echo"},
		Status:    "pending",
		CreatedAt: now,
	}
	state.CreateCommand(cmd)

	if err := qm.CleanupTask(taskID); err != nil {
		t.Fatalf("CleanupTask: %v", err)
	}

	queues, err := qm.ListQueues(taskID)
	if err != nil {
		t.Fatalf("ListQueues after cleanup: %v", err)
	}
	if len(queues) != 0 {
		t.Errorf("expected 0 queues after cleanup, got %d", len(queues))
	}

	cmds, err := state.ListCommands(taskID, "default")
	if err != nil {
		t.Fatalf("ListCommands after cleanup: %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("expected 0 commands after cleanup, got %d", len(cmds))
	}
}

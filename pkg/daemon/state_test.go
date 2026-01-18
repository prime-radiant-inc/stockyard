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
		ID:                "task-123",
		Name:              "test-task",
		Repo:              "github.com/test/repo",
		Ref:               "main",
		Command:           "claude-code",
		Status:            "running",
		TailscaleHostname: "stockyard-task-123",
		CreatedAt:         time.Now(),
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
	if got.Name != task.Name {
		t.Errorf("Name mismatch: got %q, want %q", got.Name, task.Name)
	}
	if got.Repo != task.Repo {
		t.Errorf("Repo mismatch: got %q, want %q", got.Repo, task.Repo)
	}
	if got.Ref != task.Ref {
		t.Errorf("Ref mismatch: got %q, want %q", got.Ref, task.Ref)
	}
	if got.Command != task.Command {
		t.Errorf("Command mismatch: got %q, want %q", got.Command, task.Command)
	}
	if got.Status != "running" {
		t.Errorf("Status mismatch: got %q, want %q", got.Status, "running")
	}
	if got.TailscaleHostname != task.TailscaleHostname {
		t.Errorf("TailscaleHostname mismatch: got %q, want %q", got.TailscaleHostname, task.TailscaleHostname)
	}
}

func TestState_GetTask_NotFound(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	_, err = state.GetTask("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task, got nil")
	}
}

func TestState_ListTasks(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	state.CreateTask(&Task{ID: "task-1", Repo: "repo1", Ref: "main", Command: "cmd", Status: "running", CreatedAt: time.Now()})
	state.CreateTask(&Task{ID: "task-2", Repo: "repo2", Ref: "main", Command: "cmd", Status: "stopped", CreatedAt: time.Now()})

	tasks, err := state.ListTasks("")
	if err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}

	running, err := state.ListTasks("running")
	if err != nil {
		t.Fatalf("failed to list running tasks: %v", err)
	}
	if len(running) != 1 {
		t.Errorf("expected 1 running task, got %d", len(running))
	}
	if running[0].ID != "task-1" {
		t.Errorf("expected task-1, got %s", running[0].ID)
	}

	stopped, err := state.ListTasks("stopped")
	if err != nil {
		t.Fatalf("failed to list stopped tasks: %v", err)
	}
	if len(stopped) != 1 {
		t.Errorf("expected 1 stopped task, got %d", len(stopped))
	}
	if stopped[0].ID != "task-2" {
		t.Errorf("expected task-2, got %s", stopped[0].ID)
	}
}

func TestState_UpdateTaskStatus(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	task := &Task{
		ID:        "task-update",
		Repo:      "repo",
		Ref:       "main",
		Command:   "cmd",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	state.CreateTask(task)

	err = state.UpdateTaskStatus("task-update", "stopped")
	if err != nil {
		t.Fatalf("failed to update task status: %v", err)
	}

	got, err := state.GetTask("task-update")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if got.Status != "stopped" {
		t.Errorf("expected status 'stopped', got %q", got.Status)
	}
	if got.StoppedAt == nil {
		t.Error("expected StoppedAt to be set when status is 'stopped'")
	}
}

func TestState_UpdateTaskStatus_NotFound(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	err = state.UpdateTaskStatus("nonexistent", "stopped")
	if err == nil {
		t.Error("expected error for nonexistent task, got nil")
	}
}

func TestState_DeleteTask(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	task := &Task{
		ID:        "task-delete",
		Repo:      "repo",
		Ref:       "main",
		Command:   "cmd",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	state.CreateTask(task)

	err = state.DeleteTask("task-delete")
	if err != nil {
		t.Fatalf("failed to delete task: %v", err)
	}

	_, err = state.GetTask("task-delete")
	if err == nil {
		t.Error("expected error after deleting task, got nil")
	}
}

func TestState_RecordSnapshot(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	err = state.RecordSnapshot("task-123", "pool/dataset@snap1")
	if err != nil {
		t.Fatalf("failed to record snapshot: %v", err)
	}

	// Record another snapshot
	err = state.RecordSnapshot("task-123", "pool/dataset@snap2")
	if err != nil {
		t.Fatalf("failed to record second snapshot: %v", err)
	}
}

func TestState_UpdateTaskVMID(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	task := &Task{
		ID:        "task-vmid",
		Repo:      "repo",
		Ref:       "main",
		Command:   "cmd",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	state.CreateTask(task)

	err = state.UpdateTaskVMID("task-vmid", "vm-12345")
	if err != nil {
		t.Fatalf("failed to update task VMID: %v", err)
	}

	got, err := state.GetTask("task-vmid")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if got.VMID != "vm-12345" {
		t.Errorf("expected VMID 'vm-12345', got %q", got.VMID)
	}
}

func TestState_ListTaskSnapshots(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	// Create a task first
	task := &Task{
		ID:        "test-task-1",
		Name:      "test",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Record some snapshots
	if err := state.RecordSnapshot("test-task-1", "snap-1"); err != nil {
		t.Fatalf("record snapshot 1: %v", err)
	}
	if err := state.RecordSnapshot("test-task-1", "snap-2"); err != nil {
		t.Fatalf("record snapshot 2: %v", err)
	}

	// List snapshots
	snaps, err := state.ListTaskSnapshots("test-task-1")
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}

	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}

	// Should be ordered by created_at DESC
	if len(snaps) >= 2 && snaps[0].Name != "snap-2" {
		t.Errorf("expected snap-2 first (most recent), got %s", snaps[0].Name)
	}
}

func TestDataDir(t *testing.T) {
	dir := DataDir()
	if dir == "" {
		t.Error("DataDir returned empty string")
	}
	// Should contain "stockyard" in the path
	if !contains(dir, "stockyard") {
		t.Errorf("DataDir %q should contain 'stockyard'", dir)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestState_StatusChangeCallback(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	// Track callback invocations
	var called bool
	var receivedTaskID, receivedOld, receivedNew string

	state.SetStatusChangeCallback(func(taskID, oldStatus, newStatus string) {
		called = true
		receivedTaskID = taskID
		receivedOld = oldStatus
		receivedNew = newStatus
	})

	// Create a task
	task := &Task{ID: "test-callback", Status: "pending", Repo: "test", Ref: "main", Command: "cmd", CreatedAt: time.Now()}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Update status
	if err := state.UpdateTaskStatus("test-callback", "running"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}

	if !called {
		t.Error("callback was not called")
	}
	if receivedTaskID != "test-callback" {
		t.Errorf("expected taskID test-callback, got %s", receivedTaskID)
	}
	if receivedOld != "pending" {
		t.Errorf("expected old status pending, got %s", receivedOld)
	}
	if receivedNew != "running" {
		t.Errorf("expected new status running, got %s", receivedNew)
	}
}

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
	if found.CID != 100 {
		t.Errorf("expected CID 100, got %d", found.CID)
	}
}

func TestState_GetTaskByCID_NotFound(t *testing.T) {
	s, err := NewStateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err = s.GetTaskByCID(999)
	if err == nil {
		t.Error("expected error for non-existent CID")
	}
}

func TestState_GetTaskByCID_NotRunning(t *testing.T) {
	s, err := NewStateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create a stopped task with a CID
	task := &Task{
		ID:        "task-stopped",
		Repo:      "github.com/test/repo",
		Ref:       "main",
		Command:   "test",
		Status:    "stopped",
		CID:       150,
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	// Should not find it because it's not running
	_, err = s.GetTaskByCID(150)
	if err == nil {
		t.Error("expected error for stopped task CID")
	}
}

func TestState_UpdateTaskCID(t *testing.T) {
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
		CID:       0,
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateTaskCID("task-123", 200); err != nil {
		t.Fatalf("UpdateTaskCID failed: %v", err)
	}

	found, err := s.GetTaskByCID(200)
	if err != nil {
		t.Fatalf("GetTaskByCID after update failed: %v", err)
	}
	if found.CID != 200 {
		t.Errorf("expected CID 200, got %d", found.CID)
	}
}

func TestState_UpdateTaskCID_NotFound(t *testing.T) {
	s, err := NewStateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	err = s.UpdateTaskCID("nonexistent", 100)
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestState_CID_PreservedInGetTask(t *testing.T) {
	s, err := NewStateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	task := &Task{
		ID:        "task-cid-get",
		Repo:      "github.com/test/repo",
		Ref:       "main",
		Command:   "test",
		Status:    "running",
		CID:       300,
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTask("task-cid-get")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if got.CID != 300 {
		t.Errorf("expected CID 300, got %d", got.CID)
	}
}

func TestState_CID_PreservedInListTasks(t *testing.T) {
	s, err := NewStateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	task := &Task{
		ID:        "task-cid-list",
		Repo:      "github.com/test/repo",
		Ref:       "main",
		Command:   "test",
		Status:    "running",
		CID:       400,
		CreatedAt: time.Now(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.ListTasks("running")
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].CID != 400 {
		t.Errorf("expected CID 400, got %d", tasks[0].CID)
	}
}

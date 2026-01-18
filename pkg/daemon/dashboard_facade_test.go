package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/dashboard"
)

func TestDashboardFacade_ImplementsInterface(t *testing.T) {
	// Compile-time check that DashboardFacade implements dashboard.RealDaemon
	var _ dashboard.RealDaemon = (*DashboardFacade)(nil)
}

func TestDashboardFacade_ListTasks(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	// Create test tasks
	task1 := &Task{
		ID:        "task-1",
		Name:      "test-vm",
		Repo:      "github.com/test/repo",
		Ref:       "main",
		Command:   "claude",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	task2 := &Task{
		ID:        "task-2",
		Repo:      "github.com/test/other",
		Ref:       "develop",
		Command:   "bash",
		Status:    "stopped",
		CreatedAt: time.Now(),
	}

	if err := state.CreateTask(task1); err != nil {
		t.Fatalf("failed to create task1: %v", err)
	}
	if err := state.CreateTask(task2); err != nil {
		t.Fatalf("failed to create task2: %v", err)
	}

	facade := NewDashboardFacade(state, nil)

	// List all tasks
	tasks, err := facade.ListTasks(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}

	// List only running tasks
	tasks, err = facade.ListTasks(context.Background(), "running")
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 running task, got %d", len(tasks))
	}
	if tasks[0].ID != "task-1" {
		t.Errorf("expected task-1, got %s", tasks[0].ID)
	}
}

func TestDashboardFacade_GetTask(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	task := &Task{
		ID:                "task-123",
		Name:              "my-vm",
		Repo:              "github.com/test/repo",
		Ref:               "feature",
		Command:           "claude",
		Status:            "running",
		TailscaleHostname: "stockyard-task-123",
		CreatedAt:         time.Now(),
	}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	facade := NewDashboardFacade(state, nil)

	// Test found case
	result, err := facade.GetTask(context.Background(), "task-123")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected task, got nil")
	}
	if result.ID != "task-123" {
		t.Errorf("expected task-123, got %s", result.ID)
	}
	if result.TailscaleHostname != "stockyard-task-123" {
		t.Errorf("expected stockyard-task-123, got %s", result.TailscaleHostname)
	}

	// Test not found case - should return nil, nil (not an error)
	result, err = facade.GetTask(context.Background(), "nonexistent")
	if err == nil && result != nil {
		t.Error("expected nil result for nonexistent task")
	}
}

func TestDashboardFacade_ListTaskSnapshots(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	// Create task and snapshots
	task := &Task{
		ID:        "task-1",
		Repo:      "github.com/test/repo",
		Ref:       "main",
		Command:   "claude",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	state.RecordSnapshot("task-1", "task-1@snap1")
	state.RecordSnapshot("task-1", "task-1@snap2")

	facade := NewDashboardFacade(state, nil)

	snaps, err := facade.ListTaskSnapshots(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ListTaskSnapshots failed: %v", err)
	}
	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}
}

func TestDashboardFacade_CreateSnapshot(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	task := &Task{
		ID:        "task-1",
		Repo:      "github.com/test/repo",
		Ref:       "main",
		Command:   "claude",
		Status:    "running",
		CreatedAt: time.Now(),
	}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	facade := NewDashboardFacade(state, nil)

	snapName, err := facade.CreateSnapshot(context.Background(), "task-1", "my-label")
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}
	if snapName != "task-1@my-label" {
		t.Errorf("expected task-1@my-label, got %s", snapName)
	}

	// Verify it was recorded
	snaps, _ := state.ListTaskSnapshots("task-1")
	if len(snaps) != 1 {
		t.Errorf("expected 1 snapshot recorded, got %d", len(snaps))
	}
}

func TestDashboardFacade_RestoreSnapshot_NotImplemented(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	facade := NewDashboardFacade(state, nil)

	err = facade.RestoreSnapshot(context.Background(), "task-1", "task-1@snap1")
	if err == nil {
		t.Error("expected error for unimplemented restore")
	}
}

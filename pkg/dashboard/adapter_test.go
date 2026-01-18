package dashboard

import (
	"context"
	"testing"
	"time"
)

func TestDaemonAdapter_ImplementsInterface(t *testing.T) {
	// This is a compile-time check
	var _ DaemonAPI = (*DaemonAdapter)(nil)
}

// MockRealDaemon implements the interface we need from the actual daemon
type MockRealDaemon struct {
	tasks     []*DaemonTask
	snapshots map[string][]DaemonSnapshot
	stopped   []string
	destroyed []string
	created   []string
	restored  []string
}

func (m *MockRealDaemon) ListTasks(ctx context.Context, status string) ([]*DaemonTask, error) {
	if status == "" {
		return m.tasks, nil
	}
	var filtered []*DaemonTask
	for _, t := range m.tasks {
		if t.Status == status {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

func (m *MockRealDaemon) GetTask(ctx context.Context, id string) (*DaemonTask, error) {
	for _, t := range m.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

func (m *MockRealDaemon) StopTask(ctx context.Context, id string) error {
	m.stopped = append(m.stopped, id)
	return nil
}

func (m *MockRealDaemon) DestroyTask(ctx context.Context, id string) error {
	m.destroyed = append(m.destroyed, id)
	return nil
}

func (m *MockRealDaemon) ListTaskSnapshots(ctx context.Context, taskID string) ([]DaemonSnapshot, error) {
	return m.snapshots[taskID], nil
}

func (m *MockRealDaemon) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
	m.created = append(m.created, taskID+":"+label)
	return "snap-" + taskID, nil
}

func (m *MockRealDaemon) RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error {
	m.restored = append(m.restored, taskID+":"+snapshotName)
	return nil
}

func TestDaemonAdapter_ListTasks(t *testing.T) {
	now := time.Now()
	mock := &MockRealDaemon{
		tasks: []*DaemonTask{
			{
				ID:                "task-1",
				Name:              "test-vm",
				Repo:              "github.com/test/repo",
				Ref:               "main",
				Status:            "running",
				Owner:             "jesse@example.com",
				TailscaleHostname: "stockyard-task-1",
				CreatedAt:         now,
			},
			{
				ID:     "task-2",
				Repo:   "github.com/test/other",
				Status: "stopped",
				Owner:  "other@example.com",
			},
		},
	}

	adapter := NewDaemonAdapter(mock)
	tasks, err := adapter.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}

	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}

	if tasks[0].ID != "task-1" {
		t.Errorf("expected task-1, got %s", tasks[0].ID)
	}
	if tasks[0].RepoURL != "github.com/test/repo" {
		t.Errorf("expected github.com/test/repo, got %s", tasks[0].RepoURL)
	}
	if tasks[0].TailscaleHost != "stockyard-task-1" {
		t.Errorf("expected stockyard-task-1, got %s", tasks[0].TailscaleHost)
	}
	if tasks[0].Owner != "jesse@example.com" {
		t.Errorf("expected owner jesse@example.com, got %s", tasks[0].Owner)
	}
	if tasks[1].Owner != "other@example.com" {
		t.Errorf("expected owner other@example.com, got %s", tasks[1].Owner)
	}
}

func TestDaemonAdapter_GetTask(t *testing.T) {
	now := time.Now()
	mock := &MockRealDaemon{
		tasks: []*DaemonTask{
			{
				ID:        "task-1",
				Name:      "test-vm",
				Repo:      "github.com/test/repo",
				Ref:       "main",
				Status:    "running",
				Owner:     "jesse@example.com",
				CreatedAt: now,
			},
		},
	}

	adapter := NewDaemonAdapter(mock)

	// Test found case
	task, err := adapter.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task == nil {
		t.Fatal("expected task, got nil")
	}
	if task.ID != "task-1" {
		t.Errorf("expected task-1, got %s", task.ID)
	}
	if task.Owner != "jesse@example.com" {
		t.Errorf("expected owner jesse@example.com, got %s", task.Owner)
	}

	// Test not found case
	task, err = adapter.GetTask(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task != nil {
		t.Error("expected nil for nonexistent task")
	}
}

func TestDaemonAdapter_StopTask(t *testing.T) {
	mock := &MockRealDaemon{}
	adapter := NewDaemonAdapter(mock)

	err := adapter.StopTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("StopTask failed: %v", err)
	}

	if len(mock.stopped) != 1 || mock.stopped[0] != "task-1" {
		t.Errorf("expected task-1 to be stopped, got %v", mock.stopped)
	}
}

func TestDaemonAdapter_DestroyTask(t *testing.T) {
	mock := &MockRealDaemon{}
	adapter := NewDaemonAdapter(mock)

	err := adapter.DestroyTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("DestroyTask failed: %v", err)
	}

	if len(mock.destroyed) != 1 || mock.destroyed[0] != "task-1" {
		t.Errorf("expected task-1 to be destroyed, got %v", mock.destroyed)
	}
}

func TestDaemonAdapter_ListSnapshots(t *testing.T) {
	now := time.Now()
	mock := &MockRealDaemon{
		snapshots: map[string][]DaemonSnapshot{
			"task-1": {
				{Name: "snap-1", CreatedAt: now},
				{Name: "snap-2", CreatedAt: now.Add(-time.Hour)},
			},
		},
	}

	adapter := NewDaemonAdapter(mock)
	snapshots, err := adapter.ListSnapshots(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}

	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].Name != "snap-1" {
		t.Errorf("expected snap-1, got %s", snapshots[0].Name)
	}
	if snapshots[0].TaskID != "task-1" {
		t.Errorf("expected TaskID task-1, got %s", snapshots[0].TaskID)
	}
}

func TestDaemonAdapter_CreateSnapshot(t *testing.T) {
	mock := &MockRealDaemon{}
	adapter := NewDaemonAdapter(mock)

	snap, err := adapter.CreateSnapshot(context.Background(), "task-1", "test-label")
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	if snap == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snap.Name != "snap-task-1" {
		t.Errorf("expected snap-task-1, got %s", snap.Name)
	}
	if snap.TaskID != "task-1" {
		t.Errorf("expected TaskID task-1, got %s", snap.TaskID)
	}
	if snap.Label != "test-label" {
		t.Errorf("expected label test-label, got %s", snap.Label)
	}

	if len(mock.created) != 1 || mock.created[0] != "task-1:test-label" {
		t.Errorf("expected task-1:test-label to be created, got %v", mock.created)
	}
}

func TestDaemonAdapter_RestoreSnapshot(t *testing.T) {
	mock := &MockRealDaemon{
		tasks:     []*DaemonTask{{ID: "task-1", Status: "stopped"}},
		snapshots: make(map[string][]DaemonSnapshot),
	}

	adapter := NewDaemonAdapter(mock)
	err := adapter.RestoreSnapshot(context.Background(), "task-1", "task-1@my-label")
	if err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}

	if len(mock.restored) != 1 || mock.restored[0] != "task-1:task-1@my-label" {
		t.Errorf("expected RestoreSnapshot called with task-1:task-1@my-label, got %v", mock.restored)
	}
}

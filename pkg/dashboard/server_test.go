package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServer_HealthEndpoint(t *testing.T) {
	srv := NewServer(nil)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %s", w.Body.String())
	}
}

func TestServer_WithMockDaemon(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{
			{ID: "task-1", Name: "test", Status: "running"},
		},
	}
	srv := NewServer(mock)

	if srv.daemon == nil {
		t.Error("expected daemon to be set")
	}
}

type MockDaemon struct {
	tasks []Task
}

func (m *MockDaemon) ListTasks(ctx context.Context) ([]Task, error) {
	return m.tasks, nil
}

func (m *MockDaemon) GetTask(ctx context.Context, id string) (*Task, error) {
	for i := range m.tasks {
		if m.tasks[i].ID == id {
			task := m.tasks[i]
			return &task, nil
		}
	}
	return nil, nil
}

func (m *MockDaemon) StopTask(ctx context.Context, id string) error {
	return nil
}

func (m *MockDaemon) DestroyTask(ctx context.Context, id string) error {
	return nil
}

func (m *MockDaemon) ListSnapshots(ctx context.Context, taskID string) ([]Snapshot, error) {
	return nil, nil
}

func (m *MockDaemon) CreateSnapshot(ctx context.Context, taskID, label string) (*Snapshot, error) {
	return &Snapshot{Name: "snap-1", TaskID: taskID, Label: label}, nil
}

func TestServer_FleetPage(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{
			{ID: "task-1", Name: "test-vm", Status: "running", RepoURL: "github.com/test/repo"},
			{ID: "task-2", Name: "test-vm-2", Status: "stopped", RepoURL: "github.com/test/repo"},
		},
	}
	srv := NewServer(mock)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "task-1") {
		t.Error("expected task-1 in output")
	}
	if !strings.Contains(body, "running") {
		t.Error("expected running status in output")
	}
}

func TestServer_FleetPage_NotFound(t *testing.T) {
	srv := NewServer(nil)

	req := httptest.NewRequest("GET", "/unknown-path", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestServer_VMDetailPage(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{
			{ID: "task-123", Name: "test-vm", Status: "running", RepoURL: "github.com/test/repo", TailscaleHost: "vm-123.tail.net"},
		},
	}
	srv := NewServer(mock)

	req := httptest.NewRequest("GET", "/vm/task-123", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "task-123") {
		t.Error("expected task ID in output")
	}
	if !strings.Contains(body, "running") {
		t.Error("expected status in output")
	}
}

func TestServer_VMDetailPage_NotFound(t *testing.T) {
	mock := &MockDaemon{tasks: []Task{}}
	srv := NewServer(mock)

	req := httptest.NewRequest("GET", "/vm/nonexistent", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

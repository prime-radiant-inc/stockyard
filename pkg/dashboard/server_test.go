package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	for _, t := range m.tasks {
		if t.ID == id {
			return &t, nil
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

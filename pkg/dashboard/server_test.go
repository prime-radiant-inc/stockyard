package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServer_HealthEndpoint(t *testing.T) {
	srv := NewServer(nil, "")

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
	srv := NewServer(mock, "")

	if srv.daemon == nil {
		t.Error("expected daemon to be set")
	}
}

type MockDaemon struct {
	tasks        []Task
	snapshots    []Snapshot
	stoppedIDs   []string
	destroyedIDs []string
	err          error // For simulating errors
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

func (m *MockDaemon) CreateTask(ctx context.Context, req CreateTaskRequest) (*Task, error) {
	if m.err != nil {
		return nil, m.err
	}
	task := Task{
		ID:      "new-task-id",
		Name:    req.Name,
		RepoURL: req.Repo,
		GitRef:  req.Ref,
		Status:  "running",
	}
	m.tasks = append(m.tasks, task)
	return &task, nil
}

func (m *MockDaemon) StopTask(ctx context.Context, id string) error {
	m.stoppedIDs = append(m.stoppedIDs, id)
	return m.err
}

func (m *MockDaemon) DestroyTask(ctx context.Context, id string) error {
	m.destroyedIDs = append(m.destroyedIDs, id)
	return m.err
}

func (m *MockDaemon) ListSnapshots(ctx context.Context, taskID string) ([]Snapshot, error) {
	return nil, nil
}

func (m *MockDaemon) CreateSnapshot(ctx context.Context, taskID, label string) (*Snapshot, error) {
	return &Snapshot{Name: "snap-1", TaskID: taskID, Label: label}, nil
}

func (m *MockDaemon) RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error {
	return m.err
}

func TestServer_FleetPage(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{
			{ID: "task-1", Name: "test-vm", Status: "running", RepoURL: "github.com/test/repo"},
			{ID: "task-2", Name: "test-vm-2", Status: "stopped", RepoURL: "github.com/test/repo"},
		},
	}
	srv := NewServer(mock, "")

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
	srv := NewServer(nil, "")

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
	srv := NewServer(mock, "")

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
	srv := NewServer(mock, "")

	req := httptest.NewRequest("GET", "/vm/nonexistent", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestServer_VMPreview(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{
			{ID: "task-123", Name: "test-vm", Status: "running", RepoURL: "github.com/test/repo", TailscaleHost: "vm-123.tail.net"},
		},
	}
	srv := NewServer(mock, "")

	req := httptest.NewRequest("GET", "/preview/vm/task-123", nil)
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

func TestServer_VMPreview_NotFound(t *testing.T) {
	mock := &MockDaemon{tasks: []Task{}}
	srv := NewServer(mock, "")

	req := httptest.NewRequest("GET", "/preview/vm/nonexistent", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestServer_StopVM(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{{ID: "task-1", Status: "running"}},
	}
	srv := NewServer(mock, "")

	req := httptest.NewRequest("POST", "/api/vm/task-1/stop", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if len(mock.stoppedIDs) != 1 || mock.stoppedIDs[0] != "task-1" {
		t.Errorf("expected StopTask called with task-1, got %v", mock.stoppedIDs)
	}
}

func TestServer_DestroyVM(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{{ID: "task-1", Status: "running"}},
	}
	srv := NewServer(mock, "")

	req := httptest.NewRequest("DELETE", "/api/vm/task-1", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if len(mock.destroyedIDs) != 1 || mock.destroyedIDs[0] != "task-1" {
		t.Errorf("expected DestroyTask called with task-1, got %v", mock.destroyedIDs)
	}
}

func TestServer_StopVM_Error(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{{ID: "task-1", Status: "running"}},
		err:   errors.New("stop failed"),
	}
	srv := NewServer(mock, "")

	req := httptest.NewRequest("POST", "/api/vm/task-1/stop", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestServer_DestroyVM_Error(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{{ID: "task-1", Status: "running"}},
		err:   errors.New("destroy failed"),
	}
	srv := NewServer(mock, "")

	req := httptest.NewRequest("DELETE", "/api/vm/task-1", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestServer_HasWebSocketEndpoint(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock, "")

	// Just verify the route exists - actual WS testing done in websocket_test.go
	req := httptest.NewRequest("GET", "/ws", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	// Should get "Bad Request" because we're not upgrading
	if w.Code == http.StatusNotFound {
		t.Error("expected /ws endpoint to exist")
	}
}

func TestServer_LogSearchAPI(t *testing.T) {
	srv := NewServer(nil, "")

	// Add some log lines
	srv.LogHistory().AddLine("task-1", "stdout", "starting server")
	srv.LogHistory().AddLine("task-1", "stderr", "error: connection failed")
	srv.LogHistory().AddLine("task-1", "stdout", "retrying connection")

	// Test search with query
	req := httptest.NewRequest("GET", "/api/vm-logs/task-1?q=connection", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "connection failed") {
		t.Error("expected 'connection failed' in results")
	}
	if !strings.Contains(body, "retrying connection") {
		t.Error("expected 'retrying connection' in results")
	}
}

func TestServer_LogSearchAPI_FilterByStream(t *testing.T) {
	srv := NewServer(nil, "")

	srv.LogHistory().AddLine("task-1", "stdout", "normal output")
	srv.LogHistory().AddLine("task-1", "stderr", "error output")

	req := httptest.NewRequest("GET", "/api/vm-logs/task-1?stream=stderr", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "error output") {
		t.Error("expected 'error output' in results")
	}
	if strings.Contains(body, "normal output") {
		t.Error("should not contain 'normal output' when filtering by stderr")
	}
}

func TestServer_LogSearchAPI_MissingTaskID(t *testing.T) {
	srv := NewServer(nil, "")

	req := httptest.NewRequest("GET", "/api/vm-logs/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestServer_CreateVM(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock, "")

	body := `{"repo": "github.com/test/repo", "ref": "main", "cpus": 2, "memory_mb": 4096}`
	req := httptest.NewRequest(http.MethodPost, "/api/vm/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	respBody := w.Body.String()
	if !strings.Contains(respBody, `"id"`) {
		t.Error("expected id in response")
	}
	if !strings.Contains(respBody, `"status"`) {
		t.Error("expected status in response")
	}
}

func TestServer_CreateVM_MissingRepo(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock, "")

	body := `{"ref": "main"}`
	req := httptest.NewRequest(http.MethodPost, "/api/vm/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_CreateVM_InvalidJSON(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock, "")

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/api/vm/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_CreateVM_MethodNotAllowed(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock, "")

	req := httptest.NewRequest(http.MethodGet, "/api/vm/create", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_CreateVM_DaemonError(t *testing.T) {
	mock := &MockDaemon{
		err: errors.New("daemon error"),
	}
	srv := NewServer(mock, "")

	body := `{"repo": "github.com/test/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/vm/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_CreateVM_Defaults(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock, "")

	// Send minimal request - should use defaults for ref, cpus, memory
	body := `{"repo": "github.com/test/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/vm/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify a task was created
	if len(mock.tasks) != 1 {
		t.Errorf("expected 1 task created, got %d", len(mock.tasks))
	}
}

func TestServer_CreateVM_NoDaemon(t *testing.T) {
	srv := NewServer(nil, "") // nil daemon

	body := `{"repo": "github.com/test/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/vm/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_CreateVM_WithEnv(t *testing.T) {
	mock := &MockDaemon{}
	srv := NewServer(mock, "")

	body := `{"repo": "github.com/test/repo", "env": {"KEY1": "value1", "KEY2": "value2"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/vm/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify task was created (response has id)
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["id"] == nil {
		t.Error("expected id in response")
	}
}

func TestServer_FleetPage_WithAdapter(t *testing.T) {
	// Use MockRealDaemon from adapter_test.go to test full integration flow:
	// MockRealDaemon -> DaemonAdapter -> Server -> HTML output
	mock := &MockRealDaemon{
		tasks: []*DaemonTask{
			{
				ID:                "task-1",
				Name:              "test-vm",
				Repo:              "github.com/test/repo",
				Ref:               "main",
				Status:            "running",
				TailscaleHostname: "stockyard-task-1",
				CreatedAt:         time.Now(),
			},
		},
	}

	adapter := NewDaemonAdapter(mock)
	srv := NewServer(adapter, "")

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

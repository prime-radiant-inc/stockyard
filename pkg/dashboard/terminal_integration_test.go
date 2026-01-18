package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// mockDaemonForTerminal implements DaemonAPI for terminal testing.
type mockDaemonForTerminal struct {
	task *Task
	cid  uint32
}

func (m *mockDaemonForTerminal) ListTasks(ctx context.Context) ([]Task, error) {
	return nil, nil
}
func (m *mockDaemonForTerminal) GetTask(ctx context.Context, id string) (*Task, error) {
	if m.task != nil && m.task.ID == id {
		return m.task, nil
	}
	return nil, nil
}
func (m *mockDaemonForTerminal) CreateTask(ctx context.Context, req CreateTaskRequest) (*Task, error) {
	return nil, nil
}
func (m *mockDaemonForTerminal) StopTask(ctx context.Context, id string) error    { return nil }
func (m *mockDaemonForTerminal) RestartTask(ctx context.Context, id string) error { return nil }
func (m *mockDaemonForTerminal) DestroyTask(ctx context.Context, id string) error { return nil }
func (m *mockDaemonForTerminal) ListSnapshots(ctx context.Context, taskID string) ([]Snapshot, error) {
	return nil, nil
}
func (m *mockDaemonForTerminal) CreateSnapshot(ctx context.Context, taskID, label string) (*Snapshot, error) {
	return nil, nil
}
func (m *mockDaemonForTerminal) RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error {
	return nil
}
func (m *mockDaemonForTerminal) GetVMIP(ctx context.Context, taskID string) (string, error) {
	return "", nil
}
func (m *mockDaemonForTerminal) GetVMCID(ctx context.Context, taskID string) (uint32, error) {
	return m.cid, nil
}
func (m *mockDaemonForTerminal) GetVsockPath(ctx context.Context, taskID string) (string, error) {
	return "", nil
}

func TestTerminalHandler_Integration_CID0(t *testing.T) {
	// This test validates the message flow without actual vsock.
	// We can't easily mock vsock.Dial, so this tests the HTTP/WebSocket layer.

	daemon := &mockDaemonForTerminal{
		task: &Task{ID: "task-123", Name: "test", Status: "running"},
		cid:  0, // Will cause createVsockSession to fail
	}

	manager := NewTerminalManager()
	handler := NewTerminalHandler(manager, daemon, "mooby")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/terminal/task-123"

	// Connect WebSocket
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		// If we got an HTTP error before upgrade, that's expected for CID 0
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			// Expected - VM not available error
			return
		}
		t.Fatalf("unexpected dial error: %v", err)
	}
	defer conn.Close()

	// If we somehow got a connection with CID 0, read the error message
	_, msg, err := conn.ReadMessage()
	if err == nil && strings.Contains(string(msg), "Error") {
		// Expected - error message sent via WebSocket
		return
	}

	t.Error("expected connection to fail or receive error for CID 0")
}

func TestTerminalHandler_Integration_TaskNotFound(t *testing.T) {
	daemon := &mockDaemonForTerminal{
		task: nil, // No task
		cid:  0,
	}

	manager := NewTerminalManager()
	handler := NewTerminalHandler(manager, daemon, "mooby")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/terminal/nonexistent"

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected connection to fail for nonexistent task")
		return
	}

	if resp != nil && resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

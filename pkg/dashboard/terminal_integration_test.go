package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// mockDaemonForTerminal implements DaemonAPI for terminal testing.
type mockDaemonForTerminal struct {
	task     *Task
	cid      uint32
	vsockErr error // if non-nil, GetVsockPath returns this error
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
	if m.vsockErr != nil {
		return "", m.vsockErr
	}
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
	handler := NewTerminalHandler(manager, daemon, "mooby", "")

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

func TestTerminalHandler_AppleContainerBranch_NoVsock503Avoided(t *testing.T) {
	// An apple-container task must route to serveContainerExec *before* any
	// vsock logic is reached. To make this a genuine regression guard we
	// configure GetVsockPath to return an error: without the apple-container
	// branch ServeHTTP would call GetVsockPath, get the error, and respond 503
	// before the WebSocket upgrade — causing Dial to fail with 503. With the
	// branch the handler never reaches GetVsockPath, so the Dial either
	// succeeds (darwin) or gets 503 from the stub (non-darwin) — but NOT the
	// vsock-error 503. The test distinguishes these two 503 sources by
	// checking the response body.
	daemon := &mockDaemonForTerminal{
		task: &Task{
			ID:      "abc12345",
			Name:    "test",
			Status:  "running",
			Backend: "apple-container",
			VMID:    "abc12345",
		},
		vsockErr: fmt.Errorf("no vsock for apple-container"),
	}
	mgr := NewTerminalManager()
	h := NewTerminalHandler(mgr, daemon, "mooby", "")

	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal/abc12345"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if resp == nil {
			t.Fatalf("unexpected dial failure with no response: %v", err)
		}
		if resp.StatusCode == http.StatusServiceUnavailable {
			// Read the body to distinguish the two 503 sources.
			body := new(strings.Builder)
			if resp.Body != nil {
				buf := make([]byte, 512)
				n, _ := resp.Body.Read(buf)
				body.Write(buf[:n])
				resp.Body.Close()
			}
			if strings.Contains(body.String(), "VM not available") {
				// The vsock-error path fired — the apple-container branch is MISSING.
				t.Fatalf("got vsock-error 503 (%q): apple-container branch not taken; branch may be absent", body.String())
			}
			// 503 from the non-darwin stub — acceptable on non-darwin builds.
			return
		}
		t.Fatalf("unexpected dial failure (wrong branch?): status=%d err=%v", resp.StatusCode, err)
	}
	// darwin: WebSocket upgrade succeeded — the branch routed to serveContainerExec.
	if conn != nil {
		conn.Close()
	}
}

func TestTerminalHandler_Integration_TaskNotFound(t *testing.T) {
	daemon := &mockDaemonForTerminal{
		task: nil, // No task
		cid:  0,
	}

	manager := NewTerminalManager()
	handler := NewTerminalHandler(manager, daemon, "mooby", "")

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

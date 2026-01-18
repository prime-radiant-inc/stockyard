package dashboard

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/obra/stockyard/pkg/shell"
)

func TestTerminalHandler_Creation(t *testing.T) {
	tm := NewTerminalManager()
	handler := NewTerminalHandler(tm, nil, "vscode")

	if handler == nil {
		t.Fatal("expected handler to be created")
	}
	if handler.defaultUser != "vscode" {
		t.Errorf("expected vscode, got %s", handler.defaultUser)
	}
}

func TestExtractTaskID(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/ws/terminal/task-123", "task-123"},
		{"/ws/terminal/abc-def-ghi", "abc-def-ghi"},
		{"/ws/terminal/", ""},
		{"/ws/terminal", ""},
		{"/ws/", ""},
		{"/ws", ""},
		{"/other/path", ""},
		{"", ""},
		{"/ws/terminal/task-123/extra", "task-123"},
	}

	for _, tt := range tests {
		result := extractTaskID(tt.path)
		if result != tt.expected {
			t.Errorf("extractTaskID(%q) = %q, expected %q", tt.path, result, tt.expected)
		}
	}
}

func TestTerminalHandler_MissingTaskID(t *testing.T) {
	tm := NewTerminalManager()
	handler := NewTerminalHandler(tm, nil, "vscode")

	req := httptest.NewRequest("GET", "/ws/terminal/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestTerminalHandler_InvalidPath(t *testing.T) {
	tm := NewTerminalManager()
	handler := NewTerminalHandler(tm, nil, "vscode")

	req := httptest.NewRequest("GET", "/invalid/path", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestTerminalHandler_NoDaemon(t *testing.T) {
	tm := NewTerminalManager()
	handler := NewTerminalHandler(tm, nil, "vscode")

	req := httptest.NewRequest("GET", "/ws/terminal/task-123", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestTerminalHandler_createVsockSession(t *testing.T) {
	// This test verifies the session is created with correct fields.
	// Actual vsock connection requires a running VM, so we test the error path.

	handler := &TerminalHandler{
		defaultUser: "mooby",
	}

	// Empty vsock path is invalid
	_, err := handler.createVsockSession("", "testuser", 80, 24)
	if err == nil {
		t.Error("expected error for empty vsock path")
	}

	// Non-existent vsock path should fail
	_, err = handler.createVsockSession("/nonexistent/vsock.sock", "testuser", 80, 24)
	if err == nil {
		t.Error("expected error for non-existent vsock path")
	}
}

func TestTerminalHandler_handleVsockSession_DataFlow(t *testing.T) {
	// Test that data flows from vsock to WebSocket
	handler := &TerminalHandler{}

	// Create a mock vsock connection using net.Pipe
	vmSide, hostSide := net.Pipe()
	defer vmSide.Close()
	defer hostSide.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: hostSide,
	}

	// Set up a WebSocket test server that calls handleVsockSession
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		handler.handleVsockSession(conn, session)
	}))
	defer server.Close()

	// Connect WebSocket client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer wsConn.Close()

	// Give the handler goroutines time to start
	time.Sleep(50 * time.Millisecond)

	// Simulate VM sending data via vsock
	testData := "Hello from VM"
	go func() {
		shell.WriteMessage(vmSide, shell.MsgData, []byte(testData))
	}()

	// Read from WebSocket and verify
	wsConn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var output TerminalOutputMessage
	if err := json.Unmarshal(msg, &output); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if output.Type != "terminal_output" {
		t.Errorf("expected type terminal_output, got %s", output.Type)
	}
	if output.Data != testData {
		t.Errorf("expected data %q, got %q", testData, output.Data)
	}
}

func TestTerminalHandler_handleVsockSession_InputFlow(t *testing.T) {
	// Test that input flows from WebSocket to vsock
	handler := &TerminalHandler{}

	// Create a mock vsock connection using net.Pipe
	vmSide, hostSide := net.Pipe()
	defer vmSide.Close()
	defer hostSide.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: hostSide,
	}

	// Set up a WebSocket test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		handler.handleVsockSession(conn, session)
	}))
	defer server.Close()

	// Connect WebSocket client
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer wsConn.Close()

	// Give the handler goroutines time to start
	time.Sleep(50 * time.Millisecond)

	// Read from vsock in background
	vmReceived := make(chan string, 1)
	go func() {
		msgType, payload, err := shell.ReadMessage(vmSide)
		if err != nil {
			vmReceived <- "error: " + err.Error()
			return
		}
		if msgType != shell.MsgData {
			vmReceived <- "wrong type"
			return
		}
		vmReceived <- string(payload)
	}()

	// Send input via WebSocket
	inputMsg := TerminalInputMessage{
		Type: "terminal_input",
		Data: "ls -la\n",
	}
	if err := wsConn.WriteJSON(inputMsg); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Verify VM received the input
	select {
	case received := <-vmReceived:
		if received != "ls -la\n" {
			t.Errorf("expected 'ls -la\\n', got %q", received)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for VM to receive input")
	}
}

func TestTerminalHandler_handleVsockSession_ExitMessage(t *testing.T) {
	// Test that exit messages are properly handled
	handler := &TerminalHandler{}

	vmSide, hostSide := net.Pipe()
	defer vmSide.Close()
	defer hostSide.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: hostSide,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler.handleVsockSession(conn, session)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer wsConn.Close()

	time.Sleep(50 * time.Millisecond)

	// Simulate VM sending exit message
	exitMsg := shell.ExitMessage{Code: 0}
	payload, _ := exitMsg.Marshal()
	go func() {
		shell.WriteMessage(vmSide, shell.MsgExit, payload)
	}()

	// Read exit notification from WebSocket
	wsConn.SetReadDeadline(time.Now().Add(time.Second))
	_, msg, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var output TerminalOutputMessage
	if err := json.Unmarshal(msg, &output); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !strings.Contains(output.Data, "Session ended") {
		t.Errorf("expected exit message, got %q", output.Data)
	}
}

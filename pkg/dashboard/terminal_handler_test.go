package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
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

	// CID 0 is invalid and won't connect
	_, err := handler.createVsockSession(0, "testuser", 80, 24)
	if err == nil {
		t.Error("expected error for CID 0")
	}
}

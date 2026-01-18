package dashboard

import (
	"testing"
)

func TestTerminalSession_Fields(t *testing.T) {
	session := &TerminalSession{
		ID:       "session-123",
		TaskID:   "task-456",
		Hostname: "stockyard-task-456",
		User:     "vscode",
	}

	if session.ID != "session-123" {
		t.Errorf("expected session-123, got %s", session.ID)
	}
	if session.TaskID != "task-456" {
		t.Errorf("expected task-456, got %s", session.TaskID)
	}
}

func TestTerminalInputMessage(t *testing.T) {
	msg := TerminalInputMessage{
		Type: "terminal_input",
		Data: "ls -la\n",
	}

	if msg.Type != "terminal_input" {
		t.Errorf("expected terminal_input, got %s", msg.Type)
	}
	if msg.Data != "ls -la\n" {
		t.Errorf("expected ls -la\\n, got %s", msg.Data)
	}
}

func TestTerminalOutputMessage(t *testing.T) {
	msg := TerminalOutputMessage{
		Type: "terminal_output",
		Data: "total 42\n",
	}

	if msg.Type != "terminal_output" {
		t.Errorf("expected terminal_output, got %s", msg.Type)
	}
}

func TestTerminalResizeMessage(t *testing.T) {
	msg := TerminalResizeMessage{
		Type: "terminal_resize",
		Cols: 120,
		Rows: 40,
	}

	if msg.Cols != 120 || msg.Rows != 40 {
		t.Errorf("expected 120x40, got %dx%d", msg.Cols, msg.Rows)
	}
}

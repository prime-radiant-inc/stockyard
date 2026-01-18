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

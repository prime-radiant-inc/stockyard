package dashboard

import (
	"testing"
)

func TestVsockSession_Fields(t *testing.T) {
	session := &VsockSession{
		ID:     "session-123",
		TaskID: "task-456",
		CID:    100,
		User:   "vscode",
	}

	if session.ID != "session-123" {
		t.Errorf("expected session-123, got %s", session.ID)
	}
	if session.TaskID != "task-456" {
		t.Errorf("expected task-456, got %s", session.TaskID)
	}
	if session.CID != 100 {
		t.Errorf("expected CID 100, got %d", session.CID)
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

func TestTerminalManager_CreateSession(t *testing.T) {
	tm := NewTerminalManager()

	session := &VsockSession{
		ID:     "test-session",
		TaskID: "task-123",
		CID:    100,
		User:   "vscode",
	}

	tm.AddSession(session)

	found := tm.GetSession("test-session")
	if found == nil {
		t.Fatal("expected to find session")
	}
	if found.TaskID != "task-123" {
		t.Errorf("expected task-123, got %s", found.TaskID)
	}
}

func TestTerminalManager_RemoveSession(t *testing.T) {
	tm := NewTerminalManager()

	session := &VsockSession{
		ID:     "test-session",
		TaskID: "task-123",
	}
	tm.AddSession(session)
	tm.RemoveSession("test-session")

	if tm.GetSession("test-session") != nil {
		t.Error("expected session to be removed")
	}
}

func TestTerminalManager_GetSessionsByTask(t *testing.T) {
	tm := NewTerminalManager()

	tm.AddSession(&VsockSession{ID: "s1", TaskID: "task-1"})
	tm.AddSession(&VsockSession{ID: "s2", TaskID: "task-1"})
	tm.AddSession(&VsockSession{ID: "s3", TaskID: "task-2"})

	sessions := tm.GetSessionsByTask("task-1")
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions for task-1, got %d", len(sessions))
	}
}

func TestTerminalManager_CloseAllForTask(t *testing.T) {
	tm := NewTerminalManager()

	s1 := &VsockSession{ID: "s1", TaskID: "task-1"}
	s2 := &VsockSession{ID: "s2", TaskID: "task-1"}
	s3 := &VsockSession{ID: "s3", TaskID: "task-2"}

	tm.AddSession(s1)
	tm.AddSession(s2)
	tm.AddSession(s3)

	tm.CloseAllForTask("task-1")

	if len(tm.GetSessionsByTask("task-1")) != 0 {
		t.Error("expected all task-1 sessions to be removed")
	}
	if len(tm.GetSessionsByTask("task-2")) != 1 {
		t.Error("expected task-2 session to remain")
	}
}

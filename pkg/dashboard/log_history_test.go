package dashboard

import (
	"testing"
)

func TestLogHistory_StoresLines(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stdout", "line 1")
	history.AddLine("task-1", "stdout", "line 2")
	history.AddLine("task-1", "stderr", "error line")

	lines := history.Search("task-1", "line")
	if len(lines) != 3 {
		t.Errorf("expected 3 matching lines, got %d", len(lines))
	}
}

func TestLogHistory_FiltersStream(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stdout", "out line")
	history.AddLine("task-1", "stderr", "err line")

	lines := history.SearchStream("task-1", "stderr", "")
	if len(lines) != 1 {
		t.Errorf("expected 1 stderr line, got %d", len(lines))
	}
	if lines[0].Text != "err line" {
		t.Errorf("expected 'err line', got %q", lines[0].Text)
	}
}

func TestLogHistory_SearchIsCaseInsensitive(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stdout", "Hello World")
	history.AddLine("task-1", "stdout", "HELLO AGAIN")

	lines := history.Search("task-1", "hello")
	if len(lines) != 2 {
		t.Errorf("expected 2 matching lines (case insensitive), got %d", len(lines))
	}
}

func TestLogHistory_FiltersTaskID(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stdout", "task 1 line")
	history.AddLine("task-2", "stdout", "task 2 line")

	lines := history.Search("task-1", "")
	if len(lines) != 1 {
		t.Errorf("expected 1 line for task-1, got %d", len(lines))
	}
	if lines[0].TaskID != "task-1" {
		t.Errorf("expected task-1, got %s", lines[0].TaskID)
	}
}

func TestLogHistory_RespectsMaxLines(t *testing.T) {
	history := NewLogHistory(3)

	history.AddLine("task-1", "stdout", "line 1")
	history.AddLine("task-1", "stdout", "line 2")
	history.AddLine("task-1", "stdout", "line 3")
	history.AddLine("task-1", "stdout", "line 4")

	lines := history.Search("task-1", "")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines (max limit), got %d", len(lines))
	}
	// Should have dropped line 1
	if lines[0].Text != "line 2" {
		t.Errorf("expected first line to be 'line 2' (oldest dropped), got %q", lines[0].Text)
	}
}

func TestLogHistory_EmptyQueryReturnsAll(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stdout", "line 1")
	history.AddLine("task-1", "stderr", "line 2")

	lines := history.Search("task-1", "")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines with empty query, got %d", len(lines))
	}
}

func TestLogHistory_SearchStreamWithQuery(t *testing.T) {
	history := NewLogHistory(100)

	history.AddLine("task-1", "stderr", "error: connection failed")
	history.AddLine("task-1", "stderr", "warning: retrying")
	history.AddLine("task-1", "stdout", "connected")

	lines := history.SearchStream("task-1", "stderr", "error")
	if len(lines) != 1 {
		t.Errorf("expected 1 stderr line matching 'error', got %d", len(lines))
	}
}

func TestLogHistory_ConcurrentAccess(t *testing.T) {
	history := NewLogHistory(1000)

	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			history.AddLine("task-1", "stdout", "concurrent line")
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = history.Search("task-1", "")
		}
		done <- true
	}()

	<-done
	<-done
	// Test passes if no race condition panic
}

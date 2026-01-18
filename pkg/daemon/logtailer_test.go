package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type mockLogSink struct {
	mu       sync.Mutex
	received []struct {
		taskID, stream, line string
	}
}

func (m *mockLogSink) SendLog(taskID, stream, line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, struct {
		taskID, stream, line string
	}{taskID, stream, line})
}

func (m *mockLogSink) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.received)
}

func TestLogTailer_TailsLogFile(t *testing.T) {
	// Create temp directory and log file
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "stdout.log")
	os.WriteFile(logFile, []byte("line 1\nline 2\n"), 0644)

	sink := &mockLogSink{}
	tailer := NewLogTailer(sink)

	// Start tailing
	err := tailer.TailFile("task-1", "stdout", logFile)
	if err != nil {
		t.Fatalf("TailFile failed: %v", err)
	}
	defer tailer.Stop()

	// Wait for initial lines
	time.Sleep(100 * time.Millisecond)

	if sink.Len() < 2 {
		t.Errorf("expected at least 2 lines, got %d", sink.Len())
	}
}

func TestLogTailer_TailsNewContent(t *testing.T) {
	// Create temp directory and log file with initial content
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "stdout.log")
	os.WriteFile(logFile, []byte("initial line\n"), 0644)

	sink := &mockLogSink{}
	tailer := NewLogTailer(sink)

	// Start tailing
	err := tailer.TailFile("task-1", "stdout", logFile)
	if err != nil {
		t.Fatalf("TailFile failed: %v", err)
	}
	defer tailer.Stop()

	// Wait for initial content
	time.Sleep(150 * time.Millisecond)

	initialCount := sink.Len()
	if initialCount < 1 {
		t.Fatalf("expected at least 1 initial line, got %d", initialCount)
	}

	// Append new content to the file
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open file for append: %v", err)
	}
	f.WriteString("new line 1\n")
	f.WriteString("new line 2\n")
	f.Close()

	// Wait for tailer to pick up new content
	time.Sleep(300 * time.Millisecond)

	finalCount := sink.Len()
	if finalCount < initialCount+2 {
		t.Errorf("expected at least %d lines after append, got %d", initialCount+2, finalCount)
	}
}

func TestLogTailer_StopTask(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "stdout.log")
	os.WriteFile(logFile, []byte("line 1\n"), 0644)

	sink := &mockLogSink{}
	tailer := NewLogTailer(sink)

	tailer.TailFile("task-1", "stdout", logFile)
	tailer.TailFile("task-1", "stderr", logFile)
	tailer.TailFile("task-2", "stdout", logFile)

	time.Sleep(50 * time.Millisecond)

	// Stop only task-1
	tailer.StopTask("task-1")

	// Verify task-2 is still tracked
	tailer.mu.Lock()
	_, task2Exists := tailer.tailers["task-2:stdout"]
	_, task1StdoutExists := tailer.tailers["task-1:stdout"]
	tailer.mu.Unlock()

	if !task2Exists {
		t.Error("task-2 should still be tailing")
	}
	if task1StdoutExists {
		t.Error("task-1:stdout should have been stopped")
	}

	tailer.Stop()
}

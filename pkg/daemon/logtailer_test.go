package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type mockLogSink struct {
	received []struct {
		taskID, stream, line string
	}
}

func (m *mockLogSink) SendLog(taskID, stream, line string) {
	m.received = append(m.received, struct {
		taskID, stream, line string
	}{taskID, stream, line})
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

	if len(sink.received) < 2 {
		t.Errorf("expected at least 2 lines, got %d", len(sink.received))
	}
}

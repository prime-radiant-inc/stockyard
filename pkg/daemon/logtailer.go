package daemon

import (
	"bufio"
	"io"
	"os"
	"sync"
	"time"
)

// LogSink receives log lines from the tailer.
type LogSink interface {
	SendLog(taskID, stream, line string)
}

// LogTailer tails VM log files and sends lines to a sink.
type LogTailer struct {
	sink    LogSink
	tailers map[string]chan struct{}
	mu      sync.Mutex
}

// NewLogTailer creates a new log tailer.
func NewLogTailer(sink LogSink) *LogTailer {
	return &LogTailer{
		sink:    sink,
		tailers: make(map[string]chan struct{}),
	}
}

// TailFile starts tailing a log file for a task.
func (t *LogTailer) TailFile(taskID, stream, path string) error {
	key := taskID + ":" + stream

	t.mu.Lock()
	if _, exists := t.tailers[key]; exists {
		t.mu.Unlock()
		return nil // Already tailing
	}
	stop := make(chan struct{})
	t.tailers[key] = stop
	t.mu.Unlock()

	go t.tailLoop(taskID, stream, path, stop)
	return nil
}

// StopTask stops tailing all files for a task.
func (t *LogTailer) StopTask(taskID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for key, stop := range t.tailers {
		if len(key) > len(taskID) && key[:len(taskID)+1] == taskID+":" {
			close(stop)
			delete(t.tailers, key)
		}
	}
}

// Stop stops all tailers.
func (t *LogTailer) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, stop := range t.tailers {
		close(stop)
	}
	t.tailers = make(map[string]chan struct{})
}

func (t *LogTailer) tailLoop(taskID, stream, path string, stop chan struct{}) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var lineBuf []byte

	for {
		select {
		case <-stop:
			return
		default:
		}

		line, isPrefix, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				// No more data, wait and try again
				time.Sleep(100 * time.Millisecond)
				continue
			}
			// Actual error, bail
			return
		}

		// Handle long lines that come in pieces
		lineBuf = append(lineBuf, line...)
		if isPrefix {
			// Line continues, keep reading
			continue
		}

		// Complete line received
		t.sink.SendLog(taskID, stream, string(lineBuf))
		lineBuf = lineBuf[:0] // Reset buffer, keep capacity
	}
}

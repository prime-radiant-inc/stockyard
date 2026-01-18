package dashboard

import (
	"encoding/json"
	"time"
)

// LogEntry represents a single log line.
type LogEntry struct {
	Type      string    `json:"type"`      // "log"
	TaskID    string    `json:"task_id"`
	Stream    string    `json:"stream"`    // "stdout" or "stderr"
	Line      string    `json:"line"`
	Timestamp time.Time `json:"timestamp"`
}

// LogStreamer manages log streaming to WebSocket clients.
type LogStreamer struct {
	hub *Hub
}

// NewLogStreamer creates a new log streamer.
func NewLogStreamer(hub *Hub) *LogStreamer {
	return &LogStreamer{hub: hub}
}

// SendLog broadcasts a log line to subscribed clients.
func (l *LogStreamer) SendLog(taskID, stream, line string) {
	entry := LogEntry{
		Type:      "log",
		TaskID:    taskID,
		Stream:    stream,
		Line:      line,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	l.hub.Broadcast(taskID, data)
}

// SendLogBatch broadcasts multiple log lines efficiently.
func (l *LogStreamer) SendLogBatch(taskID string, entries []LogEntry) {
	for _, entry := range entries {
		entry.Type = "log"
		entry.TaskID = taskID
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		l.hub.Broadcast(taskID, data)
	}
}

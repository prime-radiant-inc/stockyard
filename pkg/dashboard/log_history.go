package dashboard

import (
	"strings"
	"sync"
	"time"
)

// LogLine represents a stored log line for history search.
type LogLine struct {
	TaskID    string    `json:"task_id"`
	Stream    string    `json:"stream"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// LogHistory stores recent log lines for search.
type LogHistory struct {
	lines    []LogLine
	maxLines int
	mu       sync.RWMutex
}

// NewLogHistory creates a new log history store.
func NewLogHistory(maxLines int) *LogHistory {
	return &LogHistory{
		lines:    make([]LogLine, 0, maxLines),
		maxLines: maxLines,
	}
}

// AddLine stores a log line.
func (lh *LogHistory) AddLine(taskID, stream, text string) {
	lh.mu.Lock()
	defer lh.mu.Unlock()

	lh.lines = append(lh.lines, LogLine{
		TaskID:    taskID,
		Stream:    stream,
		Text:      text,
		Timestamp: time.Now(),
	})

	if len(lh.lines) > lh.maxLines {
		lh.lines = lh.lines[1:]
	}
}

// Search finds lines matching a query string for a specific task.
func (lh *LogHistory) Search(taskID, query string) []LogLine {
	lh.mu.RLock()
	defer lh.mu.RUnlock()

	var results []LogLine
	query = strings.ToLower(query)
	for _, line := range lh.lines {
		if line.TaskID == taskID {
			if query == "" || strings.Contains(strings.ToLower(line.Text), query) {
				results = append(results, line)
			}
		}
	}
	return results
}

// SearchStream finds lines matching query in a specific stream.
func (lh *LogHistory) SearchStream(taskID, stream, query string) []LogLine {
	lh.mu.RLock()
	defer lh.mu.RUnlock()

	var results []LogLine
	query = strings.ToLower(query)
	for _, line := range lh.lines {
		if line.TaskID == taskID && line.Stream == stream {
			if query == "" || strings.Contains(strings.ToLower(line.Text), query) {
				results = append(results, line)
			}
		}
	}
	return results
}

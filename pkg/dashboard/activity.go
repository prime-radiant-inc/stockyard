package dashboard

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ActivityEvent represents an event in the activity feed.
type ActivityEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // vm_started, vm_stopped, vm_failed, snapshot_created
	TaskID    string    `json:"task_id"`
	TaskName  string    `json:"task_name"`
	RepoURL   string    `json:"repo_url"`
	Owner     string    `json:"owner"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// ActivityFeed maintains a rolling log of recent events.
type ActivityFeed struct {
	events   []ActivityEvent
	maxSize  int
	hub      *Hub
	mu       sync.RWMutex
	sequence int64
}

// NewActivityFeed creates a new activity feed.
func NewActivityFeed(maxSize int) *ActivityFeed {
	return &ActivityFeed{
		events:  make([]ActivityEvent, 0, maxSize),
		maxSize: maxSize,
	}
}

// NewActivityFeedWithHub creates an activity feed that broadcasts events.
func NewActivityFeedWithHub(maxSize int, hub *Hub) *ActivityFeed {
	af := NewActivityFeed(maxSize)
	af.hub = hub
	return af
}

// RecordEvent adds an event to the feed.
func (af *ActivityFeed) RecordEvent(event ActivityEvent) {
	af.mu.Lock()
	defer af.mu.Unlock()

	af.sequence++
	event.ID = fmt.Sprintf("evt-%d", af.sequence)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	af.events = append(af.events, event)
	if len(af.events) > af.maxSize {
		af.events = af.events[1:]
	}

	// Broadcast to connected clients
	if af.hub != nil {
		msg := struct {
			Type  string        `json:"type"`
			Event ActivityEvent `json:"event"`
		}{
			Type:  "activity",
			Event: event,
		}
		if data, err := json.Marshal(msg); err == nil {
			af.hub.BroadcastAll(data)
		}
	}
}

// GetRecent returns the most recent events (newest first).
func (af *ActivityFeed) GetRecent(n int) []ActivityEvent {
	af.mu.RLock()
	defer af.mu.RUnlock()

	if n > len(af.events) {
		n = len(af.events)
	}

	// Return in reverse order (newest first)
	result := make([]ActivityEvent, n)
	for i := 0; i < n; i++ {
		result[i] = af.events[len(af.events)-1-i]
	}
	return result
}

// Helper methods for common events

// VMStarted records a VM started event.
func (af *ActivityFeed) VMStarted(taskID, taskName, repoURL, owner string) {
	af.RecordEvent(ActivityEvent{
		Type:     "vm_started",
		TaskID:   taskID,
		TaskName: taskName,
		RepoURL:  repoURL,
		Owner:    owner,
		Message:  "VM started",
	})
}

// VMStopped records a VM stopped event.
func (af *ActivityFeed) VMStopped(taskID, taskName string) {
	af.RecordEvent(ActivityEvent{
		Type:     "vm_stopped",
		TaskID:   taskID,
		TaskName: taskName,
		Message:  "VM stopped",
	})
}

// VMFailed records a VM failed event.
func (af *ActivityFeed) VMFailed(taskID, taskName, reason string) {
	af.RecordEvent(ActivityEvent{
		Type:     "vm_failed",
		TaskID:   taskID,
		TaskName: taskName,
		Message:  "VM failed: " + reason,
	})
}

// SnapshotCreated records a snapshot created event.
func (af *ActivityFeed) SnapshotCreated(taskID, snapshotName, label string) {
	af.RecordEvent(ActivityEvent{
		Type:    "snapshot_created",
		TaskID:  taskID,
		Message: "Snapshot created: " + snapshotName,
	})
}

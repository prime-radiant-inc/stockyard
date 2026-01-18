package dashboard

import (
	"encoding/json"
	"time"
)

// StatusMessage represents a VM status change.
type StatusMessage struct {
	Type      string    `json:"type"` // "status"
	TaskID    string    `json:"task_id"`
	OldStatus string    `json:"old_status"`
	NewStatus string    `json:"new_status"`
	Timestamp time.Time `json:"timestamp"`
}

// StatusBroadcaster sends status updates to clients.
type StatusBroadcaster struct {
	hub *Hub
}

// NewStatusBroadcaster creates a new status broadcaster.
func NewStatusBroadcaster(hub *Hub) *StatusBroadcaster {
	return &StatusBroadcaster{hub: hub}
}

// TaskStatusChanged broadcasts a status change.
func (s *StatusBroadcaster) TaskStatusChanged(taskID, oldStatus, newStatus string) {
	msg := StatusMessage{
		Type:      "status",
		TaskID:    taskID,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	// Broadcast to all clients (status changes are fleet-wide events)
	s.hub.BroadcastAll(data)
}

// TaskCreated broadcasts that a new task was created.
func (s *StatusBroadcaster) TaskCreated(taskID string) {
	s.TaskStatusChanged(taskID, "", "pending")
}

// TaskDestroyed broadcasts that a task was destroyed.
func (s *StatusBroadcaster) TaskDestroyed(taskID string) {
	s.TaskStatusChanged(taskID, "stopped", "destroyed")
}

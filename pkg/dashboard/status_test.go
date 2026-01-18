package dashboard

import (
	"testing"
	"time"
)

func TestStatusBroadcaster_SendsUpdates(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	broadcaster := NewStatusBroadcaster(hub)

	client := &Client{
		hub:    hub,
		taskID: "", // Subscribe to all
		send:   make(chan []byte, 256),
	}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	broadcaster.TaskStatusChanged("task-1", "running", "stopped")

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("expected status message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for status message")
	}
}

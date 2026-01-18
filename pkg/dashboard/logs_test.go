package dashboard

import (
	"testing"
	"time"
)

func TestLogStreamer_BroadcastsLogs(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	streamer := NewLogStreamer(hub)

	// Create a receiving client
	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	// Send a log line
	streamer.SendLog("task-1", "stdout", "Hello from VM")

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("expected log message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for log message")
	}
}

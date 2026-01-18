package dashboard

import (
	"testing"
	"time"
)

func TestHub_BroadcastMessage(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	// Create a test client
	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}

	hub.Register(client)
	time.Sleep(10 * time.Millisecond) // Let register process

	// Broadcast a message
	hub.Broadcast("task-1", []byte("test message"))
	time.Sleep(10 * time.Millisecond) // Let broadcast process

	select {
	case msg := <-client.send:
		if string(msg) != "test message" {
			t.Errorf("expected 'test message', got '%s'", string(msg))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected message but got none")
	}
}

func TestHub_ClientUnsubscribe(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}

	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	hub.Unregister(client)
	time.Sleep(10 * time.Millisecond)

	// Broadcast should not panic
	hub.Broadcast("task-1", []byte("test"))
}

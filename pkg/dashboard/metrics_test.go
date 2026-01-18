package dashboard

import (
	"testing"
	"time"
)

func TestMetricsCollector_BroadcastsMetrics(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	collector := NewMetricsCollector(hub)

	// Create a receiving client
	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	// Send metrics
	collector.SendMetrics("task-1", VMMetrics{
		CPUPercent:     45.5,
		MemoryBytes:    2147483648,  // 2GB
		MemoryMaxBytes: 4294967296,  // 4GB
		NetworkRxBytes: 1024000,
		NetworkTxBytes: 512000,
	})

	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("expected metrics message")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for metrics message")
	}
}

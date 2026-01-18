package dashboard

import (
	"testing"
	"time"
)

func TestMetricsCollector_BroadcastsMetrics(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	collector := NewMetricsCollector(hub, nil)

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

func TestMetricsCollector_BroadcastsAlerts(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	alertChecker := NewAlertChecker()
	collector := NewMetricsCollector(hub, alertChecker)

	// Create a receiving client
	client := &Client{
		hub:    hub,
		taskID: "task-1",
		send:   make(chan []byte, 256),
	}
	hub.Register(client)
	time.Sleep(10 * time.Millisecond)

	// Send metrics that trigger high CPU alert
	collector.SendMetrics("task-1", VMMetrics{
		CPUPercent:     95.0, // Above 90% threshold
		MemoryBytes:    1024 * 1024 * 1024,
		MemoryMaxBytes: 4 * 1024 * 1024 * 1024,
	})

	// Should receive metrics message first
	select {
	case <-client.send:
		// Got metrics, now wait for alerts
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for metrics message")
	}

	// Should receive alerts message
	select {
	case msg := <-client.send:
		if len(msg) == 0 {
			t.Error("expected alerts message")
		}
		// Verify it contains "alerts" type
		if !contains(string(msg), `"type":"alerts"`) {
			t.Errorf("expected alerts message type, got: %s", string(msg))
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for alerts message")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

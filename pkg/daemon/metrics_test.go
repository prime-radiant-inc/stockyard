package daemon

import (
	"testing"
	"time"
)

func TestMetricsPoller_StartsAndStops(t *testing.T) {
	poller := NewMetricsPoller(nil, nil, 100*time.Millisecond)

	poller.Start()
	time.Sleep(50 * time.Millisecond)

	if !poller.Running() {
		t.Error("expected poller to be running")
	}

	poller.Stop()
	time.Sleep(50 * time.Millisecond)

	if poller.Running() {
		t.Error("expected poller to be stopped")
	}
}

func TestMetricsPoller_CleanupTask(t *testing.T) {
	poller := NewMetricsPoller(nil, nil, 100*time.Millisecond)
	poller.Start()
	defer poller.Stop()

	// Simulate adding a task with both reader and state (as StartTaskMetrics does)
	poller.mu.Lock()
	poller.state["task-1"] = &vmMetricsState{
		lastVCPUExits: 100,
		lastTimestamp: time.Now(),
		memoryBytes:   2147483648,
	}
	// Add a nil reader to simulate the reader entry existing
	// (In real usage, StartTaskMetrics creates an actual reader)
	poller.readers["task-1"] = nil
	poller.mu.Unlock()

	// Verify state exists
	poller.mu.Lock()
	_, stateExists := poller.state["task-1"]
	_, readerExists := poller.readers["task-1"]
	poller.mu.Unlock()
	if !stateExists {
		t.Error("expected state to exist before cleanup")
	}
	if !readerExists {
		t.Error("expected reader entry to exist before cleanup")
	}

	// Cleanup (which now calls StopTaskMetrics)
	poller.CleanupTask("task-1")

	// Verify state and reader removed
	poller.mu.Lock()
	_, stateExists = poller.state["task-1"]
	_, readerExists = poller.readers["task-1"]
	poller.mu.Unlock()
	if stateExists {
		t.Error("expected state to be removed after cleanup")
	}
	if readerExists {
		t.Error("expected reader entry to be removed after cleanup")
	}
}

func TestMetricsPoller_StateInitialization(t *testing.T) {
	poller := NewMetricsPoller(nil, nil, 100*time.Millisecond)

	if poller.state == nil {
		t.Error("state should be initialized")
	}

	if len(poller.state) != 0 {
		t.Error("state should be empty initially")
	}

	if poller.readers == nil {
		t.Error("readers should be initialized")
	}

	if len(poller.readers) != 0 {
		t.Error("readers should be empty initially")
	}
}

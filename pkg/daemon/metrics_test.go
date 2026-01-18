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

	// Simulate adding state for a task
	poller.vmStateMu.Lock()
	poller.vmState["task-1"] = &vmMetricsState{
		lastVCPUExits: 100,
		lastTimestamp: time.Now(),
		memoryBytes:   2147483648,
		vcpuCount:     2,
	}
	poller.vmStateMu.Unlock()

	// Verify state exists
	poller.vmStateMu.Lock()
	_, exists := poller.vmState["task-1"]
	poller.vmStateMu.Unlock()
	if !exists {
		t.Error("expected state to exist before cleanup")
	}

	// Cleanup
	poller.CleanupTask("task-1")

	// Verify state removed
	poller.vmStateMu.Lock()
	_, exists = poller.vmState["task-1"]
	poller.vmStateMu.Unlock()
	if exists {
		t.Error("expected state to be removed after cleanup")
	}
}

func TestMetricsPoller_StateInitialization(t *testing.T) {
	poller := NewMetricsPoller(nil, nil, 100*time.Millisecond)

	if poller.vmState == nil {
		t.Error("vmState should be initialized")
	}

	if len(poller.vmState) != 0 {
		t.Error("vmState should be empty initially")
	}
}

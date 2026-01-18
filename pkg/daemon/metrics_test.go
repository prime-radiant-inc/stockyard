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

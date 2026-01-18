package dashboard

import (
	"testing"
)

func TestActivityFeed_RecordsEvents(t *testing.T) {
	feed := NewActivityFeed(100)

	feed.RecordEvent(ActivityEvent{
		Type:    "vm_started",
		TaskID:  "task-1",
		Message: "VM started",
	})

	events := feed.GetRecent(10)
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != "vm_started" {
		t.Errorf("expected vm_started, got %s", events[0].Type)
	}
}

func TestActivityFeed_LimitsSize(t *testing.T) {
	feed := NewActivityFeed(5)

	for i := 0; i < 10; i++ {
		feed.RecordEvent(ActivityEvent{
			Type:    "test",
			Message: "Event",
		})
	}

	events := feed.GetRecent(100)
	if len(events) != 5 {
		t.Errorf("expected 5 events (max), got %d", len(events))
	}
}

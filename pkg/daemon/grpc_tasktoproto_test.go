package daemon

import (
	"testing"
	"time"
)

func TestTaskToProto_PopulatesBackendAndVMID(t *testing.T) {
	task := &Task{
		ID:        "abc12345",
		Name:      "demo",
		Status:    "running",
		VMID:      "abc12345",
		IP:        "192.168.64.7",
		CreatedAt: time.Now(),
	}
	pt := taskToProto(task, "apple-container")
	if pt.Backend != "apple-container" {
		t.Errorf("expected backend apple-container, got %q", pt.Backend)
	}
	if pt.VmId != "abc12345" {
		t.Errorf("expected vm_id abc12345, got %q", pt.VmId)
	}
}

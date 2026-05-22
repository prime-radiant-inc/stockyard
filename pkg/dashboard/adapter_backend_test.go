package dashboard

import "testing"

func TestConvertTask_CarriesBackendAndVMID(t *testing.T) {
	dt := &DaemonTask{
		ID:      "abc12345",
		Name:    "demo",
		Status:  "running",
		VMID:    "abc12345",
		Backend: "apple-container",
	}
	got := convertTask(dt)
	if got.Backend != "apple-container" {
		t.Errorf("expected Backend apple-container, got %q", got.Backend)
	}
	if got.VMID != "abc12345" {
		t.Errorf("expected VMID abc12345, got %q", got.VMID)
	}
}

package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceCollector_CollectVMDirs(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	vmDir := filepath.Join(tmpDir, "vms")
	if err := os.MkdirAll(filepath.Join(vmDir, "task-1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vmDir, "task-2"), 0755); err != nil {
		t.Fatal(err)
	}

	// Write MAC address file for task-1
	if err := os.WriteFile(filepath.Join(vmDir, "task-1", "mac_addr"), []byte("aa:bb:cc:dd:ee:ff"), 0644); err != nil {
		t.Fatal(err)
	}

	tasks := []Task{
		{ID: "task-1", Status: "running"},
	}

	rc := NewResourceCollector(vmDir, "/nonexistent", "tank/stockyard")
	resources, err := rc.Collect(tasks)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 2 VM directories
	vmDirs := filterByType(resources, "vm")
	if len(vmDirs) != 2 {
		t.Errorf("expected 2 VM directories, got %d", len(vmDirs))
	}

	// task-1 should be running, task-2 should be orphan
	var foundTask1, foundTask2 bool
	for _, r := range vmDirs {
		if r.ID == "task-1" {
			foundTask1 = true
			if r.Status != "running" {
				t.Errorf("expected task-1 status running, got %s", r.Status)
			}
		}
		if r.ID == "task-2" {
			foundTask2 = true
			if r.Status != "orphan" {
				t.Errorf("expected task-2 status orphan, got %s", r.Status)
			}
		}
	}
	if !foundTask1 {
		t.Error("expected to find task-1")
	}
	if !foundTask2 {
		t.Error("expected to find task-2")
	}
}

func TestResourceCollector_CollectDHCPLeases(t *testing.T) {
	tmpDir := t.TempDir()
	leaseFile := filepath.Join(tmpDir, "dnsmasq.leases")

	// Write sample lease file
	// Format: <expiry_timestamp> <MAC> <IP> <hostname> <client-id>
	leaseContent := "9999999999 aa:bb:cc:dd:ee:ff 192.168.64.100 vm-1 *\n"
	leaseContent += "9999999999 11:22:33:44:55:66 192.168.64.101 vm-2 *\n"
	if err := os.WriteFile(leaseFile, []byte(leaseContent), 0644); err != nil {
		t.Fatal(err)
	}

	tasks := []Task{
		{ID: "vm-1", Status: "running"},
	}

	rc := NewResourceCollector("/nonexistent", leaseFile, "tank/stockyard")
	resources, err := rc.Collect(tasks)
	if err != nil {
		t.Fatal(err)
	}

	dhcpLeases := filterByType(resources, "dhcp")
	if len(dhcpLeases) != 2 {
		t.Errorf("expected 2 DHCP leases, got %d", len(dhcpLeases))
	}

	// Check that leases have correct info
	for _, r := range dhcpLeases {
		if r.ID == "" {
			t.Error("expected lease to have an ID")
		}
		if r.Details == "" {
			t.Error("expected lease to have details")
		}
	}
}

func TestResourceCollector_EmptyDirs(t *testing.T) {
	rc := NewResourceCollector("/nonexistent", "/nonexistent", "tank/stockyard")
	resources, err := rc.Collect(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Should return empty list, not error
	if resources == nil {
		t.Error("expected empty slice, not nil")
	}
}

func TestServer_ResourcesPage(t *testing.T) {
	mock := &MockDaemon{
		tasks: []Task{
			{ID: "task-1", Name: "test-vm", Status: "running"},
			{ID: "task-2", Name: "test-vm-2", Status: "stopped"},
		},
	}
	srv := NewServer(mock, "")

	req := httptest.NewRequest("GET", "/resources", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should mention "Resources" in some form
	if !strings.Contains(strings.ToLower(body), "resource") {
		t.Error("expected 'resource' in output")
	}
}

func TestServer_ResourcesPage_NoDaemon(t *testing.T) {
	srv := NewServer(nil, "")

	req := httptest.NewRequest("GET", "/resources", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}
}

func filterByType(resources []Resource, resType string) []Resource {
	var result []Resource
	for _, r := range resources {
		if r.Type == resType {
			result = append(result, r)
		}
	}
	return result
}

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
	leaseContent := "9999999999 aa:bb:cc:dd:ee:ff 10.0.100.100 vm-1 *\n"
	leaseContent += "9999999999 11:22:33:44:55:66 10.0.100.101 vm-2 *\n"
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

func TestParseTapOutput(t *testing.T) {
	// Sample output from "ip -o link show"
	output := `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN mode DEFAULT
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP mode DEFAULT
3: tap-abc12345: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel master flbr0 state UP mode DEFAULT
4: tap-xyz98765: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel master flbr0 state UP mode DEFAULT
5: flbr0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP mode DEFAULT
`

	tapToTask := map[string]string{
		"tap-abc12345": "task-1",
	}
	taskStatus := map[string]string{
		"task-1": "running",
	}

	resources := parseTapOutput(output, tapToTask, taskStatus)

	if len(resources) != 2 {
		t.Errorf("expected 2 tap interfaces, got %d", len(resources))
	}

	// Find tap-abc12345 - should be active because task-1 is running
	var foundKnown, foundOrphan bool
	for _, r := range resources {
		if r.ID == "tap-abc12345" {
			foundKnown = true
			if r.Status != "active" {
				t.Errorf("expected tap-abc12345 status 'active', got %s", r.Status)
			}
			if r.TaskID != "task-1" {
				t.Errorf("expected tap-abc12345 TaskID 'task-1', got %s", r.TaskID)
			}
		}
		if r.ID == "tap-xyz98765" {
			foundOrphan = true
			if r.Status != "orphan" {
				t.Errorf("expected tap-xyz98765 status 'orphan', got %s", r.Status)
			}
		}
	}

	if !foundKnown {
		t.Error("expected to find tap-abc12345")
	}
	if !foundOrphan {
		t.Error("expected to find tap-xyz98765")
	}
}

func TestParseTapOutput_EmptyOutput(t *testing.T) {
	resources := parseTapOutput("", nil, nil)
	if len(resources) != 0 {
		t.Errorf("expected 0 resources for empty output, got %d", len(resources))
	}
}

func TestParseTapOutput_NoTapInterfaces(t *testing.T) {
	output := `1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN mode DEFAULT
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UP mode DEFAULT
`
	resources := parseTapOutput(output, nil, nil)
	if len(resources) != 0 {
		t.Errorf("expected 0 tap interfaces, got %d", len(resources))
	}
}

func TestParseZfsOutput(t *testing.T) {
	// Sample output from "zfs list -H -r -d 1 -o name,used tank/stockyard/vms"
	output := `tank/stockyard/vms	1.5G
tank/stockyard/vms/task-1	512M
tank/stockyard/vms/task-2	1G
`

	taskStatus := map[string]string{
		"task-1": "running",
	}

	resources := parseZfsOutput(output, "tank/stockyard/vms", "zfs_vm", taskStatus)

	if len(resources) != 2 {
		t.Errorf("expected 2 ZFS datasets, got %d", len(resources))
	}

	var foundTask1, foundTask2 bool
	for _, r := range resources {
		if r.ID == "task-1" {
			foundTask1 = true
			if r.Status != "active" {
				t.Errorf("expected task-1 status 'active', got %s", r.Status)
			}
			if r.Size != "512M" {
				t.Errorf("expected task-1 size '512M', got %s", r.Size)
			}
			if r.Type != "zfs_vm" {
				t.Errorf("expected task-1 type 'zfs_vm', got %s", r.Type)
			}
		}
		if r.ID == "task-2" {
			foundTask2 = true
			if r.Status != "orphan" {
				t.Errorf("expected task-2 status 'orphan', got %s", r.Status)
			}
			if r.Size != "1G" {
				t.Errorf("expected task-2 size '1G', got %s", r.Size)
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

func TestParseZfsOutput_EmptyOutput(t *testing.T) {
	resources := parseZfsOutput("", "tank/stockyard/vms", "zfs_vm", nil)
	if len(resources) != 0 {
		t.Errorf("expected 0 resources for empty output, got %d", len(resources))
	}
}

func TestParseZfsOutput_OnlyBasePath(t *testing.T) {
	// Output contains only the base path itself
	output := `tank/stockyard/vms	1.5G
`
	resources := parseZfsOutput(output, "tank/stockyard/vms", "zfs_vm", nil)
	if len(resources) != 0 {
		t.Errorf("expected 0 resources when only base path is present, got %d", len(resources))
	}
}

func TestParseZfsOutput_WorkspaceType(t *testing.T) {
	output := `tank/stockyard/workspaces	2G
tank/stockyard/workspaces/ws-1	1G
`

	resources := parseZfsOutput(output, "tank/stockyard/workspaces", "zfs_workspace", nil)

	if len(resources) != 1 {
		t.Errorf("expected 1 ZFS workspace, got %d", len(resources))
	}

	if resources[0].Type != "zfs_workspace" {
		t.Errorf("expected type 'zfs_workspace', got %s", resources[0].Type)
	}
}

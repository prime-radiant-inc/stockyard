// cmd/stockyard/resources_test.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/config"
)

func TestResourceCollector_IsVMRunning_NoFile(t *testing.T) {
	rc := &ResourceCollector{}
	tmpDir := t.TempDir()

	// No pid file - should return false
	if rc.isVMRunning(tmpDir) {
		t.Error("expected false for missing pid file")
	}
}

func TestResourceCollector_IsVMRunning_InvalidPid(t *testing.T) {
	rc := &ResourceCollector{}
	tmpDir := t.TempDir()

	// Write invalid pid
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte("notanumber"), 0644); err != nil {
		t.Fatal(err)
	}

	if rc.isVMRunning(tmpDir) {
		t.Error("expected false for invalid pid")
	}
}

func TestResourceCollector_IsVMRunning_NonexistentPid(t *testing.T) {
	rc := &ResourceCollector{}
	tmpDir := t.TempDir()

	// Write a pid that doesn't exist (high number unlikely to be in use)
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}

	if rc.isVMRunning(tmpDir) {
		t.Error("expected false for nonexistent pid")
	}
}

func TestResourceCollector_IsVMRunning_CurrentProcess(t *testing.T) {
	rc := &ResourceCollector{}
	tmpDir := t.TempDir()

	// Write our own pid - should be running
	pid := os.Getpid()
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		t.Fatal(err)
	}

	// Our own process should be running
	if !rc.isVMRunning(tmpDir) {
		t.Error("expected true for our own pid")
	}
}

func TestResourceCollector_CollectVMDirs_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	rc := &ResourceCollector{
		cfg:     config.DefaultConfig(),
		vmDir:   tmpDir,
		taskIDs: make(map[string]string),
	}

	rc.collectVMDirs()

	if len(rc.resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(rc.resources))
	}
}

func TestResourceCollector_CollectVMDirs_WithDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some VM directories
	os.Mkdir(filepath.Join(tmpDir, "vm1"), 0755)
	os.Mkdir(filepath.Join(tmpDir, "vm2"), 0755)

	rc := &ResourceCollector{
		cfg:     config.DefaultConfig(),
		vmDir:   tmpDir,
		taskIDs: make(map[string]string),
	}

	rc.collectVMDirs()

	if len(rc.resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(rc.resources))
	}

	// Both should be orphans since taskIDs is empty
	for _, r := range rc.resources {
		if r.Status != "orphan" {
			t.Errorf("expected status 'orphan', got %q", r.Status)
		}
		if r.Type != "vm-dir" {
			t.Errorf("expected type 'vm-dir', got %q", r.Type)
		}
	}
}

func TestResourceCollector_CollectVMDirs_KnownTask(t *testing.T) {
	tmpDir := t.TempDir()

	// Create VM directory
	os.Mkdir(filepath.Join(tmpDir, "known-vm"), 0755)

	rc := &ResourceCollector{
		cfg:   config.DefaultConfig(),
		vmDir: tmpDir,
		taskIDs: map[string]string{
			"known-vm": "stopped",
		},
	}

	rc.collectVMDirs()

	if len(rc.resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(rc.resources))
	}

	// Should be stopped, not orphan
	if rc.resources[0].Status != "stopped" {
		t.Errorf("expected status 'stopped', got %q", rc.resources[0].Status)
	}
}

func TestResourceCollector_CollectVMDirs_RunningVM(t *testing.T) {
	tmpDir := t.TempDir()

	// Create VM directory with pid file pointing to our own process
	vmDir := filepath.Join(tmpDir, "running-vm")
	os.Mkdir(vmDir, 0755)
	os.WriteFile(filepath.Join(vmDir, "firecracker.pid"), []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	rc := &ResourceCollector{
		cfg:   config.DefaultConfig(),
		vmDir: tmpDir,
		taskIDs: map[string]string{
			"running-vm": "running",
		},
	}

	rc.collectVMDirs()

	if len(rc.resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(rc.resources))
	}

	if rc.resources[0].Status != "running" {
		t.Errorf("expected status 'running', got %q", rc.resources[0].Status)
	}
}

func TestResourceCollector_CollectVMDirs_SkipsFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file (not a directory)
	os.WriteFile(filepath.Join(tmpDir, "not-a-vm"), []byte("test"), 0644)
	// Create a directory
	os.Mkdir(filepath.Join(tmpDir, "is-a-vm"), 0755)

	rc := &ResourceCollector{
		cfg:     config.DefaultConfig(),
		vmDir:   tmpDir,
		taskIDs: make(map[string]string),
	}

	rc.collectVMDirs()

	if len(rc.resources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(rc.resources))
	}
}

func TestResourcesCommand_Help(t *testing.T) {
	// Just verify the command is registered and has proper help
	if resourcesCmd.Use != "resources" {
		t.Errorf("expected Use 'resources', got %q", resourcesCmd.Use)
	}
	if resourcesCmd.Short == "" {
		t.Error("expected non-empty Short description")
	}
}

func TestResourceCollector_LoadDHCPLeases(t *testing.T) {
	tmpDir := t.TempDir()
	leaseFile := filepath.Join(tmpDir, "dnsmasq.leases")

	// Write a valid lease file
	// Format: <expiry> <MAC> <IP> <hostname> <client-id>
	expiry := fmt.Sprintf("%d", 9999999999) // Far future
	leaseContent := fmt.Sprintf("%s aa:bb:cc:dd:ee:ff 192.168.1.100 test-host *\n", expiry)
	if err := os.WriteFile(leaseFile, []byte(leaseContent), 0644); err != nil {
		t.Fatal(err)
	}

	rc := &ResourceCollector{
		cfg:       config.DefaultConfig(),
		leaseFile: leaseFile,
		macToIP:   make(map[string]string),
	}

	rc.loadDHCPLeases()

	if len(rc.leases) != 1 {
		t.Fatalf("expected 1 lease, got %d", len(rc.leases))
	}

	lease := rc.leases[0]
	if lease.MAC != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("expected MAC 'aa:bb:cc:dd:ee:ff', got %q", lease.MAC)
	}
	if lease.IP != "192.168.1.100" {
		t.Errorf("expected IP '192.168.1.100', got %q", lease.IP)
	}
	if lease.Hostname != "test-host" {
		t.Errorf("expected hostname 'test-host', got %q", lease.Hostname)
	}

	// Check MAC to IP mapping
	if ip, ok := rc.macToIP["aa:bb:cc:dd:ee:ff"]; !ok || ip != "192.168.1.100" {
		t.Errorf("expected macToIP mapping, got %v", rc.macToIP)
	}
}

func TestResourceCollector_LoadDHCPLeases_MissingFile(t *testing.T) {
	rc := &ResourceCollector{
		cfg:       config.DefaultConfig(),
		leaseFile: "/nonexistent/file",
		macToIP:   make(map[string]string),
	}

	// Should not panic, just return empty
	rc.loadDHCPLeases()

	if len(rc.leases) != 0 {
		t.Errorf("expected 0 leases, got %d", len(rc.leases))
	}
}

func TestResourceCollector_LoadDHCPLeases_InvalidLines(t *testing.T) {
	tmpDir := t.TempDir()
	leaseFile := filepath.Join(tmpDir, "dnsmasq.leases")

	// Write lease file with some invalid lines
	content := "invalid line\n"
	content += "not-a-number aa:bb:cc:dd:ee:ff 192.168.1.100 test\n"
	content += fmt.Sprintf("%d aa:bb:cc:dd:ee:ff 192.168.1.100 valid *\n", 9999999999)
	if err := os.WriteFile(leaseFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	rc := &ResourceCollector{
		cfg:       config.DefaultConfig(),
		leaseFile: leaseFile,
		macToIP:   make(map[string]string),
	}

	rc.loadDHCPLeases()

	// Should only get the valid line
	if len(rc.leases) != 1 {
		t.Fatalf("expected 1 lease, got %d", len(rc.leases))
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"seconds", 45 * time.Second, "45s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours_and_minutes", 2*time.Hour + 30*time.Minute, "2h30m"},
		{"days_and_hours", 3*24*time.Hour + 5*time.Hour, "3d5h"},
		{"negative_seconds", -30 * time.Second, "30s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

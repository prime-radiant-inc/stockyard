// cmd/stockyard/resources_test.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

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

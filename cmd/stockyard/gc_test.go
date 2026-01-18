// cmd/stockyard/gc_test.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/obra/stockyard/pkg/config"
)

func TestGarbageCollector_FindOrphanVMDirs_Empty(t *testing.T) {
	tmpDir := t.TempDir()

	gc := &GarbageCollector{
		cfg:     config.DefaultConfig(),
		vmDir:   tmpDir,
		taskIDs: make(map[string]string),
	}

	gc.findOrphanVMDirs()

	if len(gc.toClean) != 0 {
		t.Errorf("expected 0 items, got %d", len(gc.toClean))
	}
}

func TestGarbageCollector_FindOrphanVMDirs_FindsOrphans(t *testing.T) {
	tmpDir := t.TempDir()

	// Create orphan VM directory
	os.Mkdir(filepath.Join(tmpDir, "orphan-vm"), 0755)

	gc := &GarbageCollector{
		cfg:     config.DefaultConfig(),
		vmDir:   tmpDir,
		taskIDs: make(map[string]string),
	}

	gc.findOrphanVMDirs()

	if len(gc.toClean) != 1 {
		t.Fatalf("expected 1 item, got %d", len(gc.toClean))
	}

	item := gc.toClean[0]
	if item.ID != "orphan-vm" {
		t.Errorf("expected ID 'orphan-vm', got %q", item.ID)
	}
	if item.Type != "vm-dir" {
		t.Errorf("expected Type 'vm-dir', got %q", item.Type)
	}
	if !item.IsOrphan {
		t.Error("expected IsOrphan to be true")
	}
}

func TestGarbageCollector_FindOrphanVMDirs_SkipsKnown(t *testing.T) {
	tmpDir := t.TempDir()

	// Create VM directories
	os.Mkdir(filepath.Join(tmpDir, "known-vm"), 0755)
	os.Mkdir(filepath.Join(tmpDir, "orphan-vm"), 0755)

	gc := &GarbageCollector{
		cfg:   config.DefaultConfig(),
		vmDir: tmpDir,
		taskIDs: map[string]string{
			"known-vm": "stopped",
		},
	}

	gc.findOrphanVMDirs()

	// Should only find the orphan, not the known one
	if len(gc.toClean) != 1 {
		t.Fatalf("expected 1 item, got %d", len(gc.toClean))
	}

	if gc.toClean[0].ID != "orphan-vm" {
		t.Errorf("expected 'orphan-vm', got %q", gc.toClean[0].ID)
	}
}

func TestGarbageCollector_FindOrphanVMDirs_SkipsRunning(t *testing.T) {
	tmpDir := t.TempDir()

	// Create orphan VM that's running (has pid file with running process - our own pid)
	vmDir := filepath.Join(tmpDir, "running-orphan")
	os.Mkdir(vmDir, 0755)
	os.WriteFile(filepath.Join(vmDir, "firecracker.pid"), []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	gc := &GarbageCollector{
		cfg:     config.DefaultConfig(),
		vmDir:   tmpDir,
		taskIDs: make(map[string]string),
	}

	gc.findOrphanVMDirs()

	// Should skip the running orphan
	if len(gc.toClean) != 0 {
		t.Errorf("expected 0 items (running VM should be skipped), got %d", len(gc.toClean))
	}
}

func TestGarbageCollector_FindResources_CleanAll(t *testing.T) {
	tmpDir := t.TempDir()

	gc := &GarbageCollector{
		cfg:      config.DefaultConfig(),
		vmDir:    tmpDir,
		cleanAll: true,
		taskIDs: map[string]string{
			"running-task": "running",
			"stopped-task": "stopped",
		},
	}

	gc.findResources()

	// Should only add stopped tasks, not running ones
	if len(gc.toClean) != 1 {
		t.Fatalf("expected 1 item, got %d", len(gc.toClean))
	}

	if gc.toClean[0].ID != "stopped-task" {
		t.Errorf("expected 'stopped-task', got %q", gc.toClean[0].ID)
	}
	if gc.toClean[0].Type != "task" {
		t.Errorf("expected type 'task', got %q", gc.toClean[0].Type)
	}
}

func TestGarbageCollector_CleanVMDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a VM directory with some files
	vmDir := filepath.Join(tmpDir, "test-vm")
	os.Mkdir(vmDir, 0755)
	os.WriteFile(filepath.Join(vmDir, "config.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(vmDir, "stdout.log"), []byte("logs"), 0644)

	gc := &GarbageCollector{
		cfg:   config.DefaultConfig(),
		vmDir: tmpDir,
	}

	item := CleanupItem{
		ID:   "test-vm",
		Type: "vm-dir",
		Path: vmDir,
	}

	err := gc.cleanVMDir(item)
	if err != nil {
		t.Fatalf("cleanVMDir failed: %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Error("expected VM directory to be removed")
	}
}

func TestGarbageCollector_CleanVMDir_WithTap(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a VM directory with tap_name file
	vmDir := filepath.Join(tmpDir, "test-vm")
	os.Mkdir(vmDir, 0755)
	os.WriteFile(filepath.Join(vmDir, "tap_name"), []byte("tap-nonexistent"), 0644)

	gc := &GarbageCollector{
		cfg:   config.DefaultConfig(),
		vmDir: tmpDir,
	}

	item := CleanupItem{
		ID:   "test-vm",
		Type: "vm-dir",
		Path: vmDir,
	}

	// Should not error even if tap doesn't exist
	err := gc.cleanVMDir(item)
	if err != nil {
		t.Fatalf("cleanVMDir failed: %v", err)
	}
}

func TestGCCommand_Help(t *testing.T) {
	if gcCmd.Use != "gc" {
		t.Errorf("expected Use 'gc', got %q", gcCmd.Use)
	}
	if gcCmd.Short == "" {
		t.Error("expected non-empty Short description")
	}
}

func TestGCCommand_Flags(t *testing.T) {
	// Verify flags are registered
	flags := gcCmd.Flags()

	if flags.Lookup("all") == nil {
		t.Error("expected --all flag to be registered")
	}
	if flags.Lookup("orphans") == nil {
		t.Error("expected --orphans flag to be registered")
	}
	if flags.Lookup("everything") == nil {
		t.Error("expected --everything flag to be registered")
	}
	if flags.Lookup("force") == nil {
		t.Error("expected --force flag to be registered")
	}
	if flags.Lookup("dry-run") == nil {
		t.Error("expected --dry-run flag to be registered")
	}
	if flags.Lookup("verbose") == nil {
		t.Error("expected --verbose flag to be registered")
	}
}

func TestGarbageCollector_BuildTapToTaskMap(t *testing.T) {
	tmpDir := t.TempDir()

	// Create VM directories with tap_name files
	vm1Dir := filepath.Join(tmpDir, "12345678-abcd-1234-5678-abcdef123456")
	os.Mkdir(vm1Dir, 0755)
	os.WriteFile(filepath.Join(vm1Dir, "tap_name"), []byte("tap-12345678\n"), 0644)

	vm2Dir := filepath.Join(tmpDir, "12345678-different-uuid-here")
	os.Mkdir(vm2Dir, 0755)
	os.WriteFile(filepath.Join(vm2Dir, "tap_name"), []byte("tap-87654321"), 0644)

	// VM without tap_name file
	vm3Dir := filepath.Join(tmpDir, "no-tap-file")
	os.Mkdir(vm3Dir, 0755)

	gc := &GarbageCollector{
		cfg:   config.DefaultConfig(),
		vmDir: tmpDir,
	}

	tapToTask := gc.buildTapToTaskMap()

	if len(tapToTask) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(tapToTask))
	}

	if taskID := tapToTask["tap-12345678"]; taskID != "12345678-abcd-1234-5678-abcdef123456" {
		t.Errorf("tap-12345678 should map to first UUID, got %q", taskID)
	}

	if taskID := tapToTask["tap-87654321"]; taskID != "12345678-different-uuid-here" {
		t.Errorf("tap-87654321 should map to second UUID, got %q", taskID)
	}
}

func TestGarbageCollector_BuildTapToTaskMap_DistinguishesSimilarPrefixes(t *testing.T) {
	// This test verifies that two task IDs with the same 8-char prefix
	// are correctly distinguished by their tap_name files
	tmpDir := t.TempDir()

	// Two UUIDs that share the same 8-character prefix
	vm1Dir := filepath.Join(tmpDir, "12345678-aaaa-1111-1111-111111111111")
	os.Mkdir(vm1Dir, 0755)
	os.WriteFile(filepath.Join(vm1Dir, "tap_name"), []byte("tap-12345678"), 0644)

	vm2Dir := filepath.Join(tmpDir, "12345678-bbbb-2222-2222-222222222222")
	os.Mkdir(vm2Dir, 0755)
	os.WriteFile(filepath.Join(vm2Dir, "tap_name"), []byte("tap-abcdef12"), 0644)

	gc := &GarbageCollector{
		cfg:   config.DefaultConfig(),
		vmDir: tmpDir,
	}

	tapToTask := gc.buildTapToTaskMap()

	// Each tap should map to exactly one task
	if len(tapToTask) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(tapToTask))
	}

	// tap-12345678 belongs to the first VM
	if taskID := tapToTask["tap-12345678"]; taskID != "12345678-aaaa-1111-1111-111111111111" {
		t.Errorf("tap-12345678 should map to first UUID, got %q", taskID)
	}

	// tap-abcdef12 belongs to the second VM
	if taskID := tapToTask["tap-abcdef12"]; taskID != "12345678-bbbb-2222-2222-222222222222" {
		t.Errorf("tap-abcdef12 should map to second UUID, got %q", taskID)
	}
}

func TestCleanupItem_Types(t *testing.T) {
	// Verify all expected types are handled
	types := []string{"task", "vm-dir", "zfs-vm", "zfs-workspace", "tap"}

	for _, typ := range types {
		item := CleanupItem{Type: typ}
		if item.Type != typ {
			t.Errorf("type mismatch: got %q, want %q", item.Type, typ)
		}
	}
}

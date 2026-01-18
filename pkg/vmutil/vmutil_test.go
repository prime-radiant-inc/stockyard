// pkg/vmutil/vmutil_test.go
package vmutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIsVMRunning_NoPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	if IsVMRunning(tmpDir) {
		t.Error("expected false for missing pid file")
	}
}

func TestIsVMRunning_InvalidPid(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte("notanumber"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsVMRunning(tmpDir) {
		t.Error("expected false for invalid pid")
	}
}

func TestIsVMRunning_NonexistentPid(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte("999999999"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsVMRunning(tmpDir) {
		t.Error("expected false for nonexistent pid")
	}
}

func TestIsVMRunning_RunningProcess(t *testing.T) {
	tmpDir := t.TempDir()
	pidPath := filepath.Join(tmpDir, "firecracker.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsVMRunning(tmpDir) {
		t.Error("expected true for running process")
	}
}

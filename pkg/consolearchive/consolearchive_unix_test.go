//go:build unix

package consolearchive

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A non-regular console file (here a FIFO) must be skipped, not opened —
// opening a FIFO for archiving could block on a reader that never comes,
// which would violate the never-block-a-destroy contract.
func TestArchiveSkipsNonRegularConsoleFile(t *testing.T) {
	archiveDir := t.TempDir()
	a, logs := testArchiver(archiveDir)
	vmDir := filepath.Join(t.TempDir(), "stockyard", "abc12345")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(vmDir, "stdout.log"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err != nil {
		t.Fatalf("ArchiveVMDir: %v", err)
	}
	dirents, _ := os.ReadDir(archiveDir)
	if len(dirents) != 0 {
		t.Errorf("a non-regular console file must not be archived, got %v", dirents)
	}
	if !hasToken(logs, "console_archive_empty task=abc12345") {
		t.Errorf("expected empty token when only a non-regular file is present, logs: %v", *logs)
	}
}

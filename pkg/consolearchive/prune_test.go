package consolearchive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeEntry fabricates a completed archive entry with a given payload size
// and age. Entry names sort chronologically, matching the ArchiveVMDir
// naming scheme.
func makeEntry(t *testing.T, archiveDir, name string, size int, age time.Duration) {
	t.Helper()
	dir := filepath.Join(archiveDir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stdout.log"), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	mod := time.Now().Add(-age)
	if err := os.Chtimes(dir, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRemovesEntriesPastMaxAge(t *testing.T) {
	archiveDir := t.TempDir()
	a, logs := testArchiver(archiveDir) // MaxAge 24h
	makeEntry(t, archiveDir, "20260101T000000Z-old00001-1001", 10, 48*time.Hour)
	makeEntry(t, archiveDir, "20260709T000000Z-new00001-1002", 10, time.Hour)

	if err := a.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "20260101T000000Z-old00001-1001")); !os.IsNotExist(err) {
		t.Error("expired entry should be pruned")
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "20260709T000000Z-new00001-1002")); err != nil {
		t.Errorf("fresh entry should survive: %v", err)
	}
	if !hasToken(logs, "console_archive_pruned") {
		t.Errorf("missing pruned token, logs: %v", *logs)
	}
}

func TestPruneEnforcesByteBudgetOldestFirst(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)
	a.MaxTotalBytes = 250
	makeEntry(t, archiveDir, "20260701T000000Z-e1-1001", 100, 3*time.Hour)
	makeEntry(t, archiveDir, "20260702T000000Z-e2-1002", 100, 2*time.Hour)
	makeEntry(t, archiveDir, "20260703T000000Z-e3-1003", 100, time.Hour)

	if err := a.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "20260701T000000Z-e1-1001")); !os.IsNotExist(err) {
		t.Error("oldest entry should be pruned first")
	}
	for _, keep := range []string{"20260702T000000Z-e2-1002", "20260703T000000Z-e3-1003"} {
		if _, err := os.Stat(filepath.Join(archiveDir, keep)); err != nil {
			t.Errorf("%s should survive: %v", keep, err)
		}
	}
}

func TestPruneKeepsNewestEvenOverBudget(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)
	a.MaxTotalBytes = 50
	makeEntry(t, archiveDir, "20260701T000000Z-e1-1001", 100, 2*time.Hour)
	makeEntry(t, archiveDir, "20260702T000000Z-e2-1002", 100, time.Hour)

	if err := a.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "20260702T000000Z-e2-1002")); err != nil {
		t.Errorf("newest entry must never be pruned by the byte bound: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "20260701T000000Z-e1-1001")); !os.IsNotExist(err) {
		t.Error("older entry should be pruned")
	}
}

func TestPruneRemovesStaleStaging(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)
	stale := filepath.Join(archiveDir, ".staging-20260101T000000Z-dead-1001")
	fresh := filepath.Join(archiveDir, ".staging-20260710T000000Z-live-1002")
	for _, dir := range []string{stale, fresh} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if err := a.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale staging dir should be pruned")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh staging dir should survive: %v", err)
	}
}

func TestPruneIgnoresUnrelatedDirectories(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)
	a.MaxTotalBytes = 1

	unrelated := filepath.Join(archiveDir, "unrelated-service-data")
	makeEntry(t, archiveDir, filepath.Base(unrelated), 100, 48*time.Hour)
	if err := a.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated directory was pruned: %v", err)
	}
}

func TestPruneIgnoresMalformedStagingDirectories(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)
	malformed := filepath.Join(archiveDir, ".staging-unrelated")
	makeEntry(t, archiveDir, filepath.Base(malformed), 10, 2*time.Hour)
	if err := a.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if _, err := os.Stat(malformed); err != nil {
		t.Fatalf("malformed staging directory was pruned: %v", err)
	}
}

func TestPruneMissingDirIsNoop(t *testing.T) {
	a, _ := testArchiver(filepath.Join(t.TempDir(), "does-not-exist"))
	if err := a.Prune(); err != nil {
		t.Fatalf("Prune on a missing dir must be a no-op, got %v", err)
	}
}

func TestArchivePrunesInline(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir) // MaxAge 24h
	makeEntry(t, archiveDir, "20260101T000000Z-old00001-1001", 10, 48*time.Hour)
	vmDir := writeVMDir(t, "abc12345", map[string]string{"stdout.log": "x"})

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err != nil {
		t.Fatalf("ArchiveVMDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "20260101T000000Z-old00001-1001")); !os.IsNotExist(err) {
		t.Error("inline prune should remove the expired entry")
	}
	entryDir(t, archiveDir, "abc12345") // fresh entry present
}

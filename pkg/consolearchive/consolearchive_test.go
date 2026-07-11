package consolearchive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testArchiver returns an archiver rooted at dir with small bounds and a
// log capture instead of the process logger.
func testArchiver(dir string) (*Archiver, *[]string) {
	logs := &[]string{}
	return &Archiver{
		Dir:           dir,
		MaxTotalBytes: 1 << 20,
		MaxAge:        24 * time.Hour,
		MaxEntryBytes: 1 << 20,
		Logf: func(format string, args ...any) {
			*logs = append(*logs, fmt.Sprintf(format, args...))
		},
	}, logs
}

func writeVMDir(t *testing.T, id string, files map[string]string) string {
	t.Helper()
	vmDir := filepath.Join(t.TempDir(), "stockyard", id)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(vmDir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(vmDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return vmDir
}

// entryDir returns the single completed archive entry for id.
func entryDir(t *testing.T, archiveDir, id string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(archiveDir, "*-"+id+"-*"))
	var entries []string
	for _, m := range matches {
		if !strings.HasPrefix(filepath.Base(m), ".") {
			entries = append(entries, m)
		}
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one archive entry for %s, got %v", id, entries)
	}
	return entries[0]
}

func hasToken(logs *[]string, token string) bool {
	for _, l := range *logs {
		if strings.Contains(l, token) {
			return true
		}
	}
	return false
}

func TestArchiveCopiesBothConsoleFiles(t *testing.T) {
	archiveDir := t.TempDir()
	a, logs := testArchiver(archiveDir)
	vmDir := writeVMDir(t, "abc12345", map[string]string{
		"stdout.log": "kernel boot\n",
		"stderr.log": "warning\n",
	})

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err != nil {
		t.Fatalf("ArchiveVMDir: %v", err)
	}

	for name, want := range map[string]string{"stdout.log": "kernel boot\n", "stderr.log": "warning\n"} {
		source, err := os.ReadFile(filepath.Join(vmDir, name))
		if err != nil || string(source) != want {
			t.Errorf("source %s = %q, err %v; archive must not remove it", name, source, err)
		}
	}

	entry := entryDir(t, archiveDir, "abc12345")
	for name, want := range map[string]string{"stdout.log": "kernel boot\n", "stderr.log": "warning\n"} {
		data, err := os.ReadFile(filepath.Join(entry, name))
		if err != nil {
			t.Fatalf("read archived %s: %v", name, err)
		}
		if string(data) != want {
			t.Errorf("archived %s = %q, want %q", name, data, want)
		}
	}

	var meta struct {
		TaskID      string    `json:"task_id"`
		Namespace   string    `json:"namespace"`
		Source      string    `json:"source"`
		DestroyedAt time.Time `json:"destroyed_at"`
		Files       []struct {
			Name          string `json:"name"`
			Bytes         int64  `json:"bytes"`
			OriginalBytes int64  `json:"original_bytes"`
			Truncated     bool   `json:"truncated"`
		} `json:"files"`
	}
	data, err := os.ReadFile(filepath.Join(entry, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parse meta.json: %v", err)
	}
	if meta.TaskID != "abc12345" || meta.Namespace != "stockyard" || meta.Source != "delete_vm" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if meta.DestroyedAt.IsZero() {
		t.Error("meta must record the destroy time")
	}
	if len(meta.Files) != 2 {
		t.Fatalf("expected 2 files in meta, got %d", len(meta.Files))
	}

	if !hasToken(logs, "console_archive_ok task=abc12345") {
		t.Errorf("missing ok token, logs: %v", *logs)
	}
	staging, _ := filepath.Glob(filepath.Join(archiveDir, ".staging-*"))
	if len(staging) != 0 {
		t.Errorf("staging dirs left behind: %v", staging)
	}
}

func TestArchiveSameIDSameSecondCreatesDistinctEntries(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)

	for _, content := range []string{"first", "second"} {
		vmDir := writeVMDir(t, "abc12345", map[string]string{"stdout.log": content})
		if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err != nil {
			t.Fatalf("ArchiveVMDir: %v", err)
		}
	}

	matches, _ := filepath.Glob(filepath.Join(archiveDir, "*-abc12345-*"))
	if len(matches) != 2 {
		t.Fatalf("expected two distinct entries, got %v", matches)
	}
}

func TestArchiveLateFailureKeepsSourceLogs(t *testing.T) {
	original := consoleFiles
	consoleFiles = []string{"stdout.log", "nested/stderr.log"}
	t.Cleanup(func() { consoleFiles = original })

	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)
	vmDir := writeVMDir(t, "abc12345", map[string]string{
		"stdout.log":        "boot output",
		"nested/stderr.log": "failure trigger",
	})

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err == nil {
		t.Fatal("expected staging failure")
	}
	data, err := os.ReadFile(filepath.Join(vmDir, "stdout.log"))
	if err != nil || string(data) != "boot output" {
		t.Fatalf("source stdout.log lost after failure: %q, %v", data, err)
	}
}

func TestArchiveSingleFile(t *testing.T) {
	archiveDir := t.TempDir()
	a, _ := testArchiver(archiveDir)
	vmDir := writeVMDir(t, "abc12345", map[string]string{"stdout.log": "only stdout\n"})

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err != nil {
		t.Fatalf("ArchiveVMDir: %v", err)
	}
	entry := entryDir(t, archiveDir, "abc12345")
	if _, err := os.Stat(filepath.Join(entry, "stdout.log")); err != nil {
		t.Errorf("archived stdout.log missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(entry, "stderr.log")); !os.IsNotExist(err) {
		t.Error("stderr.log should not exist in the entry")
	}
}

func TestArchiveNoConsoleFiles(t *testing.T) {
	archiveDir := t.TempDir()
	a, logs := testArchiver(archiveDir)
	vmDir := writeVMDir(t, "abc12345", nil)

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err != nil {
		t.Fatalf("ArchiveVMDir: %v", err)
	}
	dirents, _ := os.ReadDir(archiveDir)
	if len(dirents) != 0 {
		t.Errorf("expected empty archive, got %v", dirents)
	}
	if !hasToken(logs, "console_archive_empty task=abc12345") {
		t.Errorf("missing empty token, logs: %v", *logs)
	}
}

func TestArchiveRejectsMalformedIDs(t *testing.T) {
	archiveDir := t.TempDir()
	a, logs := testArchiver(archiveDir)
	vmDir := writeVMDir(t, "abc12345", map[string]string{"stdout.log": "data"})

	for _, id := range []string{"", ".", "..", "../escape", "a/b", "a b", strings.Repeat("x", 65)} {
		if err := a.ArchiveVMDir(vmDir, id, "delete_vm"); err == nil {
			t.Errorf("id %q: expected error", id)
		}
	}
	dirents, _ := os.ReadDir(archiveDir)
	if len(dirents) != 0 {
		t.Errorf("malformed ids must write nothing inside the archive, got %v", dirents)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "stdout.log")); err != nil {
		t.Errorf("source file must be untouched: %v", err)
	}
	if !hasToken(logs, "console_archive_failed") {
		t.Errorf("missing failed token, logs: %v", *logs)
	}
}

func TestArchiveFailureLogsAndErrors(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Dir nested under a regular file: MkdirAll must fail.
	a, logs := testArchiver(filepath.Join(blocker, "archive"))
	vmDir := writeVMDir(t, "abc12345", map[string]string{"stdout.log": "data"})

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err == nil {
		t.Fatal("expected error when archive root cannot be created")
	}
	if !hasToken(logs, "console_archive_failed task=") {
		t.Errorf("missing failed token, logs: %v", *logs)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "stdout.log")); err != nil {
		t.Errorf("source file must survive a failed archive: %v", err)
	}
}

func TestArchiveTruncatesOversizedConsole(t *testing.T) {
	archiveDir := t.TempDir()
	a, logs := testArchiver(archiveDir)
	a.MaxEntryBytes = 1024 // head = 512, tail = 512

	content := make([]byte, 4096)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	vmDir := writeVMDir(t, "abc12345", map[string]string{"stdout.log": string(content)})

	if err := a.ArchiveVMDir(vmDir, "abc12345", "delete_vm"); err != nil {
		t.Fatalf("ArchiveVMDir: %v", err)
	}

	entry := entryDir(t, archiveDir, "abc12345")
	data, err := os.ReadFile(filepath.Join(entry, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.HasPrefix(s, string(content[:512])) {
		t.Error("archived file must start with the original head")
	}
	if !strings.HasSuffix(s, string(content[4096-512:])) {
		t.Error("archived file must end with the original tail")
	}
	if !strings.Contains(s, "--- console truncated: original 4096 bytes, kept first 512 and last 512 ---") {
		t.Error("missing truncation marker")
	}
	if !hasToken(logs, "truncated=true") {
		t.Errorf("ok token must record truncation, logs: %v", *logs)
	}
	// Oversized originals are copied, not moved; the caller removes them
	// with the VM directory.
	if _, err := os.Stat(filepath.Join(vmDir, "stdout.log")); err != nil {
		t.Errorf("oversized original should remain for the caller to remove: %v", err)
	}
}

package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/secrets"
)

func TestNewPrunesConsoleArchiveAtStartup(t *testing.T) {
	archiveDir := t.TempDir()
	stale := filepath.Join(archiveDir, ".staging-20000101T000000Z-dead01-1001")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Daemon.DataDir = t.TempDir()
	// Leave kernel/rootfs unset so no firecracker client (and no system
	// state dir) is created in the test environment.
	cfg.Firecracker.KernelPath = ""
	cfg.Firecracker.RootfsPath = ""
	cfg.ConsoleArchive.Enabled = true
	cfg.ConsoleArchive.Dir = archiveDir

	d, err := New(cfg, &secrets.MockProvider{Secrets: map[string]string{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = d.Stop() }()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("startup should prune the stale staging dir")
	}
}

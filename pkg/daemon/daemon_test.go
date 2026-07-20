package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/secrets"
)

func TestNewFailsWhenPersistedIPPoolStateCannotLoad(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Daemon.DataDir = t.TempDir()
	cfg.Firecracker.KernelPath = ""
	cfg.Firecracker.RootfsPath = ""
	if err := os.WriteFile(filepath.Join(cfg.Daemon.DataDir, "ip_pool.json"), []byte("not JSON"), 0644); err != nil {
		t.Fatalf("write malformed IP pool state: %v", err)
	}

	d, err := New(cfg, &secrets.MockProvider{Secrets: map[string]string{}})
	if err == nil {
		defer d.Stop()
		t.Fatal("New succeeded with malformed persisted IP pool state")
	}
}

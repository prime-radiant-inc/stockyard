// cmd/stockyard/init_test.go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/obra/stockyard/pkg/config"
)

func TestInitCommand_RequiresInstance(t *testing.T) {
	// Reset flag for this test
	initInstanceName = ""

	rootCmd.SetArgs([]string{"init"})
	err := rootCmd.Execute()

	if err == nil {
		t.Fatal("expected error when --instance not provided")
	}
}

func TestInitCommand_CreatesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Reset flag for this test
	initInstanceName = ""

	rootCmd.SetArgs([]string{"init", "--instance", "test-local"})
	err := rootCmd.Execute()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify config was created
	configPath := filepath.Join(tmpDir, "stockyard", "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config file not created")
	}

	// Load and verify config contents
	cfg, err := config.LoadFromDir(filepath.Join(tmpDir, "stockyard"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.InstanceID != "test-local" {
		t.Errorf("instance ID: got %q, want %q", cfg.InstanceID, "test-local")
	}

	if cfg.Secrets.Prefix != "test-local" {
		t.Errorf("secrets prefix: got %q, want %q", cfg.Secrets.Prefix, "test-local")
	}
}

func TestInitCommand_OverwritesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// Create initial config
	initialCfg := config.DefaultConfig()
	initialCfg.InstanceID = "old-instance"
	initialCfg.Secrets.Prefix = "old-instance"
	err := initialCfg.SaveToDir(filepath.Join(tmpDir, "stockyard"))
	if err != nil {
		t.Fatalf("failed to create initial config: %v", err)
	}

	// Reset flag for this test
	initInstanceName = ""

	rootCmd.SetArgs([]string{"init", "--instance", "new-instance"})
	err = rootCmd.Execute()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify config was overwritten
	cfg, err := config.LoadFromDir(filepath.Join(tmpDir, "stockyard"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.InstanceID != "new-instance" {
		t.Errorf("instance ID: got %q, want %q", cfg.InstanceID, "new-instance")
	}

	if cfg.Secrets.Prefix != "new-instance" {
		t.Errorf("secrets prefix: got %q, want %q", cfg.Secrets.Prefix, "new-instance")
	}
}

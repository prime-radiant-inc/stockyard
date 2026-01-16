package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_DefaultsWhenNoFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := LoadFromDir(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.InstanceID != "" {
		t.Errorf("expected empty instance ID, got %q", cfg.InstanceID)
	}

	if cfg.Secrets.Provider != "1password" {
		t.Errorf("expected default provider '1password', got %q", cfg.Secrets.Provider)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		InstanceID: "test-instance",
		Secrets: SecretsConfig{
			Provider: "1password",
			Vault:    "TestVault",
			Prefix:   "test-instance",
		},
	}

	err := cfg.SaveToDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	configPath := filepath.Join(tmpDir, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config file not created")
	}

	loaded, err := LoadFromDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if loaded.InstanceID != cfg.InstanceID {
		t.Errorf("instance ID mismatch: got %q, want %q", loaded.InstanceID, cfg.InstanceID)
	}
}

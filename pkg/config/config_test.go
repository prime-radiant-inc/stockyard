package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestConfig_HTTPDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HTTP.Enabled {
		t.Errorf("expected HTTP disabled by default, got %v", cfg.HTTP.Enabled)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("expected default addr :8080, got %s", cfg.HTTP.Addr)
	}
}

func TestConfig_DHCPDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Firecracker.VMSubnet != "10.0.100.0/24" {
		t.Errorf("expected VMSubnet '10.0.100.0/24', got %q", cfg.Firecracker.VMSubnet)
	}
	if cfg.Firecracker.VMGateway != "10.0.100.1" {
		t.Errorf("expected VMGateway '10.0.100.1', got %q", cfg.Firecracker.VMGateway)
	}
	if cfg.Firecracker.DHCPRangeStart != "10.0.100.2" {
		t.Errorf("expected DHCPRangeStart '10.0.100.2', got %q", cfg.Firecracker.DHCPRangeStart)
	}
	if cfg.Firecracker.DHCPRangeEnd != "10.0.100.254" {
		t.Errorf("expected DHCPRangeEnd '10.0.100.254', got %q", cfg.Firecracker.DHCPRangeEnd)
	}
	if cfg.Firecracker.DHCPLeaseTime != "12h" {
		t.Errorf("expected DHCPLeaseTime '12h', got %q", cfg.Firecracker.DHCPLeaseTime)
	}
}

func TestConfig_DataDirDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Daemon.DataDir != "/var/lib/stockyard" {
		t.Errorf("expected DataDir '/var/lib/stockyard', got %q", cfg.Daemon.DataDir)
	}
}

func TestConfig_VMDir(t *testing.T) {
	cfg := DefaultConfig()
	expected := "/var/lib/stockyard/vms/stockyard"
	if got := cfg.VMDir(); got != expected {
		t.Errorf("VMDir() = %q, want %q", got, expected)
	}

	// Test with custom DataDir
	cfg.Daemon.DataDir = "/custom/path"
	expected = "/custom/path/vms/stockyard"
	if got := cfg.VMDir(); got != expected {
		t.Errorf("VMDir() with custom DataDir = %q, want %q", got, expected)
	}
}

func TestConfig_DHCPLeaseFile(t *testing.T) {
	cfg := DefaultConfig()
	expected := "/var/lib/stockyard/dnsmasq.leases"
	if got := cfg.DHCPLeaseFile(); got != expected {
		t.Errorf("DHCPLeaseFile() = %q, want %q", got, expected)
	}

	// Test with custom DataDir
	cfg.Daemon.DataDir = "/custom/path"
	expected = "/custom/path/dnsmasq.leases"
	if got := cfg.DHCPLeaseFile(); got != expected {
		t.Errorf("DHCPLeaseFile() with custom DataDir = %q, want %q", got, expected)
	}
}

func TestConfig_GRPCAddrDefault(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Daemon.GRPCAddr != "" {
		t.Errorf("expected GRPCAddr to be empty by default, got %q", cfg.Daemon.GRPCAddr)
	}
}

func TestDefaultConfigConsoleArchive(t *testing.T) {
	ca := DefaultConfig().ConsoleArchive
	if ca.Dir != "/var/lib/stockyard/console-archive" {
		t.Errorf("dir = %q", ca.Dir)
	}
	if ca.MaxTotalBytes != 1<<30 {
		t.Errorf("max_total_bytes = %d", ca.MaxTotalBytes)
	}
	if ca.MaxAgeDays != 14 {
		t.Errorf("max_age_days = %d", ca.MaxAgeDays)
	}
	if ca.MaxEntryBytes != 64<<20 {
		t.Errorf("max_entry_bytes = %d", ca.MaxEntryBytes)
	}
	if ca.Enabled {
		t.Error("console archive should default to disabled (opt-in)")
	}
}

func TestConsoleArchiverFromConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ConsoleArchive.Enabled = true
	a := cfg.ConsoleArchiver()
	if a == nil {
		t.Fatal("expected an archiver when enabled with the default config")
	}
	if a.Dir != cfg.ConsoleArchive.Dir {
		t.Errorf("dir = %q", a.Dir)
	}
	if a.MaxAge != 14*24*time.Hour {
		t.Errorf("max age = %v", a.MaxAge)
	}
	if a.MaxTotalBytes != 1<<30 || a.MaxEntryBytes != 64<<20 {
		t.Errorf("bounds = %d/%d", a.MaxTotalBytes, a.MaxEntryBytes)
	}
}

func TestConsoleArchiverEnableGating(t *testing.T) {
	// Default (opt-in, not enabled) yields no archiver.
	if DefaultConfig().ConsoleArchiver() != nil {
		t.Error("default (not enabled) must yield a nil archiver")
	}
	// Enabled but no dir yields no archiver.
	cfg := DefaultConfig()
	cfg.ConsoleArchive.Enabled = true
	cfg.ConsoleArchive.Dir = ""
	if cfg.ConsoleArchiver() != nil {
		t.Error("empty dir must yield a nil archiver even when enabled")
	}
	// Enabled with a dir yields an archiver.
	cfg = DefaultConfig()
	cfg.ConsoleArchive.Enabled = true
	if cfg.ConsoleArchiver() == nil {
		t.Error("enabled with a dir must yield an archiver")
	}
}

func TestLoadFromDirPartialConsoleArchive(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"console_archive":{"enabled":true}}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ConsoleArchive.Enabled {
		t.Error("expected enabled=true from file")
	}
	if cfg.ConsoleArchive.Dir != "/var/lib/stockyard/console-archive" {
		t.Errorf("unset keys must keep defaults, dir = %q", cfg.ConsoleArchive.Dir)
	}
}

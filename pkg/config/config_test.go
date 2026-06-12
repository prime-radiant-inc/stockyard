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

	if cfg.Secrets.Vault != "Stockyard" {
		t.Errorf("expected default vault 'Stockyard', got %q", cfg.Secrets.Vault)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		InstanceID: "test-instance",
		Secrets: SecretsConfig{
			Vault:  "TestVault",
			Prefix: "test-instance",
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

// TestLoadConfig_IgnoresRemovedFields proves that a config.json written
// before secrets.provider and firecracker.vm_subnet were removed still
// loads: json.Unmarshal ignores unknown keys, and the keys vanish on the
// next Save(). This is a regression guard (it passes before and after the
// field removal); it exists so nobody adds DisallowUnknownFields and
// silently breaks every pre-PRI-2177 install.
func TestLoadConfig_IgnoresRemovedFields(t *testing.T) {
	tmpDir := t.TempDir()
	legacy := `{
  "instance_id": "legacy",
  "secrets": {"provider": "1password", "vault": "Stockyard", "prefix": "legacy"},
  "firecracker": {"vm_subnet": "10.0.100.0/24", "vm_gateway": "10.0.100.9"}
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(legacy), 0644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	cfg, err := LoadFromDir(tmpDir)
	if err != nil {
		t.Fatalf("loading a config with removed fields must succeed: %v", err)
	}
	if cfg.InstanceID != "legacy" {
		t.Errorf("instance ID: got %q, want %q", cfg.InstanceID, "legacy")
	}
	if cfg.Secrets.Prefix != "legacy" {
		t.Errorf("secrets prefix: got %q, want %q", cfg.Secrets.Prefix, "legacy")
	}
	if cfg.Firecracker.VMGateway != "10.0.100.9" {
		t.Errorf("vm_gateway: got %q, want %q", cfg.Firecracker.VMGateway, "10.0.100.9")
	}
}

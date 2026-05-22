package config

import "testing"

func TestDefaultConfig_AppleContainer(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AppleContainer.ContainerBin != "container" {
		t.Errorf("expected default ContainerBin %q, got %q", "container", cfg.AppleContainer.ContainerBin)
	}
}

func TestAppleContainerConfig_JSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Backend = "apple-container"
	cfg.AppleContainer.ContainerBin = "/opt/homebrew/bin/container"
	cfg.AppleContainer.Image = "stockyard-vm:container"
	if err := cfg.SaveToDir(dir); err != nil {
		t.Fatalf("SaveToDir: %v", err)
	}
	loaded, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	if loaded.AppleContainer.ContainerBin != "/opt/homebrew/bin/container" {
		t.Errorf("ContainerBin not round-tripped: %q", loaded.AppleContainer.ContainerBin)
	}
	if loaded.AppleContainer.Image != "stockyard-vm:container" {
		t.Errorf("Image not round-tripped: %q", loaded.AppleContainer.Image)
	}
}

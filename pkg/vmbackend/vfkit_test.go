//go:build darwin

package vmbackend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVfkitBackend_ImplementsInterface(t *testing.T) {
	var _ Backend = (*VfkitBackend)(nil)
}

func TestVfkitBackend_NilClose(t *testing.T) {
	b := NewVfkitBackend(VfkitConfig{})
	if err := b.Close(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVfkitBackend_BuildArgs(t *testing.T) {
	tmpDir := t.TempDir()

	b := &VfkitBackend{
		cfg: VfkitConfig{
			VfkitBin:   "/opt/homebrew/bin/vfkit",
			KernelPath: "/path/to/vmlinux",
			StateDir:   tmpDir,
		},
		procs: make(map[string]*vfkitProc),
	}

	args := b.buildArgs(&VMConfig{
		ID:         "test-vm",
		VCPU:       2,
		MemoryMB:   1024,
		RootfsPath: "/path/to/rootfs.img",
	}, tmpDir, "02:aa:bb:cc:dd:ee")

	joined := strings.Join(args, " ")

	// Should contain these
	mustContain := []string{
		"--cpus", "--memory", "--bootloader", "vmlinux",
		"virtio-blk", "virtio-net", "nat", "02:aa:bb:cc:dd:ee",
		"rootfs.img", "virtio-fs", "mountTag=stockyard",
	}
	for _, check := range mustContain {
		if !strings.Contains(joined, check) {
			t.Errorf("expected args to contain %q, got: %s", check, joined)
		}
	}

	// Should NOT contain these
	mustNotContain := []string{"--initrd", "--kernel-cmdline", "--cloud-init"}
	for _, check := range mustNotContain {
		if strings.Contains(joined, check) {
			t.Errorf("args should NOT contain %q, got: %s", check, joined)
		}
	}
}

func TestGenerateMAC(t *testing.T) {
	mac1 := generateMAC()
	mac2 := generateMAC()

	if mac1 == "" {
		t.Error("generateMAC returned empty string")
	}
	if mac1 == mac2 {
		t.Error("generateMAC returned duplicate MACs")
	}
	if mac1[:2] != "02" {
		t.Errorf("expected MAC starting with 02, got %s", mac1[:2])
	}
}

func TestWriteAuthorizedKeys(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &VMConfig{
		ID: "test-vm",
		SSHAuthorizedKeys: []string{
			"ssh-ed25519 AAAA... user@host",
			"ssh-rsa BBBB... other@host",
		},
	}

	if err := writeAuthorizedKeys(tmpDir, cfg); err != nil {
		t.Fatalf("writeAuthorizedKeys failed: %v", err)
	}

	// Check shared directory exists
	sharedDir := filepath.Join(tmpDir, "shared")
	info, err := os.Stat(sharedDir)
	if err != nil {
		t.Fatalf("shared dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("shared is not a directory")
	}

	// Check authorized_keys file
	data, err := os.ReadFile(filepath.Join(sharedDir, "authorized_keys"))
	if err != nil {
		t.Fatalf("failed to read authorized_keys: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "ssh-ed25519") {
		t.Error("authorized_keys missing first SSH key")
	}
	if !strings.Contains(content, "ssh-rsa") {
		t.Error("authorized_keys missing second SSH key")
	}
}

func TestWriteAuthorizedKeys_NoKeys(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &VMConfig{ID: "test-vm"}

	if err := writeAuthorizedKeys(tmpDir, cfg); err != nil {
		t.Fatalf("writeAuthorizedKeys failed: %v", err)
	}

	// shared dir should exist but no authorized_keys file
	sharedDir := filepath.Join(tmpDir, "shared")
	if _, err := os.Stat(sharedDir); err != nil {
		t.Fatal("shared dir should exist even with no keys")
	}
	if _, err := os.Stat(filepath.Join(sharedDir, "authorized_keys")); err == nil {
		t.Fatal("authorized_keys should not exist when no keys provided")
	}
}


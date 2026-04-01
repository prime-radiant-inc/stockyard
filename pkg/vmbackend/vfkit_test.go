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

	checks := []string{"--cpus", "--memory", "virtio-blk", "virtio-net", "nat", "02:aa:bb:cc:dd:ee", "vmlinux", "rootfs.img"}
	for _, check := range checks {
		if !strings.Contains(joined, check) {
			t.Errorf("expected args to contain %q, got: %s", check, joined)
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

func TestWriteCloudInit(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &VMConfig{
		ID: "test-vm",
		SSHAuthorizedKeys: []string{
			"ssh-ed25519 AAAA... user@host",
			"ssh-rsa BBBB... other@host",
		},
	}

	if err := writeCloudInit(tmpDir, cfg); err != nil {
		t.Fatalf("writeCloudInit failed: %v", err)
	}

	// Check meta-data
	metaData, err := os.ReadFile(filepath.Join(tmpDir, "meta-data"))
	if err != nil {
		t.Fatalf("failed to read meta-data: %v", err)
	}
	if !strings.Contains(string(metaData), "stockyard-test-vm") {
		t.Errorf("meta-data missing hostname, got: %s", metaData)
	}

	// Check user-data
	userData, err := os.ReadFile(filepath.Join(tmpDir, "user-data"))
	if err != nil {
		t.Fatalf("failed to read user-data: %v", err)
	}
	if !strings.Contains(string(userData), "#cloud-config") {
		t.Error("user-data missing #cloud-config header")
	}
	if !strings.Contains(string(userData), "ssh-ed25519") {
		t.Error("user-data missing SSH key")
	}
	if !strings.Contains(string(userData), "ssh-rsa") {
		t.Error("user-data missing second SSH key")
	}
}

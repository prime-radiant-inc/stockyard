//go:build darwin

package vmbackend

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	}, tmpDir, "02:aa:bb:cc:dd:ee", "192.168.64.5")

	joined := strings.Join(args, " ")

	// Should contain these
	mustContain := []string{
		"--cpus", "--memory", "--bootloader", "vmlinux",
		"virtio-blk", "virtio-net", "nat", "02:aa:bb:cc:dd:ee",
		"rootfs.img", "virtio-fs", "mountTag=stockyard",
		"ip=192.168.64.5",
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

func TestAllocateIP(t *testing.T) {
	// Reset the counter for this test
	old := atomic.LoadUint32(&nextIP)
	atomic.StoreUint32(&nextIP, 10)
	defer atomic.StoreUint32(&nextIP, old)

	ip1 := allocateIP()
	ip2 := allocateIP()
	ip3 := allocateIP()

	if ip1 != "192.168.64.10" {
		t.Errorf("expected 192.168.64.10, got %s", ip1)
	}
	if ip2 != "192.168.64.11" {
		t.Errorf("expected 192.168.64.11, got %s", ip2)
	}
	if ip3 != "192.168.64.12" {
		t.Errorf("expected 192.168.64.12, got %s", ip3)
	}
}

func TestAllocateIP_Concurrent(t *testing.T) {
	atomic.StoreUint32(&nextIP, 100)

	var wg sync.WaitGroup
	ips := make([]string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ips[idx] = allocateIP()
		}(i)
	}
	wg.Wait()

	// All IPs should be unique
	seen := make(map[string]bool)
	for _, ip := range ips {
		if seen[ip] {
			t.Errorf("duplicate IP allocated: %s", ip)
		}
		seen[ip] = true
		if !strings.HasPrefix(ip, "192.168.64.") {
			t.Errorf("IP not in expected range: %s", ip)
		}
	}
}

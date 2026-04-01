package rootfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyProvisioner_CloneAndDestroy(t *testing.T) {
	tmpDir := t.TempDir()
	baseImage := filepath.Join(tmpDir, "base.img")
	vmsDir := filepath.Join(tmpDir, "vms")

	if err := os.WriteFile(baseImage, []byte("fake rootfs content"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewCopyProvisioner(baseImage, vmsDir)

	path, err := p.Clone(context.Background(), "vm-001")
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read cloned rootfs: %v", err)
	}
	if string(data) != "fake rootfs content" {
		t.Errorf("content mismatch: got %q", string(data))
	}

	if err := p.Destroy(context.Background(), "vm-001"); err != nil {
		t.Fatalf("Destroy failed: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("expected VM directory to be removed")
	}
}

func TestCopyProvisioner_EnsureBase(t *testing.T) {
	tmpDir := t.TempDir()
	baseImage := filepath.Join(tmpDir, "base.img")

	p := NewCopyProvisioner(baseImage, tmpDir)

	if err := p.EnsureBase(context.Background()); err == nil {
		t.Error("expected error when base image missing")
	}

	os.WriteFile(baseImage, []byte("x"), 0644)

	if err := p.EnsureBase(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

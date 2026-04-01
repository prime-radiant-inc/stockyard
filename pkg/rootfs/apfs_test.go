//go:build darwin

package rootfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAPFSProvisioner_CloneAndDestroy(t *testing.T) {
	tmpDir := t.TempDir()
	baseImage := filepath.Join(tmpDir, "base.img")
	vmsDir := filepath.Join(tmpDir, "vms")

	if err := os.WriteFile(baseImage, []byte("fake rootfs content"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewAPFSProvisioner(baseImage, vmsDir)

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

func TestAPFSProvisioner_ClonefileIsCOW(t *testing.T) {
	tmpDir := t.TempDir()
	baseImage := filepath.Join(tmpDir, "base.img")
	vmsDir := filepath.Join(tmpDir, "vms")

	original := []byte("original content")
	if err := os.WriteFile(baseImage, original, 0644); err != nil {
		t.Fatal(err)
	}

	p := NewAPFSProvisioner(baseImage, vmsDir)

	path, err := p.Clone(context.Background(), "vm-cow")
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	if err := os.WriteFile(path, []byte("modified content"), 0644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(baseImage)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original content" {
		t.Error("original was modified — not a true clone")
	}
}

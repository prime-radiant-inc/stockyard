package rootfs

import (
	"context"
	"testing"
)

func TestZFSProvisioner_Clone_EmptyPool(t *testing.T) {
	p := NewZFSProvisioner("", "", "")
	_, err := p.Clone(context.Background(), "test-vm")
	if err == nil {
		t.Fatal("expected error with empty pool")
	}
}

func TestZFSProvisioner_Destroy_EmptyPool(t *testing.T) {
	p := NewZFSProvisioner("", "", "")
	err := p.Destroy(context.Background(), "test-vm")
	if err == nil {
		t.Fatal("expected error with empty pool")
	}
}

func TestZFSProvisioner_Fields(t *testing.T) {
	p := NewZFSProvisioner("tank", "stockyard/images", "stockyard/vms")
	zp := p.(*ZFSProvisioner)
	if zp.pool != "tank" {
		t.Errorf("expected pool 'tank', got %q", zp.pool)
	}
	if zp.imagesPath != "stockyard/images" {
		t.Errorf("expected imagesPath 'stockyard/images', got %q", zp.imagesPath)
	}
	if zp.vmsPath != "stockyard/vms" {
		t.Errorf("expected vmsPath 'stockyard/vms', got %q", zp.vmsPath)
	}
}

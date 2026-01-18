package network

import (
	"path/filepath"
	"testing"
)

func TestNewIPPool(t *testing.T) {
	pool, err := NewIPPool("10.0.100.0/24", "10.0.100.1")
	if err != nil {
		t.Fatalf("NewIPPool failed: %v", err)
	}
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
	// Should have 253 available IPs (.2 through .254, .1 is gateway)
	if pool.Available() != 253 {
		t.Errorf("expected 253 available IPs, got %d", pool.Available())
	}
}

func TestNewIPPoolFromGateway(t *testing.T) {
	// Test creating pool from just gateway (common case)
	pool, err := NewIPPoolFromGateway("10.0.100.1", 24)
	if err != nil {
		t.Fatalf("NewIPPoolFromGateway failed: %v", err)
	}
	if pool.Available() != 253 {
		t.Errorf("expected 253 available IPs, got %d", pool.Available())
	}
	if pool.Gateway() != "10.0.100.1" {
		t.Errorf("expected gateway 10.0.100.1, got %s", pool.Gateway())
	}
}

func TestIPPoolAllocate(t *testing.T) {
	pool, _ := NewIPPool("10.0.100.0/24", "10.0.100.1")

	ip1, err := pool.Allocate("vm-001")
	if err != nil {
		t.Fatalf("first allocation failed: %v", err)
	}
	if ip1 == "" {
		t.Fatal("expected non-empty IP")
	}

	// Same VM should get same IP
	ip1Again, err := pool.Allocate("vm-001")
	if err != nil {
		t.Fatalf("re-allocation failed: %v", err)
	}
	if ip1Again != ip1 {
		t.Errorf("expected same IP %s, got %s", ip1, ip1Again)
	}

	// Different VM should get different IP
	ip2, err := pool.Allocate("vm-002")
	if err != nil {
		t.Fatalf("second allocation failed: %v", err)
	}
	if ip2 == ip1 {
		t.Errorf("expected different IP, got same: %s", ip2)
	}
}

func TestIPPoolRelease(t *testing.T) {
	pool, _ := NewIPPool("10.0.100.0/24", "10.0.100.1")
	initialAvailable := pool.Available()

	ip, _ := pool.Allocate("vm-001")
	if pool.Available() != initialAvailable-1 {
		t.Error("available count should decrease after allocation")
	}

	pool.Release("vm-001")
	if pool.Available() != initialAvailable {
		t.Error("available count should restore after release")
	}

	// Released IP should be allocatable again
	ip2, _ := pool.Allocate("vm-002")
	if ip2 != ip {
		t.Logf("Note: released IP %s was reused as %s (pool may not guarantee order)", ip, ip2)
	}
}

func TestIPPoolPersistence(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "ip_pool.json")

	// Create pool and allocate some IPs
	pool1, _ := NewIPPool("10.0.100.0/24", "10.0.100.1")
	pool1.SetPersistPath(tempFile)
	ip1, _ := pool1.Allocate("vm-001")
	ip2, _ := pool1.Allocate("vm-002")

	// Create new pool from same file - should restore allocations
	pool2, _ := NewIPPool("10.0.100.0/24", "10.0.100.1")
	pool2.SetPersistPath(tempFile)
	if err := pool2.LoadState(); err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}

	// Same VMs should get same IPs
	ip1Again, _ := pool2.Allocate("vm-001")
	ip2Again, _ := pool2.Allocate("vm-002")
	if ip1Again != ip1 {
		t.Errorf("vm-001: expected %s, got %s", ip1, ip1Again)
	}
	if ip2Again != ip2 {
		t.Errorf("vm-002: expected %s, got %s", ip2, ip2Again)
	}
}

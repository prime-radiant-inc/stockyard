package network

import (
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

package network

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type failingPersistenceFile struct {
	persistenceFile
	failWrite bool
	failSync  bool
}

func (f failingPersistenceFile) Write(data []byte) (int, error) {
	if f.failWrite {
		return 0, errors.New("injected write failure")
	}
	return f.persistenceFile.Write(data)
}

func (f failingPersistenceFile) Sync() error {
	if f.failSync {
		return errors.New("injected sync failure")
	}
	return f.persistenceFile.Sync()
}

func persistenceFailure(stage string) persistenceOps {
	ops := defaultPersistenceOps()
	if stage == "mkdir" {
		ops.mkdirAll = func(string, os.FileMode) error {
			return errors.New("injected mkdir failure")
		}
	}
	if stage == "create-temp" {
		ops.createTemp = func(string, string) (persistenceFile, error) {
			return nil, errors.New("injected temp-file failure")
		}
	} else {
		ops.createTemp = func(dir, pattern string) (persistenceFile, error) {
			file, err := os.CreateTemp(dir, pattern)
			if err != nil {
				return nil, err
			}
			return failingPersistenceFile{
				persistenceFile: file,
				failWrite:       stage == "write",
				failSync:        stage == "file-sync",
			}, nil
		}
	}
	if stage == "rename" {
		ops.rename = func(string, string) error {
			return errors.New("injected rename failure")
		}
	}
	if stage == "open-parent" {
		ops.openDir = func(string) (persistenceFile, error) {
			return nil, errors.New("injected parent-directory open failure")
		}
	} else {
		ops.openDir = func(path string) (persistenceFile, error) {
			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			return failingPersistenceFile{
				persistenceFile: file,
				failSync:        stage == "parent-sync",
			}, nil
		}
	}
	return ops
}

func restartIPPool(t *testing.T, path string) *IPPool {
	t.Helper()
	pool, err := NewIPPool("10.0.100.0/24", "10.0.100.1")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}
	pool.SetPersistPath(path)
	if err := pool.LoadState(); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return pool
}

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

func TestIPPool_AllocatePersistenceWriteFailureLeavesMemoryUnchanged(t *testing.T) {
	pool, err := NewIPPool("10.0.100.0/24", "10.0.100.1")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}

	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, nil, 0644); err != nil {
		t.Fatalf("write blocked parent: %v", err)
	}
	pool.SetPersistPath(filepath.Join(blockedParent, "ip_pool.json"))
	available := pool.Available()

	if _, err := pool.Allocate("vm-001"); err == nil {
		t.Fatal("Allocate succeeded despite an unwritable persistence path")
	}
	if got := pool.GetAllocation("vm-001"); got != "" {
		t.Errorf("allocation after failed persistence = %q, want empty", got)
	}
	if got := pool.Available(); got != available {
		t.Errorf("available addresses after failed persistence = %d, want %d", got, available)
	}
}

func TestIPPool_AllocatePersistenceFailuresPreserveOwnership(t *testing.T) {
	stages := []struct {
		name            string
		proposedVisible bool
	}{
		{name: "mkdir"},
		{name: "create-temp"},
		{name: "write"},
		{name: "file-sync"},
		{name: "rename"},
		{name: "open-parent", proposedVisible: true},
		{name: "parent-sync", proposedVisible: true},
	}

	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ip_pool.json")
			pool := restartIPPool(t, path)
			if _, err := pool.Allocate("vm-existing"); err != nil {
				t.Fatalf("Allocate vm-existing: %v", err)
			}
			previousFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read previous persistence state: %v", err)
			}
			nextIP := pool.available[0]
			pool.persistence = persistenceFailure(stage.name)

			if _, err := pool.Allocate("vm-next"); err == nil {
				t.Fatal("Allocate succeeded despite injected persistence failure")
			}
			if got := pool.GetAllocation("vm-existing"); got == "" {
				t.Error("existing allocation was lost after failed persistence")
			}
			if got := pool.GetAllocation("vm-next"); got != "" {
				t.Errorf("new allocation after failed persistence = %q, want empty", got)
			}

			visibleFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read visible persistence state: %v", err)
			}
			if stage.proposedVisible {
				restarted := restartIPPool(t, path)
				if got := restarted.GetAllocation("vm-next"); got != nextIP {
					t.Errorf("restart allocation after post-rename failure = %q, want %q", got, nextIP)
				}
				if got, err := restarted.Allocate("vm-next"); err != nil || got != nextIP {
					t.Errorf("retry allocation after proposed restart state = %q, %v; want %q, nil", got, err, nextIP)
				}
			} else if string(visibleFile) != string(previousFile) {
				t.Error("pre-rename persistence failure changed the durable file")
			}

			pool.persistence = defaultPersistenceOps()
			if got, err := pool.Allocate("vm-next"); err != nil || got != nextIP {
				t.Errorf("retry allocation = %q, %v; want %q, nil", got, err, nextIP)
			}
		})
	}
}

func TestIPPool_ReleasePersistenceFailuresPreserveOwnership(t *testing.T) {
	stages := []struct {
		name            string
		proposedVisible bool
	}{
		{name: "mkdir"},
		{name: "create-temp"},
		{name: "write"},
		{name: "file-sync"},
		{name: "rename"},
		{name: "open-parent", proposedVisible: true},
		{name: "parent-sync", proposedVisible: true},
	}

	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ip_pool.json")
			pool := restartIPPool(t, path)
			ip, err := pool.Allocate("vm-existing")
			if err != nil {
				t.Fatalf("Allocate vm-existing: %v", err)
			}
			previousFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read previous persistence state: %v", err)
			}
			pool.persistence = persistenceFailure(stage.name)

			if err := pool.Release("vm-existing"); err == nil {
				t.Fatal("Release succeeded despite injected persistence failure")
			}
			if got := pool.GetAllocation("vm-existing"); got != ip {
				t.Errorf("allocation after failed release = %q, want %q", got, ip)
			}

			visibleFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read visible persistence state: %v", err)
			}
			if stage.proposedVisible {
				restarted := restartIPPool(t, path)
				if got := restarted.GetAllocation("vm-existing"); got != "" {
					t.Errorf("restart allocation after post-rename failure = %q, want empty", got)
				}
				if err := restarted.Release("vm-existing"); err != nil {
					t.Errorf("idempotent release after proposed restart state: %v", err)
				}
			} else if string(visibleFile) != string(previousFile) {
				t.Error("pre-rename persistence failure changed the durable file")
			}

			pool.persistence = defaultPersistenceOps()
			if err := pool.Release("vm-existing"); err != nil {
				t.Fatalf("retry release: %v", err)
			}
			if got, err := pool.Allocate("vm-retry"); err != nil || got == "" {
				t.Errorf("allocation after release retry = %q, %v; want a non-empty address and nil", got, err)
			}
		})
	}
}

func TestIPPool_ReleasePersistenceAbsentTaskIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_pool.json")
	pool := restartIPPool(t, path)
	if _, err := pool.Allocate("vm-existing"); err != nil {
		t.Fatalf("Allocate vm-existing: %v", err)
	}
	previousFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read previous persistence state: %v", err)
	}
	pool.persistence = persistenceFailure("write")

	if err := pool.Release("vm-absent"); err != nil {
		t.Fatalf("Release absent task: %v", err)
	}
	visibleFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read visible persistence state: %v", err)
	}
	if string(visibleFile) != string(previousFile) {
		t.Error("idempotent release changed the durable file")
	}
}

func TestIPPoolNetworkConfig(t *testing.T) {
	pool, _ := NewIPPool("10.0.100.0/24", "10.0.100.1")
	ip, _ := pool.Allocate("vm-001")

	cfg := pool.NetworkConfig("vm-001")
	if cfg == nil {
		t.Fatal("expected non-nil NetworkConfig")
	}
	if cfg.IP != ip {
		t.Errorf("expected IP %s, got %s", ip, cfg.IP)
	}
	if cfg.Gateway != "10.0.100.1" {
		t.Errorf("expected gateway 10.0.100.1, got %s", cfg.Gateway)
	}
	if cfg.Netmask != "255.255.255.0" {
		t.Errorf("expected netmask 255.255.255.0, got %s", cfg.Netmask)
	}

	// Non-existent VM should return nil
	if pool.NetworkConfig("no-such-vm") != nil {
		t.Error("expected nil for non-existent VM")
	}
}

func TestIPPoolKernelIPArgs(t *testing.T) {
	pool, _ := NewIPPool("10.0.100.0/24", "10.0.100.1")
	pool.Allocate("vm-001")

	args := pool.KernelIPArgs("vm-001")
	expected := "ip=10.0.100.2::10.0.100.1:255.255.255.0::eth0:off"
	if args != expected {
		t.Errorf("expected %q, got %q", expected, args)
	}

	// Non-existent VM should return empty
	if pool.KernelIPArgs("no-such-vm") != "" {
		t.Error("expected empty string for non-existent VM")
	}
}

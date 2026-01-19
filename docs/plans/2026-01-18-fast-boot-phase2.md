# Fast Boot Phase 2: Static IP and Tailscale Pre-Registration Implementation Plan

**Status: Completed (2025-01-19)**

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Reduce VM boot time from ~5.1s to <2s by eliminating DHCP wait (~3s) and shifting Tailscale registration to pre-boot.

**Result: 1.65s init time achieved (76% reduction from original 7s)**

**Architecture:** Allocate static IPs on the host before VM boot and pass via kernel command line (fastest method - IP available at kernel init). Pre-register Tailscale nodes on the host concurrently with ZFS clone, then inject state via MMDS. Both optimizations have graceful fallbacks to current behavior.

**Tech Stack:** Go, Firecracker API (boot args), Firecracker MMDS, bash, Tailscale CLI

---

## Part 1: Static IP Assignment

### Task 1: Create IP Pool Package

**Files:**
- Create: `pkg/network/ip_pool.go`
- Test: `pkg/network/ip_pool_test.go`

**Step 1: Write the failing test for IPPool creation**

```go
// pkg/network/ip_pool_test.go
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
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/network/... -run TestNewIPPool -v`
Expected: FAIL with "undefined: NewIPPool"

**Step 3: Write minimal implementation**

```go
// pkg/network/ip_pool.go
package network

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// IPPool manages a pool of IP addresses for VM allocation.
type IPPool struct {
	mu        sync.Mutex
	network   *net.IPNet
	gateway   string
	allocated map[string]string // vmID -> IP
	available []string          // available IPs
}

// NewIPPool creates an IP pool from a CIDR and gateway.
// The gateway IP is excluded from the pool.
func NewIPPool(cidr, gateway string) (*IPPool, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR: %w", err)
	}

	pool := &IPPool{
		network:   ipNet,
		gateway:   gateway,
		allocated: make(map[string]string),
		available: make([]string, 0),
	}

	// Generate available IPs (skip network address, gateway, and broadcast)
	pool.generateAvailableIPs()

	return pool, nil
}

// NewIPPoolFromGateway creates an IP pool from a gateway IP and prefix length.
// This is more robust than parsing CIDR from config strings.
func NewIPPoolFromGateway(gateway string, prefixLen int) (*IPPool, error) {
	gwIP := net.ParseIP(gateway)
	if gwIP == nil {
		return nil, fmt.Errorf("invalid gateway IP: %s", gateway)
	}
	gwIP = gwIP.To4()
	if gwIP == nil {
		return nil, fmt.Errorf("gateway must be IPv4: %s", gateway)
	}

	// Calculate network address from gateway
	mask := net.CIDRMask(prefixLen, 32)
	networkIP := gwIP.Mask(mask)

	ipNet := &net.IPNet{
		IP:   networkIP,
		Mask: mask,
	}

	pool := &IPPool{
		network:   ipNet,
		gateway:   gateway,
		allocated: make(map[string]string),
		available: make([]string, 0),
	}

	pool.generateAvailableIPs()
	return pool, nil
}

// generateAvailableIPs populates the available IP list.
func (p *IPPool) generateAvailableIPs() {
	ip := make(net.IP, 4)
	copy(ip, p.network.IP.To4())

	ones, bits := p.network.Mask.Size()
	hostBits := bits - ones
	maxHosts := (1 << hostBits) - 1

	for {
		// Increment IP
		for i := 3; i >= 0; i-- {
			ip[i]++
			if ip[i] != 0 {
				break
			}
		}

		// Check if still in network
		if !p.network.Contains(ip) {
			break
		}

		// Skip broadcast (last IP in range)
		ipInt := binary.BigEndian.Uint32(ip)
		baseInt := binary.BigEndian.Uint32(p.network.IP.To4())
		if ipInt-baseInt >= uint32(maxHosts) {
			break
		}

		ipStr := ip.String()
		if ipStr != p.gateway {
			p.available = append(p.available, ipStr)
		}
	}
}

// Available returns the number of available IPs.
func (p *IPPool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.available)
}

// Gateway returns the gateway IP.
func (p *IPPool) Gateway() string {
	return p.gateway
}

// Netmask returns the netmask in dotted-decimal notation.
func (p *IPPool) Netmask() string {
	mask := p.network.Mask
	if len(mask) == 4 {
		return fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
	}
	return mask.String()
}

// PrefixLen returns the CIDR prefix length.
func (p *IPPool) PrefixLen() int {
	ones, _ := p.network.Mask.Size()
	return ones
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/network/... -run TestNewIPPool -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/network/ip_pool.go pkg/network/ip_pool_test.go
git commit -m "$(cat <<'EOF'
feat(network): add IP pool for static VM IP allocation

Initial implementation of IPPool that generates available IPs
from a CIDR, excluding the gateway address. Includes robust
NewIPPoolFromGateway constructor.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Add IP Allocation, Release, and Persistence to IPPool

**Files:**
- Modify: `pkg/network/ip_pool.go`
- Modify: `pkg/network/ip_pool_test.go`

**Step 1: Write failing tests for Allocate, Release, and persistence**

```go
// Add to pkg/network/ip_pool_test.go

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
```

**Step 2: Run tests to verify they fail**

Run: `go test ./pkg/network/... -run "TestIPPoolAllocate|TestIPPoolRelease|TestIPPoolPersistence" -v`
Expected: FAIL

**Step 3: Add Allocate, Release, and persistence methods**

```go
// Add to pkg/network/ip_pool.go

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Add to IPPool struct
type IPPool struct {
	// ... existing fields ...
	persistPath string // Path to persist state (optional)
}

// persistedState is the JSON structure for saving/loading state.
type persistedState struct {
	Allocated map[string]string `json:"allocated"`
}

// SetPersistPath sets the file path for persisting allocation state.
func (p *IPPool) SetPersistPath(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.persistPath = path
}

// Allocate assigns an IP to a VM. If the VM already has an IP, returns it.
func (p *IPPool) Allocate(vmID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Return existing allocation if present
	if ip, ok := p.allocated[vmID]; ok {
		return ip, nil
	}

	if len(p.available) == 0 {
		return "", fmt.Errorf("IP pool exhausted")
	}

	ip := p.available[0]
	p.available = p.available[1:]
	p.allocated[vmID] = ip

	p.persistLocked()
	return ip, nil
}

// Release returns an IP to the pool.
func (p *IPPool) Release(vmID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ip, ok := p.allocated[vmID]; ok {
		delete(p.allocated, vmID)
		p.available = append(p.available, ip)
		p.persistLocked()
	}
}

// GetAllocation returns the IP allocated to a VM, or empty string if none.
func (p *IPPool) GetAllocation(vmID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.allocated[vmID]
}

// persistLocked saves state to disk. Caller must hold the lock.
func (p *IPPool) persistLocked() {
	if p.persistPath == "" {
		return
	}

	state := persistedState{Allocated: p.allocated}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return // Best effort
	}

	// Ensure directory exists
	os.MkdirAll(filepath.Dir(p.persistPath), 0755)
	os.WriteFile(p.persistPath, data, 0644)
}

// LoadState restores allocation state from disk.
func (p *IPPool) LoadState() error {
	if p.persistPath == "" {
		return nil
	}

	data, err := os.ReadFile(p.persistPath)
	if os.IsNotExist(err) {
		return nil // No state to load
	}
	if err != nil {
		return err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Restore allocations and remove from available
	for vmID, ip := range state.Allocated {
		p.allocated[vmID] = ip
		// Remove from available list
		for i, availIP := range p.available {
			if availIP == ip {
				p.available = append(p.available[:i], p.available[i+1:]...)
				break
			}
		}
	}

	return nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./pkg/network/... -run "TestIPPoolAllocate|TestIPPoolRelease|TestIPPoolPersistence" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/network/ip_pool.go pkg/network/ip_pool_test.go
git commit -m "$(cat <<'EOF'
feat(network): add Allocate, Release, and persistence to IPPool

VMs can now get static IPs from the pool. Allocations are
idempotent (same VM always gets same IP until released).
State is persisted to disk so allocations survive daemon restart.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Add NetworkConfig and KernelIPArgs to IPPool

**Files:**
- Modify: `pkg/network/ip_pool.go`
- Modify: `pkg/network/ip_pool_test.go`

**Step 1: Write failing test for NetworkConfig and KernelIPArgs**

```go
// Add to pkg/network/ip_pool_test.go

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
```

**Step 2: Run tests to verify they fail**

Run: `go test ./pkg/network/... -run "TestIPPoolNetworkConfig|TestIPPoolKernelIPArgs" -v`
Expected: FAIL

**Step 3: Add NetworkConfig type and methods**

```go
// Add to pkg/network/ip_pool.go

// StaticNetworkConfig contains network configuration for a VM.
type StaticNetworkConfig struct {
	IP      string `json:"ip"`
	Netmask string `json:"netmask"`
	Gateway string `json:"gateway"`
	DNS     string `json:"dns"`
}

// NetworkConfig returns the complete network configuration for a VM.
// Returns nil if the VM has no allocation.
func (p *IPPool) NetworkConfig(vmID string) *StaticNetworkConfig {
	p.mu.Lock()
	defer p.mu.Unlock()

	ip, ok := p.allocated[vmID]
	if !ok {
		return nil
	}

	return &StaticNetworkConfig{
		IP:      ip,
		Netmask: p.Netmask(),
		Gateway: p.gateway,
		DNS:     "8.8.8.8",
	}
}

// KernelIPArgs returns the kernel command line IP configuration string.
// Format: ip=<client-ip>::<gw-ip>:<netmask>::<device>:<autoconf>
// This configures the IP in the kernel before userspace starts.
// Returns empty string if VM has no allocation.
func (p *IPPool) KernelIPArgs(vmID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	ip, ok := p.allocated[vmID]
	if !ok {
		return ""
	}

	// Format: ip=<client-ip>:<server-ip>:<gw-ip>:<netmask>:<hostname>:<device>:<autoconf>
	// We leave server-ip and hostname empty
	return fmt.Sprintf("ip=%s::%s:%s::eth0:off", ip, p.gateway, p.Netmask())
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./pkg/network/... -run "TestIPPoolNetworkConfig|TestIPPoolKernelIPArgs" -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/network/ip_pool.go pkg/network/ip_pool_test.go
git commit -m "$(cat <<'EOF'
feat(network): add NetworkConfig and KernelIPArgs to IPPool

NetworkConfig returns structured network info for MMDS.
KernelIPArgs returns kernel boot parameter for static IP -
this is the fastest method as IP is available at kernel init.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Extend MMDS with Network Configuration

**Files:**
- Modify: `pkg/firecracker/cloudinit.go`
- Modify: `pkg/firecracker/cloudinit_test.go`

**Step 1: Write failing test for network config in MMDS**

```go
// Add to pkg/firecracker/cloudinit_test.go

func TestBuildMMDSDataWithNetworkConfig(t *testing.T) {
	metadata := MMDSMetadata{
		InstanceID: "i-test",
		Hostname:   "test-vm",
		NetworkConfig: &NetworkConfig{
			IP:      "10.0.100.50",
			Netmask: "255.255.255.0",
			Gateway: "10.0.100.1",
			DNS:     "8.8.8.8",
		},
	}

	data := BuildMMDSData(metadata)

	// Verify network-config is present
	latest, ok := data["latest"].(map[string]interface{})
	if !ok {
		t.Fatal("expected latest in MMDS")
	}
	metaData, ok := latest["meta-data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected meta-data in MMDS")
	}

	netCfg, ok := metaData["network-config"].(map[string]interface{})
	if !ok {
		t.Fatal("expected network-config in meta-data")
	}

	if netCfg["ip"] != "10.0.100.50" {
		t.Errorf("expected IP 10.0.100.50, got %v", netCfg["ip"])
	}
	if netCfg["gateway"] != "10.0.100.1" {
		t.Errorf("expected gateway 10.0.100.1, got %v", netCfg["gateway"])
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/firecracker/... -run TestBuildMMDSDataWithNetworkConfig -v`
Expected: FAIL

**Step 3: Add NetworkConfig to MMDSMetadata and update BuildMMDSData**

```go
// Modify pkg/firecracker/cloudinit.go

// NetworkConfig holds static IP configuration for MMDS.
type NetworkConfig struct {
	IP      string `json:"ip"`
	Netmask string `json:"netmask"`
	Gateway string `json:"gateway"`
	DNS     string `json:"dns"`
}

// MMDSMetadata holds metadata fields for MMDS.
type MMDSMetadata struct {
	InstanceID        string
	Hostname          string
	TailscaleAuthKey  string
	SSHAuthorizedKeys []string
	UserData          string
	NetworkConfig     *NetworkConfig // Static IP configuration (optional)
}

// BuildMMDSData constructs the MMDS data structure for cloud-init.
func BuildMMDSData(meta MMDSMetadata) map[string]interface{} {
	// Use map[string]interface{} to support nested objects
	metaData := map[string]interface{}{
		"instance-id":    meta.InstanceID,
		"local-hostname": meta.Hostname,
	}
	if meta.TailscaleAuthKey != "" {
		metaData["tailscale-auth-key"] = meta.TailscaleAuthKey
	}
	if len(meta.SSHAuthorizedKeys) > 0 {
		metaData["ssh-authorized-keys"] = strings.Join(meta.SSHAuthorizedKeys, "\n")
	}
	if meta.NetworkConfig != nil {
		metaData["network-config"] = map[string]interface{}{
			"ip":      meta.NetworkConfig.IP,
			"netmask": meta.NetworkConfig.Netmask,
			"gateway": meta.NetworkConfig.Gateway,
			"dns":     meta.NetworkConfig.DNS,
		}
	}

	return map[string]interface{}{
		"latest": map[string]interface{}{
			"meta-data": metaData,
			"user-data": meta.UserData,
		},
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/firecracker/... -run TestBuildMMDSDataWithNetworkConfig -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/firecracker/cloudinit.go pkg/firecracker/cloudinit_test.go
git commit -m "$(cat <<'EOF'
feat(firecracker): add NetworkConfig to MMDS metadata

MMDS can now include static IP configuration for VMs.
Changed meta-data to map[string]interface{} to support
nested network-config object.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Initialize IPPool in Daemon

**Files:**
- Modify: `pkg/daemon/daemon.go`

**Step 1: Add IPPool to Daemon struct and New()**

```go
// Modify pkg/daemon/daemon.go

import (
	// ... existing imports ...
	"path/filepath"
)

// Add to Daemon struct
type Daemon struct {
	// ... existing fields ...
	ipPool *network.IPPool
}

// In New() function, after dhcpServer creation, add:

// Initialize IP pool for static VM IPs
// Use the gateway and a /24 prefix (standard for VM networks)
ipPool, err := network.NewIPPoolFromGateway(cfg.Firecracker.VMGateway, 24)
if err != nil {
	return nil, fmt.Errorf("failed to create IP pool: %w", err)
}
// Persist allocations to survive daemon restarts
ipPool.SetPersistPath(filepath.Join(cfg.Daemon.DataDir, "ip_pool.json"))
if err := ipPool.LoadState(); err != nil {
	// Log warning but continue - fresh state is fine
	fmt.Printf("Warning: could not load IP pool state: %v\n", err)
}
d.ipPool = ipPool

// Add accessor method
func (d *Daemon) IPPool() *network.IPPool {
	return d.ipPool
}
```

**Step 2: Run existing daemon tests**

Run: `go test ./pkg/daemon/... -v`
Expected: PASS (or skip tests that require full daemon setup)

**Step 3: Commit**

```bash
git add pkg/daemon/daemon.go
git commit -m "$(cat <<'EOF'
feat(daemon): initialize IP pool for static VM IPs

Daemon now maintains an IP pool derived from the gateway config.
VMs will be allocated static IPs from this pool. State is
persisted to survive daemon restarts.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Add Static IP to VMConfig and Firecracker Boot Args

**Files:**
- Modify: `pkg/firecracker/types.go`
- Modify: `pkg/firecracker/client.go`

**Step 1: Add StaticIP field to VMConfig**

```go
// Modify pkg/firecracker/types.go

type VMConfig struct {
	// ... existing fields ...
	StaticIPArgs  string         // Kernel IP args (e.g., "ip=10.0.100.2::10.0.100.1:...")
	NetworkConfig *NetworkConfig // Network config for MMDS (optional)
}
```

**Step 2: Update CreateVM to use static IP in boot args**

```go
// Modify pkg/firecracker/client.go CreateVM function

// Find this line (around line 214):
bootArgs := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"

// Replace with:
bootArgs := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
if config.StaticIPArgs != "" {
	bootArgs += " " + config.StaticIPArgs
}

// Also update MMDS to include NetworkConfig (around line 252):
mmdsData := BuildMMDSData(MMDSMetadata{
	InstanceID:        "i-" + config.ID,
	Hostname:          hostname,
	TailscaleAuthKey:  config.TailscaleAuthKey,
	SSHAuthorizedKeys: config.SSHAuthorizedKeys,
	UserData:          config.CloudInitData,
	NetworkConfig:     config.NetworkConfig,
})
```

**Step 3: Update StartVM similarly**

```go
// Modify pkg/firecracker/client.go StartVM function (around line 539)

bootArgs := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
if config.StaticIPArgs != "" {
	bootArgs += " " + config.StaticIPArgs
}
```

**Step 4: Run tests**

Run: `go test ./pkg/firecracker/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/firecracker/types.go pkg/firecracker/client.go
git commit -m "$(cat <<'EOF'
feat(firecracker): add static IP to kernel boot args

VMs can now receive static IP via kernel command line parameter.
This is the fastest method - IP is configured at kernel init,
before systemd even starts. Eliminates entire DHCP wait (~3s).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Integrate Static IP into VM Creation

**Files:**
- Modify: `pkg/daemon/tasks.go`

**Step 1: Update CreateTask to allocate and pass static IP**

```go
// Modify pkg/daemon/tasks.go CreateTask function

// After generating taskID (around line 89), add IP allocation:

// Allocate static IP for the VM
var staticIPArgs string
var networkConfig *firecracker.NetworkConfig
if tm.daemon.IPPool() != nil {
	if _, err := tm.daemon.IPPool().Allocate(taskID); err != nil {
		log.Printf("Warning: could not allocate static IP: %v (falling back to DHCP)", err)
	} else {
		staticIPArgs = tm.daemon.IPPool().KernelIPArgs(taskID)
		cfg := tm.daemon.IPPool().NetworkConfig(taskID)
		if cfg != nil {
			networkConfig = &firecracker.NetworkConfig{
				IP:      cfg.IP,
				Netmask: cfg.Netmask,
				Gateway: cfg.Gateway,
				DNS:     cfg.DNS,
			}
		}
	}
}

// Update VMConfig to include static IP (around line 169):
vmCfg := &firecracker.VMConfig{
	ID:                taskID,
	Namespace:         "stockyard",
	VCPU:              req.CPUs,
	MemoryMB:          req.MemoryMB,
	CloudInitData:     cloudInitData,
	TailscaleAuthKey:  tailscaleAuthKey,
	SSHAuthorizedKeys: req.SSHAuthorizedKeys,
	StaticIPArgs:      staticIPArgs,    // Add this
	NetworkConfig:     networkConfig,   // Add this
	Metadata: map[string]string{
		"task-id":   taskID,
		"task-name": req.Name,
		"repo":      req.Repo,
		"ref":       req.Ref,
	},
}
```

**Step 2: Update DestroyTask to release IP**

```go
// Modify pkg/daemon/tasks.go DestroyTask function

// Add before "Delete task from database" (around line 410):
if tm.daemon.IPPool() != nil {
	tm.daemon.IPPool().Release(taskID)
}
```

**Step 3: Run tests**

Run: `go test ./pkg/daemon/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/daemon/tasks.go
git commit -m "$(cat <<'EOF'
feat(daemon): allocate static IPs for VMs via kernel args

VMs now receive pre-allocated static IPs via kernel command
line. The IP is allocated before VM creation and released on
destroy. Falls back to DHCP if IP allocation fails.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Update VM Init Script to Skip DHCP When Static IP Present

**Files:**
- Modify: `vm-image/init/stockyard-init.sh`

**Step 1: Update network phase to detect kernel-configured IP**

The kernel IP parameter configures eth0 before init even runs. Update the network check:

```bash
# Replace Phase 1 (Network) section with:

# ============================================================================
# Phase 1: Network (kernel may have configured static IP via boot args)
# ============================================================================
log_timing "Checking network status..."

# Check if kernel already configured eth0 (static IP via boot args)
if ip addr show eth0 2>/dev/null | grep -q "inet "; then
    CURRENT_IP=$(ip addr show eth0 | grep "inet " | awk '{print $2}' | cut -d/ -f1)
    log_timing "Network already configured (kernel): $CURRENT_IP"
else
    # Fallback: wait for DHCP (systemd-networkd)
    log_timing "No kernel IP, waiting for DHCP..."
    for i in {1..30}; do
        if ip addr show eth0 2>/dev/null | grep -q "inet "; then
            log_timing "Network configured via DHCP after $((i/10)).$((i%10))s"
            ip addr show eth0 | grep "inet "
            break
        fi
        sleep 0.1
    done
fi

# Set up DNS (systemd-resolved is disabled)
log_timing "Configuring DNS..."
rm -f /etc/resolv.conf 2>/dev/null || true
printf 'nameserver 8.8.8.8\nnameserver 8.8.4.4\n' > /etc/resolv.conf

# Ensure MMDS route exists
if ! ip route | grep -q "169.254.169.254"; then
    log_timing "Adding MMDS route..."
    ip route add 169.254.169.254/32 dev eth0 scope link 2>/dev/null || true
fi
```

**Step 2: Commit**

```bash
git add vm-image/init/stockyard-init.sh
git commit -m "$(cat <<'EOF'
feat(vm-image): detect kernel-configured static IP

stockyard-init.sh now checks if eth0 was configured by the
kernel via boot args. If so, skips DHCP wait entirely.
Falls back to DHCP if no kernel IP present.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Build and Test Static IP Feature

**Files:**
- None (testing only)

**Step 1: Build the VM image**

Run: `cd vm-image && ./build.sh`
Expected: Docker build succeeds

**Step 2: Convert to rootfs**

Run: `cd vm-image && sudo ./convert-to-rootfs.sh`
Expected: Rootfs and kernel extracted

**Step 3: Restart daemon with new image**

Run: `sudo systemctl restart stockyardd`

**Step 4: Create test VM and verify static IP**

```bash
time stockyard run --repo https://github.com/obra/stockyard --name test-static-ip
```

**Step 5: Verify VM has static IP from kernel**

```bash
stockyard attach test-static-ip
# Inside VM:
ip addr show eth0
cat /var/log/stockyard/init.log
# Should show "Network already configured (kernel): 10.0.100.X"
```

**Step 6: Record benchmark results**

Run 4 VMs and record times:
```bash
for i in 1 2 3 4; do
    time stockyard run --repo https://github.com/obra/stockyard --name "bench-$i"
    stockyard destroy "bench-$i"
done
```

Expected: ~3s improvement (from ~5s to ~2s init complete)

**Step 7: Commit any fixes needed**

---

## Part 2: Tailscale Pre-Registration

### Task 10: Create Tailscale Pre-Registration Package

**Files:**
- Create: `pkg/tailscale/preregister.go`
- Create: `pkg/tailscale/preregister_test.go`

**Step 1: Write failing test for PreRegistrar**

```go
// pkg/tailscale/preregister_test.go
package tailscale

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPreRegistrar(t *testing.T) {
	// Skip if no auth key available
	authKey := os.Getenv("TAILSCALE_AUTH_KEY")
	if authKey == "" {
		t.Skip("TAILSCALE_AUTH_KEY not set")
	}

	tempDir := t.TempDir()
	pr := NewPreRegistrar(authKey, tempDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	node, err := pr.PreRegister(ctx, "test-prereg-"+time.Now().Format("150405"))
	if err != nil {
		t.Fatalf("PreRegister failed: %v", err)
	}

	if node.Hostname == "" {
		t.Error("expected non-empty hostname")
	}
	if len(node.State) == 0 {
		t.Error("expected non-empty state")
	}
	if node.IP == "" {
		t.Error("expected non-empty IP")
	}

	t.Logf("Pre-registered node: hostname=%s ip=%s state_size=%d",
		node.Hostname, node.IP, len(node.State))
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/tailscale/... -run TestPreRegistrar -v`
Expected: FAIL with "undefined: NewPreRegistrar"

**Step 3: Write PreRegistrar implementation**

```go
// pkg/tailscale/preregister.go
package tailscale

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PreRegistrar handles pre-registering Tailscale nodes for VMs.
type PreRegistrar struct {
	authKey  string
	stateDir string
}

// PreRegisteredNode contains the result of pre-registration.
type PreRegisteredNode struct {
	Hostname string
	State    []byte
	IP       string
}

// NewPreRegistrar creates a new pre-registrar.
func NewPreRegistrar(authKey, stateDir string) *PreRegistrar {
	return &PreRegistrar{
		authKey:  authKey,
		stateDir: stateDir,
	}
}

// PreRegister creates a new Tailscale identity and registers it.
func (p *PreRegistrar) PreRegister(ctx context.Context, hostname string) (*PreRegisteredNode, error) {
	// Create isolated state directory
	nodeDir := filepath.Join(p.stateDir, hostname)
	if err := os.MkdirAll(nodeDir, 0700); err != nil {
		return nil, fmt.Errorf("create node dir: %w", err)
	}

	statePath := filepath.Join(nodeDir, "tailscaled.state")
	socketPath := filepath.Join(nodeDir, "tailscaled.sock")

	// Start tailscaled in userspace mode with isolated state
	tailscaled := exec.CommandContext(ctx,
		"tailscaled",
		"--state="+statePath,
		"--socket="+socketPath,
		"--tun=userspace-networking",
		"--statedir="+nodeDir,
	)
	tailscaled.Env = append(os.Environ(),
		"TS_NO_LOGS_NO_SUPPORT=true",
	)

	if err := tailscaled.Start(); err != nil {
		os.RemoveAll(nodeDir)
		return nil, fmt.Errorf("start tailscaled: %w", err)
	}

	// Ensure cleanup on any exit path
	cleanup := func() {
		if tailscaled.Process != nil {
			tailscaled.Process.Kill()
			tailscaled.Wait()
		}
		os.RemoveAll(nodeDir)
	}

	// Wait for socket
	if err := waitForSocket(ctx, socketPath, 15*time.Second); err != nil {
		cleanup()
		return nil, fmt.Errorf("wait for socket: %w", err)
	}

	// Run tailscale up
	up := exec.CommandContext(ctx,
		"tailscale",
		"--socket="+socketPath,
		"up",
		"--authkey="+p.authKey,
		"--hostname="+hostname,
		"--accept-routes",
		"--ssh",
		"--timeout=30s",
	)

	if output, err := up.CombinedOutput(); err != nil {
		cleanup()
		return nil, fmt.Errorf("tailscale up: %w: %s", err, output)
	}

	// Get assigned IP
	ipCmd := exec.CommandContext(ctx,
		"tailscale",
		"--socket="+socketPath,
		"ip", "-4",
	)
	ipOutput, err := ipCmd.Output()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("get IP: %w", err)
	}
	ip := strings.TrimSpace(string(ipOutput))

	// Read the state file before cleanup
	state, err := os.ReadFile(statePath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("read state: %w", err)
	}

	cleanup()

	return &PreRegisteredNode{
		Hostname: hostname,
		State:    state,
		IP:       ip,
	}, nil
}

// waitForSocket waits for a Unix socket to become available.
func waitForSocket(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("socket not available after %v", timeout)
}
```

**Step 4: Run test to verify it passes (requires TAILSCALE_AUTH_KEY)**

Run: `TAILSCALE_AUTH_KEY="tskey-..." go test ./pkg/tailscale/... -run TestPreRegistrar -v`
Expected: PASS (or skip if no key)

**Step 5: Commit**

```bash
git add pkg/tailscale/preregister.go pkg/tailscale/preregister_test.go
git commit -m "$(cat <<'EOF'
feat(tailscale): add PreRegistrar for node pre-registration

PreRegistrar can create and register Tailscale nodes on the host
before VM boot. The resulting state can be injected into VMs via
MMDS for instant Tailscale connectivity.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: Extend MMDS with Tailscale State

**Files:**
- Modify: `pkg/firecracker/cloudinit.go`
- Modify: `pkg/firecracker/types.go`

**Step 1: Add TailscaleState to MMDSMetadata**

```go
// Modify pkg/firecracker/cloudinit.go

import (
	"encoding/base64"
	// ... other imports ...
)

// Add to MMDSMetadata struct:
type MMDSMetadata struct {
	InstanceID        string
	Hostname          string
	TailscaleAuthKey  string
	SSHAuthorizedKeys []string
	UserData          string
	NetworkConfig     *NetworkConfig
	TailscaleState    []byte // Pre-registered Tailscale state (optional)
}

// Update BuildMMDSData to include tailscale-state:
func BuildMMDSData(meta MMDSMetadata) map[string]interface{} {
	metaData := map[string]interface{}{
		"instance-id":    meta.InstanceID,
		"local-hostname": meta.Hostname,
	}
	if meta.TailscaleAuthKey != "" {
		metaData["tailscale-auth-key"] = meta.TailscaleAuthKey
	}
	if len(meta.SSHAuthorizedKeys) > 0 {
		metaData["ssh-authorized-keys"] = strings.Join(meta.SSHAuthorizedKeys, "\n")
	}
	if meta.NetworkConfig != nil {
		metaData["network-config"] = map[string]interface{}{
			"ip":      meta.NetworkConfig.IP,
			"netmask": meta.NetworkConfig.Netmask,
			"gateway": meta.NetworkConfig.Gateway,
			"dns":     meta.NetworkConfig.DNS,
		}
	}
	if len(meta.TailscaleState) > 0 {
		// Base64 encode for safe JSON transport
		metaData["tailscale-state"] = base64.StdEncoding.EncodeToString(meta.TailscaleState)
	}

	return map[string]interface{}{
		"latest": map[string]interface{}{
			"meta-data": metaData,
			"user-data": meta.UserData,
		},
	}
}
```

**Step 2: Add TailscaleState to VMConfig**

```go
// Modify pkg/firecracker/types.go

type VMConfig struct {
	// ... existing fields ...
	TailscaleState []byte // Pre-registered Tailscale state (optional)
}
```

**Step 3: Update CreateVM to pass TailscaleState**

```go
// Modify pkg/firecracker/client.go CreateVM function

// Update MMDS building (around line 252):
mmdsData := BuildMMDSData(MMDSMetadata{
	InstanceID:        "i-" + config.ID,
	Hostname:          hostname,
	TailscaleAuthKey:  config.TailscaleAuthKey,
	SSHAuthorizedKeys: config.SSHAuthorizedKeys,
	UserData:          config.CloudInitData,
	NetworkConfig:     config.NetworkConfig,
	TailscaleState:    config.TailscaleState,
})
```

**Step 4: Commit**

```bash
git add pkg/firecracker/cloudinit.go pkg/firecracker/types.go pkg/firecracker/client.go
git commit -m "$(cat <<'EOF'
feat(firecracker): add TailscaleState to MMDS

MMDS can now include pre-registered Tailscale state (base64).
VMs can use this to reconnect instantly instead of registering.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 12: Integrate Parallel Pre-Registration into VM Creation

**Files:**
- Modify: `pkg/daemon/tasks.go`

**Step 1: Add parallel pre-registration to CreateTask**

Pre-registration runs concurrently with ZFS clone to avoid adding latency:

```go
// Modify pkg/daemon/tasks.go CreateTask function

import (
	"sync"
	// ... other imports ...
)

// After getting tailscaleAuthKey (around line 145), add parallel pre-registration:

// Start Tailscale pre-registration in parallel with ZFS operations
var tailscaleState []byte
var preRegErr error
var preRegWg sync.WaitGroup

if tailscaleAuthKey != "" {
	preRegWg.Add(1)
	go func() {
		defer preRegWg.Done()
		preReg := tailscale.NewPreRegistrar(
			tailscaleAuthKey,
			filepath.Join(tm.daemon.cfg.Daemon.DataDir, "tailscale-prereg"),
		)

		preRegCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		node, err := preReg.PreRegister(preRegCtx, tailscaleHostname)
		if err != nil {
			preRegErr = err
			return
		}
		tailscaleState = node.State
		log.Printf("Pre-registered Tailscale node %s with IP %s", node.Hostname, node.IP)
	}()
}

// ... ZFS dataset creation happens here (existing code) ...

// Wait for pre-registration to complete before building VMConfig
preRegWg.Wait()
if preRegErr != nil {
	log.Printf("Warning: Tailscale pre-registration failed: %v (VM will register at boot)", preRegErr)
	// Continue without pre-registered state - VM will use auth key
}

// Update VMConfig to include TailscaleState (around line 169):
vmCfg := &firecracker.VMConfig{
	ID:                taskID,
	Namespace:         "stockyard",
	VCPU:              req.CPUs,
	MemoryMB:          req.MemoryMB,
	CloudInitData:     cloudInitData,
	TailscaleAuthKey:  tailscaleAuthKey,
	SSHAuthorizedKeys: req.SSHAuthorizedKeys,
	StaticIPArgs:      staticIPArgs,
	NetworkConfig:     networkConfig,
	TailscaleState:    tailscaleState, // Add this
	Metadata: map[string]string{
		"task-id":   taskID,
		"task-name": req.Name,
		"repo":      req.Repo,
		"ref":       req.Ref,
	},
}
```

**Step 2: Commit**

```bash
git add pkg/daemon/tasks.go
git commit -m "$(cat <<'EOF'
feat(daemon): pre-register Tailscale nodes in parallel

Pre-registration now runs concurrently with ZFS clone,
eliminating the latency it would add to stockyard run.
Falls back to in-VM registration if pre-reg fails.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: Update VM Init Script to Use Pre-Registered State

**Files:**
- Modify: `vm-image/init/stockyard-init.sh`

**Step 1: Replace Tailscale section with pre-registered state support**

Replace the entire Phase 3 (Tailscale setup) section with:

```bash
# ============================================================================
# Phase 3: Tailscale setup
# ============================================================================
log_timing "Starting Tailscale configuration..."

# Check for pre-registered Tailscale state first
TS_STATE_B64=$(curl -sf "${MMDS_URL}/meta-data/tailscale-state" 2>/dev/null | strip_json_quotes)

if [ -n "$TS_STATE_B64" ] && [ "$TS_STATE_B64" != "null" ]; then
    log_timing "Found pre-registered Tailscale state"

    # Decode and write state before starting tailscaled
    mkdir -p /var/lib/tailscale
    echo "$TS_STATE_B64" | base64 -d > /var/lib/tailscale/tailscaled.state
    chmod 600 /var/lib/tailscale/tailscaled.state

    # Start tailscaled (will use existing state)
    log_timing "Starting tailscaled with pre-registered state..."
    systemctl start tailscaled.service 2>&1 || log_timing "WARNING: tailscaled start failed"

    # Wait for reconnection (should be fast with existing state)
    reconnect_start=$(date +%s.%N)
    for i in {1..50}; do
        if tailscale status &>/dev/null; then
            elapsed=$(echo "$(date +%s.%N) - $reconnect_start" | bc)
            TS_IP=$(tailscale ip -4 2>/dev/null || echo "unknown")
            log_timing "Tailscale reconnected in ${elapsed}s: $TS_IP"
            break
        fi
        sleep 0.1
    done
else
    # Fall back to auth key registration
    TS_AUTH_KEY_RAW=$(curl -sf "${MMDS_URL}/meta-data/tailscale-auth-key" 2>/dev/null || echo "")
    TS_AUTH_KEY=$(echo "$TS_AUTH_KEY_RAW" | strip_json_quotes)

    if [ -n "$TS_AUTH_KEY" ]; then
        log_timing "Using auth key for Tailscale registration (${#TS_AUTH_KEY} chars)"

        TAILSCALE_SOCKET="/run/tailscale/tailscaled.sock"

        # Start tailscaled (DNS is already configured)
        log_timing "Starting tailscaled..."
        mkdir -p /run/tailscale /var/lib/tailscale

        # Dynamically choose TUN mode based on kernel support
        if [ -c /dev/net/tun ]; then
            log_timing "TUN device available, using native networking"
            sed -i 's/--tun=userspace-networking//' /etc/default/tailscaled 2>/dev/null || true
        else
            log_timing "TUN not available, using userspace networking"
        fi

        systemctl start tailscaled.service 2>&1 || log_timing "WARNING: tailscaled start failed"

        # Wait for socket
        tailscaled_ready=false
        wait_start=$(date +%s.%N)
        while true; do
            if [ -S "$TAILSCALE_SOCKET" ]; then
                elapsed=$(echo "$(date +%s.%N) - $wait_start" | bc)
                log_timing "tailscaled socket ready after ${elapsed}s"
                tailscaled_ready=true
                break
            fi
            elapsed=$(echo "$(date +%s.%N) - $wait_start" | bc)
            if [ "$(echo "$elapsed > 15" | bc)" -eq 1 ]; then
                break
            fi
            sleep 0.1
        done

        if [ "$tailscaled_ready" = true ]; then
            # Connect to Tailscale in BACKGROUND (non-blocking)
            log_timing "Starting Tailscale connection (background)..."
            (
                if tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh --timeout=30s 2>&1; then
                    TS_IP=$(tailscale ip -4 2>/dev/null || echo "unknown")
                    echo "[$(date +%s.%N)] Tailscale connected: $TS_IP" >> /var/log/stockyard/tailscale.log
                else
                    echo "[$(date +%s.%N)] Tailscale up failed" >> /var/log/stockyard/tailscale.log
                    journalctl -u tailscaled --no-pager -n 10 >> /var/log/stockyard/tailscale.log 2>&1 || true
                fi
            ) &
            log_timing "Tailscale connecting in background (PID: $!)"
        else
            log_timing "ERROR: tailscaled socket not ready after 15s"
            journalctl -u tailscaled --no-pager -n 10 2>&1 || true
        fi
    else
        log_timing "No Tailscale configuration found in MMDS"
    fi
fi
```

**Step 2: Commit**

```bash
git add vm-image/init/stockyard-init.sh
git commit -m "$(cat <<'EOF'
feat(vm-image): use pre-registered Tailscale state when available

stockyard-init.sh now checks for pre-registered state in MMDS
first, enabling instant reconnection (~0.3s vs 1.5s registration).
Falls back to auth key registration with background connection.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: Add Tailscale Cleanup on VM Destroy

**Files:**
- Modify: `pkg/tailscale/tailscale.go`
- Modify: `pkg/daemon/tasks.go`

**Step 1: Add device removal function (best-effort, relies on ephemeral keys)**

```go
// Add to pkg/tailscale/tailscale.go

import (
	"context"
	"log"
)

// RemoveDevice attempts to remove a device from the tailnet.
// This is best-effort - if it fails, ephemeral keys will expire the device.
// Note: Proper removal would require Tailscale API access, which we don't have.
// For now, we rely on using ephemeral auth keys that auto-expire.
func RemoveDevice(ctx context.Context, hostname string) error {
	// With ephemeral keys, devices are automatically removed when they
	// disconnect and the key expires. No action needed here.
	log.Printf("Tailscale device %s will be cleaned up by ephemeral key expiration", hostname)
	return nil
}
```

**Step 2: Update DestroyTask to log Tailscale cleanup**

```go
// Modify pkg/daemon/tasks.go DestroyTask function

// Add before IP release (around line 400):
if task.TailscaleHostname != "" {
	if err := tailscale.RemoveDevice(ctx, task.TailscaleHostname); err != nil {
		log.Printf("Warning: Tailscale cleanup for %s: %v", task.TailscaleHostname, err)
		// Don't fail - ephemeral keys handle cleanup
	}
}
```

**Step 3: Commit**

```bash
git add pkg/tailscale/tailscale.go pkg/daemon/tasks.go
git commit -m "$(cat <<'EOF'
feat(daemon): log Tailscale cleanup on VM destroy

Documents that Tailscale device cleanup relies on ephemeral
key expiration. Proper API-based removal could be added later
if needed.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: Build and Test Complete Phase 2

**Files:**
- None (testing only)

**Step 1: Rebuild VM image**

Run: `cd vm-image && ./build.sh && sudo ./convert-to-rootfs.sh`

**Step 2: Restart daemon**

Run: `sudo systemctl restart stockyardd`

**Step 3: Run comprehensive benchmark**

```bash
# Run 4 VMs and measure times
for i in 1 2 3 4; do
    echo "=== Run $i ==="
    START=$(date +%s.%N)
    stockyard run --repo https://github.com/obra/stockyard --name "bench-$i"
    END=$(date +%s.%N)
    echo "Total: $(echo "$END - $START" | bc)s"

    # Wait a moment for init to complete
    sleep 2

    # Check VM init log
    stockyard ssh "bench-$i" cat /var/log/stockyard/init.log 2>/dev/null || \
        echo "(SSH not yet available)"

    # Cleanup
    stockyard destroy "bench-$i"
done
```

**Step 4: Verify targets met**

Expected results:
- Init complete: <2s
- SSH available: <2.5s
- All VMs have static IPs (kernel-configured)
- Tailscale reconnects in <0.5s (with pre-registered state)

**Step 5: Document final results in research doc**

Update `docs/research/boot-time-optimization.md` with Phase 2 results.

**Step 6: Final commit**

```bash
git add docs/research/boot-time-optimization.md
git commit -m "$(cat <<'EOF'
docs: update boot-time-optimization with Phase 2 results

Records final benchmarks after implementing kernel-configured
static IP and Tailscale pre-registration optimizations.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
EOF
)"
```

---

## Execution Summary

**Part 1 (Static IP): Tasks 1-9**
- Creates IP pool package with allocation/release/persistence
- Uses kernel command line for static IP (fastest possible - configured before init)
- Extends MMDS with network configuration as backup
- Expected savings: ~3s (entire DHCP wait eliminated)

**Part 2 (Tailscale Pre-Registration): Tasks 10-15**
- Creates pre-registration package
- Runs pre-registration in parallel with ZFS clone (no added latency)
- Extends MMDS with Tailscale state
- Updates VM init with complete fallback to auth key flow
- Expected savings: ~1.2s in VM boot (pre-reg is parallel, not serial)

**Total expected result: ~5.1s → ~2.0s (60% reduction)**

**Key architectural decisions:**
1. **Kernel IP args** instead of early boot service - avoids chicken-and-egg with MMDS
2. **IP pool persistence** - survives daemon restarts, prevents conflicts
3. **Parallel pre-registration** - no added latency to `stockyard run`
4. **Robust gateway-based pool creation** - no fragile string parsing

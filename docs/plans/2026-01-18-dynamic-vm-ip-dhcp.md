# Dynamic VM IP Allocation via DHCP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace static IP configuration with DHCP-based dynamic IP allocation so multiple VMs can run simultaneously without IP conflicts.

**Architecture:** Stockyard daemon spawns dnsmasq as a child process, generating config from stockyard settings. VMs use standard DHCP (systemd-networkd) to obtain IPs. IP lookups go through the dnsmasq lease file.

**Tech Stack:** Go, dnsmasq, systemd-networkd, bash

**Spec:** See `docs/specs/dynamic-vm-ip-allocation.md` for full design details.

---

## Task 1: Add DHCP Config Fields

**Files:**
- Modify: `pkg/config/config.go:42-46`
- Test: `pkg/config/config_test.go`

**Step 1: Write the failing test**

Add to `pkg/config/config_test.go`:

```go
func TestConfig_DHCPDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Firecracker.VMSubnet != "192.168.64.0/18" {
		t.Errorf("expected default VMSubnet 192.168.64.0/18, got %s", cfg.Firecracker.VMSubnet)
	}
	if cfg.Firecracker.VMGateway != "192.168.64.1" {
		t.Errorf("expected default VMGateway 192.168.64.1, got %s", cfg.Firecracker.VMGateway)
	}
	if cfg.Firecracker.DHCPRangeStart != "192.168.64.2" {
		t.Errorf("expected default DHCPRangeStart 192.168.64.2, got %s", cfg.Firecracker.DHCPRangeStart)
	}
	if cfg.Firecracker.DHCPRangeEnd != "192.168.127.254" {
		t.Errorf("expected default DHCPRangeEnd 192.168.127.254, got %s", cfg.Firecracker.DHCPRangeEnd)
	}
	if cfg.Firecracker.DHCPLeaseTime != "12h" {
		t.Errorf("expected default DHCPLeaseTime 12h, got %s", cfg.Firecracker.DHCPLeaseTime)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/config/... -v -run TestConfig_DHCPDefaults`
Expected: FAIL - fields don't exist

**Step 3: Write minimal implementation**

Update `pkg/config/config.go` - change `FirecrackerConfig` struct:

```go
type FirecrackerConfig struct {
	KernelPath     string `json:"kernel_path"`
	RootfsPath     string `json:"rootfs_path"`
	BridgeName     string `json:"bridge_name"`
	VMSubnet       string `json:"vm_subnet"`
	VMGateway      string `json:"vm_gateway"`
	DHCPRangeStart string `json:"dhcp_range_start"`
	DHCPRangeEnd   string `json:"dhcp_range_end"`
	DHCPLeaseTime  string `json:"dhcp_lease_time"`
}
```

Update `DefaultConfig()` - change `Firecracker` section:

```go
Firecracker: FirecrackerConfig{
	KernelPath:     "/tmp/vmlinux.bin",
	RootfsPath:     "/var/lib/stockyard/rootfs.ext4",
	BridgeName:     "flbr0",
	VMSubnet:       "192.168.64.0/18",
	VMGateway:      "192.168.64.1",
	DHCPRangeStart: "192.168.64.2",
	DHCPRangeEnd:   "192.168.127.254",
	DHCPLeaseTime:  "12h",
},
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/config/... -v -run TestConfig_DHCPDefaults`
Expected: PASS

**Step 5: Run all config tests**

Run: `go test ./pkg/config/... -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add pkg/config/config.go pkg/config/config_test.go
git commit -m "feat(config): add DHCP configuration fields to FirecrackerConfig"
```

---

## Task 2: Create DHCP Server Package - Types and Constructor

**Files:**
- Create: `pkg/network/dhcp.go`
- Test: `pkg/network/dhcp_test.go`

**Step 1: Write the failing test**

Create `pkg/network/dhcp_test.go`:

```go
package network

import (
	"path/filepath"
	"testing"
)

func TestNewDHCPServer(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if srv.configPath != filepath.Join(dataDir, "dnsmasq.conf") {
		t.Errorf("unexpected configPath: %s", srv.configPath)
	}
	if srv.leasePath != filepath.Join(dataDir, "dnsmasq.leases") {
		t.Errorf("unexpected leasePath: %s", srv.leasePath)
	}
}

func TestNewDHCPServer_ValidationErrors(t *testing.T) {
	dataDir := t.TempDir()

	tests := []struct {
		name   string
		config DHCPConfig
	}{
		{"missing bridge", DHCPConfig{Gateway: "192.168.64.1", RangeStart: "192.168.64.2", RangeEnd: "192.168.127.254", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing gateway", DHCPConfig{Bridge: "flbr0", RangeStart: "192.168.64.2", RangeEnd: "192.168.127.254", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing range start", DHCPConfig{Bridge: "flbr0", Gateway: "192.168.64.1", RangeEnd: "192.168.127.254", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing range end", DHCPConfig{Bridge: "flbr0", Gateway: "192.168.64.1", RangeStart: "192.168.64.2", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing netmask", DHCPConfig{Bridge: "flbr0", Gateway: "192.168.64.1", RangeStart: "192.168.64.2", RangeEnd: "192.168.127.254", LeaseTime: "12h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDHCPServer(tt.config, dataDir)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/network/... -v`
Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `pkg/network/dhcp.go`:

```go
package network

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
)

// DHCPConfig holds configuration for the DHCP server.
type DHCPConfig struct {
	Bridge     string
	Gateway    string
	RangeStart string
	RangeEnd   string
	Netmask    string
	LeaseTime  string
	DNS        string
}

// DHCPServer manages a dnsmasq process for DHCP.
type DHCPServer struct {
	config     DHCPConfig
	configPath string
	leasePath  string
	logPath    string
	dataDir    string

	cmd *exec.Cmd
	mu  sync.Mutex
}

// NewDHCPServer creates a new DHCP server manager.
func NewDHCPServer(config DHCPConfig, dataDir string) (*DHCPServer, error) {
	if config.Bridge == "" {
		return nil, fmt.Errorf("bridge is required")
	}
	if config.Gateway == "" {
		return nil, fmt.Errorf("gateway is required")
	}
	if config.RangeStart == "" {
		return nil, fmt.Errorf("range start is required")
	}
	if config.RangeEnd == "" {
		return nil, fmt.Errorf("range end is required")
	}
	if config.Netmask == "" {
		return nil, fmt.Errorf("netmask is required")
	}
	if config.LeaseTime == "" {
		config.LeaseTime = "12h"
	}
	if config.DNS == "" {
		config.DNS = "8.8.8.8"
	}

	return &DHCPServer{
		config:     config,
		configPath: filepath.Join(dataDir, "dnsmasq.conf"),
		leasePath:  filepath.Join(dataDir, "dnsmasq.leases"),
		logPath:    filepath.Join(dataDir, "dnsmasq.log"),
		dataDir:    dataDir,
	}, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/network/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/network/dhcp.go pkg/network/dhcp_test.go
git commit -m "feat(network): add DHCPServer types and constructor"
```

---

## Task 3: DHCP Server - Config Generation

**Files:**
- Modify: `pkg/network/dhcp.go`
- Test: `pkg/network/dhcp_test.go`

**Step 1: Write the failing test**

Add to `pkg/network/dhcp_test.go`:

```go
func TestDHCPServer_WriteConfig(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := srv.WriteConfig(); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	// Read and verify config
	data, err := os.ReadFile(srv.configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	config := string(data)
	expectedLines := []string{
		"interface=flbr0",
		"bind-interfaces",
		"dhcp-range=192.168.64.2,192.168.127.254,255.255.192.0,12h",
		"dhcp-option=option:router,192.168.64.1",
		"dhcp-option=option:dns-server,8.8.8.8",
		"dhcp-authoritative",
	}

	for _, line := range expectedLines {
		if !strings.Contains(config, line) {
			t.Errorf("config missing expected line: %s", line)
		}
	}
}
```

Add import `"os"` and `"strings"` at the top of the test file.

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/network/... -v -run TestDHCPServer_WriteConfig`
Expected: FAIL - WriteConfig doesn't exist

**Step 3: Write minimal implementation**

Add to `pkg/network/dhcp.go`:

```go
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"text/template"
)

const dnsmasqConfigTemplate = `# Generated by stockyard - do not edit
interface={{.Bridge}}
bind-interfaces
dhcp-range={{.RangeStart}},{{.RangeEnd}},{{.Netmask}},{{.LeaseTime}}
dhcp-option=option:router,{{.Gateway}}
dhcp-option=option:dns-server,{{.DNS}}
dhcp-authoritative
dhcp-leasefile={{.LeasePath}}
log-dhcp
log-facility={{.LogPath}}
`

// WriteConfig generates the dnsmasq configuration file.
func (d *DHCPServer) WriteConfig() error {
	tmpl, err := template.New("dnsmasq").Parse(dnsmasqConfigTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	f, err := os.Create(d.configPath)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	data := struct {
		DHCPConfig
		LeasePath string
		LogPath   string
	}{
		DHCPConfig: d.config,
		LeasePath:  d.leasePath,
		LogPath:    d.logPath,
	}

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/network/... -v -run TestDHCPServer_WriteConfig`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/network/dhcp.go pkg/network/dhcp_test.go
git commit -m "feat(network): add DHCP config file generation"
```

---

## Task 4: DHCP Server - Start and Stop

**Files:**
- Modify: `pkg/network/dhcp.go`
- Test: `pkg/network/dhcp_test.go`

**Step 1: Write the failing test**

Add to `pkg/network/dhcp_test.go`:

```go
func TestDHCPServer_StartStop_NoBinary(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Use a non-existent binary path
	srv.SetBinaryPath("/nonexistent/dnsmasq")

	err = srv.Start()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestDHCPServer_IsRunning(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if srv.IsRunning() {
		t.Error("expected not running before start")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/network/... -v -run "TestDHCPServer_StartStop|TestDHCPServer_IsRunning"`
Expected: FAIL - methods don't exist

**Step 3: Write minimal implementation**

Add to `pkg/network/dhcp.go`:

```go
// Add binaryPath field to DHCPServer struct
type DHCPServer struct {
	config     DHCPConfig
	configPath string
	leasePath  string
	logPath    string
	dataDir    string
	binaryPath string

	cmd *exec.Cmd
	mu  sync.Mutex
}

// Update NewDHCPServer to set default binary path
func NewDHCPServer(config DHCPConfig, dataDir string) (*DHCPServer, error) {
	// ... existing validation ...

	return &DHCPServer{
		config:     config,
		configPath: filepath.Join(dataDir, "dnsmasq.conf"),
		leasePath:  filepath.Join(dataDir, "dnsmasq.leases"),
		logPath:    filepath.Join(dataDir, "dnsmasq.log"),
		dataDir:    dataDir,
		binaryPath: "dnsmasq", // Use PATH lookup by default
	}, nil
}

// SetBinaryPath sets a custom path for the dnsmasq binary.
func (d *DHCPServer) SetBinaryPath(path string) {
	d.binaryPath = path
}

// Start launches the dnsmasq process.
func (d *DHCPServer) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cmd != nil {
		return fmt.Errorf("DHCP server already running")
	}

	// Check if binary exists
	binaryPath, err := exec.LookPath(d.binaryPath)
	if err != nil {
		return fmt.Errorf("dnsmasq binary not found: %w", err)
	}

	// Write config file
	if err := d.WriteConfig(); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	// Create empty lease file if it doesn't exist
	if _, err := os.Stat(d.leasePath); os.IsNotExist(err) {
		if err := os.WriteFile(d.leasePath, []byte{}, 0644); err != nil {
			return fmt.Errorf("failed to create lease file: %w", err)
		}
	}

	d.cmd = exec.Command(binaryPath,
		"-k",                  // Keep in foreground
		"-C", d.configPath,    // Config file
		"--dhcp-leasefile", d.leasePath,
	)

	if err := d.cmd.Start(); err != nil {
		d.cmd = nil
		return fmt.Errorf("failed to start dnsmasq: %w", err)
	}

	return nil
}

// Stop terminates the dnsmasq process.
func (d *DHCPServer) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.cmd == nil || d.cmd.Process == nil {
		return nil
	}

	if err := d.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to kill dnsmasq: %w", err)
	}

	d.cmd.Wait()
	d.cmd = nil
	return nil
}

// IsRunning returns true if dnsmasq is running.
func (d *DHCPServer) IsRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cmd != nil && d.cmd.Process != nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/network/... -v -run "TestDHCPServer_StartStop|TestDHCPServer_IsRunning"`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/network/dhcp.go pkg/network/dhcp_test.go
git commit -m "feat(network): add DHCP server start/stop methods"
```

---

## Task 5: DHCP Server - Lease File Parsing

**Files:**
- Modify: `pkg/network/dhcp.go`
- Test: `pkg/network/dhcp_test.go`

**Step 1: Write the failing test**

Add to `pkg/network/dhcp_test.go`:

```go
func TestDHCPServer_GetIPForMAC(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Create a fake lease file
	// Format: <expiry> <MAC> <IP> <hostname> <client-id>
	leaseContent := `1737200000 02:7a:77:e8:87:9e 192.168.64.2 stockyard-abc123 *
1737200000 02:8d:3f:70:39:a9 192.168.64.3 stockyard-def456 *
`
	if err := os.WriteFile(srv.leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatalf("failed to write lease file: %v", err)
	}

	tests := []struct {
		mac      string
		wantIP   string
		wantFind bool
	}{
		{"02:7a:77:e8:87:9e", "192.168.64.2", true},
		{"02:8d:3f:70:39:a9", "192.168.64.3", true},
		{"02:00:00:00:00:00", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.mac, func(t *testing.T) {
			ip, found := srv.GetIPForMAC(tt.mac)
			if found != tt.wantFind {
				t.Errorf("GetIPForMAC(%s) found = %v, want %v", tt.mac, found, tt.wantFind)
			}
			if ip != tt.wantIP {
				t.Errorf("GetIPForMAC(%s) = %s, want %s", tt.mac, ip, tt.wantIP)
			}
		})
	}
}

func TestDHCPServer_GetIPForMAC_CaseInsensitive(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	leaseContent := `1737200000 02:7a:77:e8:87:9e 192.168.64.2 vm1 *
`
	if err := os.WriteFile(srv.leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatalf("failed to write lease file: %v", err)
	}

	// Test uppercase lookup
	ip, found := srv.GetIPForMAC("02:7A:77:E8:87:9E")
	if !found {
		t.Error("expected to find MAC with uppercase")
	}
	if ip != "192.168.64.2" {
		t.Errorf("got IP %s, want 192.168.64.2", ip)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/network/... -v -run "TestDHCPServer_GetIPForMAC"`
Expected: FAIL - GetIPForMAC doesn't exist

**Step 3: Write minimal implementation**

Add to `pkg/network/dhcp.go`:

```go
import (
	"bufio"
	// ... existing imports ...
	"strings"
)

// GetIPForMAC looks up an IP address by MAC address in the lease file.
func (d *DHCPServer) GetIPForMAC(mac string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	mac = strings.ToLower(mac)

	f, err := os.Open(d.leasePath)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Format: <expiry> <MAC> <IP> <hostname> <client-id>
		leaseMAC := strings.ToLower(fields[1])
		if leaseMAC == mac {
			return fields[2], true
		}
	}

	return "", false
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/network/... -v -run "TestDHCPServer_GetIPForMAC"`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/network/dhcp.go pkg/network/dhcp_test.go
git commit -m "feat(network): add DHCP lease file parsing for IP lookup"
```

---

## Task 6: DHCP Server - List All Leases

**Files:**
- Modify: `pkg/network/dhcp.go`
- Test: `pkg/network/dhcp_test.go`

**Step 1: Write the failing test**

Add to `pkg/network/dhcp_test.go`:

```go
func TestDHCPServer_ListLeases(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	leaseContent := `1737200000 02:7a:77:e8:87:9e 192.168.64.2 stockyard-abc123 *
1737200000 02:8d:3f:70:39:a9 192.168.64.3 stockyard-def456 *
`
	if err := os.WriteFile(srv.leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatalf("failed to write lease file: %v", err)
	}

	leases, err := srv.ListLeases()
	if err != nil {
		t.Fatalf("ListLeases failed: %v", err)
	}

	if len(leases) != 2 {
		t.Fatalf("expected 2 leases, got %d", len(leases))
	}

	if leases[0].MAC != "02:7a:77:e8:87:9e" {
		t.Errorf("unexpected MAC: %s", leases[0].MAC)
	}
	if leases[0].IP != "192.168.64.2" {
		t.Errorf("unexpected IP: %s", leases[0].IP)
	}
	if leases[0].Hostname != "stockyard-abc123" {
		t.Errorf("unexpected hostname: %s", leases[0].Hostname)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/network/... -v -run TestDHCPServer_ListLeases`
Expected: FAIL - ListLeases doesn't exist

**Step 3: Write minimal implementation**

Add to `pkg/network/dhcp.go`:

```go
import (
	"strconv"
	"time"
	// ... existing imports ...
)

// Lease represents a DHCP lease entry.
type Lease struct {
	Expiry   time.Time
	MAC      string
	IP       string
	Hostname string
	ClientID string
}

// ListLeases returns all current DHCP leases.
func (d *DHCPServer) ListLeases() ([]Lease, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	f, err := os.Open(d.leasePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to open lease file: %w", err)
	}
	defer f.Close()

	var leases []Lease
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		expiry, _ := strconv.ParseInt(fields[0], 10, 64)
		lease := Lease{
			Expiry:   time.Unix(expiry, 0),
			MAC:      fields[1],
			IP:       fields[2],
			Hostname: fields[3],
		}
		if len(fields) > 4 {
			lease.ClientID = fields[4]
		}
		leases = append(leases, lease)
	}

	return leases, scanner.Err()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/network/... -v -run TestDHCPServer_ListLeases`
Expected: PASS

**Step 5: Run all network tests**

Run: `go test ./pkg/network/... -v`
Expected: All PASS

**Step 6: Commit**

```bash
git add pkg/network/dhcp.go pkg/network/dhcp_test.go
git commit -m "feat(network): add DHCP lease listing"
```

---

## Task 7: Integrate DHCP Server into Daemon

**Files:**
- Modify: `pkg/daemon/daemon.go`

**Step 1: Read current daemon implementation**

Review `pkg/daemon/daemon.go` to understand structure.

**Step 2: Add DHCP server field and initialization**

Add import and field to `pkg/daemon/daemon.go`:

```go
import (
	// ... existing imports ...
	"github.com/obra/stockyard/pkg/network"
)

type Daemon struct {
	cfg       *config.Config
	secrets   secrets.Provider
	zfs       *zfs.Manager
	state     *State
	tasks     *TaskManager
	snapshots *SnapshotService
	dhcp      *network.DHCPServer  // NEW

	listener   net.Listener
	grpcServer *grpc.Server
	httpServer *http.Server
	mu         sync.Mutex
	running    bool
}
```

**Step 3: Initialize DHCP server in New()**

Update `New()` function in `pkg/daemon/daemon.go`:

```go
func New(cfg *config.Config, secretsProvider secrets.Provider) (*Daemon, error) {
	zfsMgr := zfs.NewManager(cfg.ZFS.Pool, cfg.ZFS.BasePath)

	state, err := NewState()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state: %w", err)
	}

	// Initialize DHCP server
	dhcpConfig := network.DHCPConfig{
		Bridge:     cfg.Firecracker.BridgeName,
		Gateway:    cfg.Firecracker.VMGateway,
		RangeStart: cfg.Firecracker.DHCPRangeStart,
		RangeEnd:   cfg.Firecracker.DHCPRangeEnd,
		Netmask:    "255.255.192.0", // /18
		LeaseTime:  cfg.Firecracker.DHCPLeaseTime,
		DNS:        "8.8.8.8",
	}
	dataDir := "/var/lib/stockyard"
	dhcpServer, err := network.NewDHCPServer(dhcpConfig, dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create DHCP server: %w", err)
	}

	d := &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		zfs:     zfsMgr,
		state:   state,
		dhcp:    dhcpServer,
	}

	// ... rest of initialization ...
}
```

**Step 4: Start DHCP server in Start()**

Add to `Start()` in `pkg/daemon/daemon.go`, after `ensureBaseImage`:

```go
// Start DHCP server
fmt.Println("Starting DHCP server...")
if err := d.dhcp.Start(); err != nil {
	// Log warning but don't fail - dnsmasq might not be installed
	fmt.Printf("Warning: Failed to start DHCP server: %v\n", err)
	fmt.Println("VMs may not receive dynamic IPs. Ensure dnsmasq is installed.")
}
```

**Step 5: Stop DHCP server in Stop()**

Add to `Stop()` in `pkg/daemon/daemon.go`, before closing state:

```go
// Stop DHCP server
if d.dhcp != nil {
	d.dhcp.Stop()
}
```

**Step 6: Add DHCP accessor method**

Add to `pkg/daemon/daemon.go`:

```go
// DHCP returns the daemon's DHCP server.
func (d *Daemon) DHCP() *network.DHCPServer {
	return d.dhcp
}
```

**Step 7: Run all tests**

Run: `go test ./pkg/... -v`
Expected: All PASS (or at least no new failures)

**Step 8: Commit**

```bash
git add pkg/daemon/daemon.go
git commit -m "feat(daemon): integrate DHCP server lifecycle"
```

---

## Task 8: Update VM Image - systemd-networkd DHCP Config

**Files:**
- Modify: `vm-image/Dockerfile:216-231`

**Step 1: Update the network configuration**

Replace the static IP configuration in `vm-image/Dockerfile`. Change:

```dockerfile
# Configure network for Firecracker MMDS access
# Static IP on 192.168.100.0/24 network, plus route to 169.254.169.254 for MMDS
# The IP will be set by cloud-init, but we need the MMDS route to be available early
RUN mkdir -p /etc/systemd/network \
    && cat > /etc/systemd/network/10-eth0.network <<'EOF'
[Match]
Name=eth0

[Network]
Address=192.168.100.2/24
Gateway=192.168.100.1
DNS=8.8.8.8

[Route]
Destination=169.254.169.254/32
Scope=link
EOF
# Enable systemd-networkd
RUN systemctl enable systemd-networkd 2>/dev/null || true
```

To:

```dockerfile
# Configure network for DHCP with MMDS route
# IP is obtained via DHCP from stockyard daemon's dnsmasq
# MMDS route is added for Firecracker metadata access
RUN mkdir -p /etc/systemd/network \
    && cat > /etc/systemd/network/10-eth0.network <<'EOF'
[Match]
Name=eth0

[Network]
DHCP=yes
DNS=8.8.8.8

[DHCP]
UseDNS=false
UseRoutes=true

[Route]
Destination=169.254.169.254/32
Scope=link
EOF
# Enable systemd-networkd
RUN systemctl enable systemd-networkd 2>/dev/null || true
```

**Step 2: Commit**

```bash
git add vm-image/Dockerfile
git commit -m "feat(vm-image): switch from static IP to DHCP"
```

---

## Task 9: Update Init Script - Wait for DHCP

**Files:**
- Modify: `vm-image/init/stockyard-init.sh:17-25`

**Step 1: Update network wait logic**

Replace the MMDS route wait in `vm-image/init/stockyard-init.sh`. Change:

```bash
# Wait for network interface
echo "Waiting for network..."
for i in {1..30}; do
    if ip route | grep -q "169.254.169.254"; then
        echo "MMDS route available"
        break
    fi
    sleep 1
done
```

To:

```bash
# Wait for DHCP to configure network
echo "Waiting for network (DHCP)..."
for i in {1..30}; do
    if ip addr show eth0 2>/dev/null | grep -q "inet "; then
        echo "Network configured via DHCP"
        ip addr show eth0 | grep "inet "
        break
    fi
    sleep 1
done

# Ensure MMDS route exists (systemd-networkd should add it, but verify)
if ! ip route | grep -q "169.254.169.254"; then
    echo "Adding MMDS route..."
    ip route add 169.254.169.254/32 dev eth0 scope link 2>/dev/null || true
fi

# Verify MMDS is reachable
for i in {1..10}; do
    if curl -sf "http://169.254.169.254/" &>/dev/null; then
        echo "MMDS is reachable"
        break
    fi
    sleep 1
done
```

**Step 2: Commit**

```bash
git add vm-image/init/stockyard-init.sh
git commit -m "feat(vm-image): update init script to wait for DHCP"
```

---

## Task 10: Add VM IP Lookup to Firecracker Client

**Files:**
- Modify: `pkg/firecracker/client.go` (or `pkg/daemon/tasks.go`)
- Modify: `pkg/daemon/daemon.go`

**Step 1: Add method to get VM's MAC address**

First, check where MAC addresses are stored. They should be in `/var/lib/stockyard/vms/<namespace>/<vmid>/mac_addr`.

Add to `pkg/daemon/tasks.go` or create a helper:

```go
// GetVMMAC reads the MAC address for a VM from its state directory.
func (tm *TaskManager) GetVMMAC(namespace, vmID string) (string, error) {
	macPath := filepath.Join("/var/lib/stockyard/vms", namespace, vmID, "mac_addr")
	data, err := os.ReadFile(macPath)
	if err != nil {
		return "", fmt.Errorf("failed to read MAC address: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// GetVMIP looks up a VM's IP address via DHCP leases.
func (tm *TaskManager) GetVMIP(namespace, vmID string) (string, error) {
	mac, err := tm.GetVMMAC(namespace, vmID)
	if err != nil {
		return "", err
	}

	if tm.daemon.DHCP() == nil {
		return "", fmt.Errorf("DHCP server not available")
	}

	ip, found := tm.daemon.DHCP().GetIPForMAC(mac)
	if !found {
		return "", fmt.Errorf("no DHCP lease found for MAC %s", mac)
	}
	return ip, nil
}
```

**Step 2: Commit**

```bash
git add pkg/daemon/tasks.go
git commit -m "feat(daemon): add VM IP lookup via DHCP leases"
```

---

## Task 11: Manual Integration Test

**Step 1: Build the daemon**

```bash
make build
```

**Step 2: Install dnsmasq**

```bash
sudo apt-get install -y dnsmasq
# Disable system dnsmasq service (stockyard manages its own)
sudo systemctl disable dnsmasq
sudo systemctl stop dnsmasq
```

**Step 3: Update host bridge network**

```bash
# Remove old IP (if present)
sudo ip addr del 192.168.100.1/24 dev flbr0 2>/dev/null || true

# Add new IP
sudo ip addr add 192.168.64.1/18 dev flbr0

# Update NAT rules
sudo iptables -t nat -D POSTROUTING -s 192.168.100.0/24 ! -o flbr0 -j MASQUERADE 2>/dev/null || true
sudo iptables -t nat -A POSTROUTING -s 192.168.64.0/18 ! -o flbr0 -j MASQUERADE
```

**Step 4: Rebuild VM image**

```bash
cd vm-image
make clean
make
# Import new rootfs
```

**Step 5: Start daemon and verify dnsmasq**

```bash
sudo ./bin/stockyardd
# In another terminal:
ps aux | grep dnsmasq
cat /var/lib/stockyard/dnsmasq.conf
```

**Step 6: Create VMs and verify unique IPs**

```bash
# Create two VMs
stockyard vm create test1
stockyard vm create test2

# Check leases
cat /var/lib/stockyard/dnsmasq.leases

# SSH to VMs and verify IPs
ssh <vm1-tailscale-name> ip addr show eth0
ssh <vm2-tailscale-name> ip addr show eth0
```

**Step 7: Document results**

Note any issues for follow-up.

---

## Task 12: Final Cleanup and Documentation

**Step 1: Run full test suite**

```bash
make test
```

**Step 2: Run linter**

```bash
make lint
```

**Step 3: Update spec status**

Change `docs/specs/dynamic-vm-ip-allocation.md` status from "Proposed" to "Implemented".

**Step 4: Final commit**

```bash
git add docs/specs/dynamic-vm-ip-allocation.md
git commit -m "docs: mark dynamic VM IP allocation spec as implemented"
```

---

## Summary

| Task | Description | Est. Time |
|------|-------------|-----------|
| 1 | Add DHCP config fields | 5 min |
| 2 | DHCP server types and constructor | 10 min |
| 3 | Config generation | 10 min |
| 4 | Start/stop methods | 10 min |
| 5 | Lease file parsing | 10 min |
| 6 | List all leases | 5 min |
| 7 | Daemon integration | 15 min |
| 8 | VM image DHCP config | 5 min |
| 9 | Init script DHCP wait | 5 min |
| 10 | VM IP lookup | 10 min |
| 11 | Manual integration test | 20 min |
| 12 | Cleanup and docs | 5 min |

**Total estimated time:** ~110 minutes

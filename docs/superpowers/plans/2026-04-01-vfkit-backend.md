# vfkit Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a macOS VM backend using vfkit so stockyard can run VMs on macOS via Apple's Virtualization.framework.

**Architecture:** New `pkg/vmbackend/vfkit.go` implements the existing `Backend` interface by spawning vfkit as a subprocess (one process per VM, same pattern as Firecracker). Uses NAT networking, cloud-init for SSH key injection, `/var/db/dhcpd_leases` for IP discovery. The rootfs is provisioned via the APFS `Provisioner` (already implemented in Phase 1).

**Tech Stack:** Go, vfkit CLI (`brew install vfkit`), Apple Virtualization.framework (indirect via vfkit)

**Prereq:** Phase 1 branch (`feature/vm-backend-interface`) must be merged or this builds on top of it.

---

## File Structure

### New files
- `pkg/vmbackend/vfkit.go` — vfkit Backend implementation (darwin build tag)
- `pkg/vmbackend/vfkit_test.go` — Tests (darwin build tag)
- `pkg/vmbackend/leases.go` — macOS DHCP lease file parser (darwin build tag)
- `pkg/vmbackend/leases_test.go` — Tests for lease parser
- `pkg/config/vfkit.go` — VfkitConfig type

### Modified files
- `pkg/config/config.go` — Add VfkitConfig to Config struct
- `pkg/daemon/daemon.go` — Wire up vfkit backend when `config.Backend == "vfkit"`
- `pkg/daemon/tasks.go` — Move `GenerateVMID` call to avoid Firecracker import on macOS

---

### Task 1: macOS DHCP lease file parser

**Files:**
- Create: `pkg/vmbackend/leases.go`
- Create: `pkg/vmbackend/leases_test.go`

This is a standalone parser for `/var/db/dhcpd_leases`. No build tag needed — it's pure string parsing, testable on any platform.

- [ ] **Step 1: Write the tests**

```go
// pkg/vmbackend/leases_test.go
package vmbackend

import (
	"os"
	"path/filepath"
	"testing"
)

const testLeaseData = `{
	name=vm-one
	ip_address=192.168.64.2
	hw_address=1,02:aa:bb:cc:dd:01
	identifier=1,02:aa:bb:cc:dd:01
	lease=0x67890001
}
{
	name=vm-two
	ip_address=192.168.64.3
	hw_address=1,02:aa:bb:cc:dd:02
	identifier=1,02:aa:bb:cc:dd:02
	lease=0x67890002
}
`

func TestParseLeaseFile_FindByMAC(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	ip, err := FindIPByMAC(leasePath, "02:aa:bb:cc:dd:02")
	if err != nil {
		t.Fatalf("FindIPByMAC failed: %v", err)
	}
	if ip != "192.168.64.3" {
		t.Errorf("expected 192.168.64.3, got %s", ip)
	}
}

func TestParseLeaseFile_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	_, err := FindIPByMAC(leasePath, "02:ff:ff:ff:ff:ff")
	if err == nil {
		t.Fatal("expected error for unknown MAC")
	}
}

func TestParseLeaseFile_CaseInsensitive(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	ip, err := FindIPByMAC(leasePath, "02:AA:BB:CC:DD:01")
	if err != nil {
		t.Fatalf("FindIPByMAC failed: %v", err)
	}
	if ip != "192.168.64.2" {
		t.Errorf("expected 192.168.64.2, got %s", ip)
	}
}

func TestParseLeaseFile_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(""), 0644)

	_, err := FindIPByMAC(leasePath, "02:aa:bb:cc:dd:01")
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestParseLeaseFile_MissingFile(t *testing.T) {
	_, err := FindIPByMAC("/nonexistent/path", "02:aa:bb:cc:dd:01")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/vmbackend/ -run TestParseLease -v`
Expected: FAIL — `FindIPByMAC` not defined

- [ ] **Step 3: Implement the lease parser**

```go
// pkg/vmbackend/leases.go
package vmbackend

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// FindIPByMAC parses a macOS /var/db/dhcpd_leases file and returns the IP
// address associated with the given MAC address. MAC comparison is case-insensitive.
func FindIPByMAC(leasePath, mac string) (string, error) {
	f, err := os.Open(leasePath)
	if err != nil {
		return "", fmt.Errorf("open lease file: %w", err)
	}
	defer f.Close()

	searchMAC := strings.ToLower(mac)

	var currentIP string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "ip_address=") {
			currentIP = strings.TrimPrefix(line, "ip_address=")
		}

		if strings.HasPrefix(line, "hw_address=") {
			// Format: "1,02:aa:bb:cc:dd:ee" — strip the hardware type prefix
			hwAddr := strings.TrimPrefix(line, "hw_address=")
			if idx := strings.Index(hwAddr, ","); idx >= 0 {
				hwAddr = hwAddr[idx+1:]
			}
			if strings.ToLower(hwAddr) == searchMAC && currentIP != "" {
				return currentIP, nil
			}
		}

		if line == "}" {
			currentIP = ""
		}
	}

	return "", fmt.Errorf("no lease found for MAC %s", mac)
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/vmbackend/ -run TestParseLease -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/leases.go pkg/vmbackend/leases_test.go
git commit -m "feat: add macOS DHCP lease file parser for VM IP discovery"
```

---

### Task 2: vfkit Backend implementation

**Files:**
- Create: `pkg/vmbackend/vfkit.go` (build tag: `darwin`)
- Create: `pkg/vmbackend/vfkit_test.go` (build tag: `darwin`)

This is the core implementation. It spawns vfkit as a subprocess and manages the VM lifecycle.

- [ ] **Step 1: Write the tests**

```go
//go:build darwin

// pkg/vmbackend/vfkit_test.go
package vmbackend

import (
	"os"
	"path/filepath"
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
	stateDir := filepath.Join(tmpDir, "state")
	os.MkdirAll(stateDir, 0755)

	b := &VfkitBackend{
		cfg: VfkitConfig{
			VfkitBin:   "/opt/homebrew/bin/vfkit",
			KernelPath: "/path/to/vmlinux",
			StateDir:   stateDir,
		},
		procs: make(map[string]*vfkitProc),
	}

	args := b.buildArgs("test-vm", &VMConfig{
		ID:       "test-vm",
		VCPU:     2,
		MemoryMB: 1024,
		RootfsPath: "/path/to/rootfs.img",
	}, stateDir)

	// Check key args are present
	found := map[string]bool{
		"--cpus":   false,
		"--memory": false,
	}
	for _, arg := range args {
		for key := range found {
			if arg == key {
				found[key] = true
			}
		}
	}
	for key, present := range found {
		if !present {
			t.Errorf("expected %s in args", key)
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
	// Should start with locally administered prefix
	if mac1[:2] != "02" {
		t.Errorf("expected MAC starting with 02, got %s", mac1[:2])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/vmbackend/ -run "TestVfkit|TestGenerateMAC" -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement vfkit backend**

```go
//go:build darwin

// pkg/vmbackend/vfkit.go
package vmbackend

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const macOSLeaseFile = "/var/db/dhcpd_leases"

// VfkitConfig holds configuration for the vfkit backend.
type VfkitConfig struct {
	VfkitBin   string // Path to vfkit binary (default: "vfkit")
	KernelPath string // Path to arm64 vmlinux
	StateDir   string // Where to store per-VM state
}

type vfkitProc struct {
	cmd    *exec.Cmd
	mac    string
	vmDir  string
}

// VfkitBackend manages VMs via vfkit subprocesses on macOS.
type VfkitBackend struct {
	cfg   VfkitConfig
	procs map[string]*vfkitProc
	mu    sync.Mutex
}

// NewVfkitBackend creates a new vfkit-based VM backend.
func NewVfkitBackend(cfg VfkitConfig) *VfkitBackend {
	if cfg.VfkitBin == "" {
		cfg.VfkitBin = "vfkit"
	}
	return &VfkitBackend{
		cfg:   cfg,
		procs: make(map[string]*vfkitProc),
	}
}

func (b *VfkitBackend) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	vmDir := filepath.Join(b.cfg.StateDir, cfg.ID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return nil, fmt.Errorf("create VM state dir: %w", err)
	}

	mac := generateMAC()

	// Write cloud-init files for SSH key injection
	if err := b.writeCloudInit(vmDir, cfg); err != nil {
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("write cloud-init: %w", err)
	}

	args := b.buildArgs(cfg.ID, cfg, vmDir)

	// Save MAC for IP discovery
	os.WriteFile(filepath.Join(vmDir, "mac_addr"), []byte(mac), 0644)

	cmd := exec.Command(b.cfg.VfkitBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutLog, _ := os.Create(filepath.Join(vmDir, "stdout.log"))
	stderrLog, _ := os.Create(filepath.Join(vmDir, "stderr.log"))
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog

	if err := cmd.Start(); err != nil {
		stdoutLog.Close()
		stderrLog.Close()
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("start vfkit: %w", err)
	}
	stdoutLog.Close()
	stderrLog.Close()

	// Save PID
	os.WriteFile(filepath.Join(vmDir, "vfkit.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0644)

	// Track process
	b.mu.Lock()
	b.procs[cfg.ID] = &vfkitProc{cmd: cmd, mac: mac, vmDir: vmDir}
	b.mu.Unlock()

	// Reaper goroutine
	go func() {
		cmd.Wait()
		b.mu.Lock()
		if p, ok := b.procs[cfg.ID]; ok && p.cmd == cmd {
			delete(b.procs, cfg.ID)
		}
		b.mu.Unlock()
	}()

	// Brief check for immediate failure
	time.Sleep(100 * time.Millisecond)
	if !processRunning(cmd.Process.Pid) {
		stderrContent, _ := os.ReadFile(filepath.Join(vmDir, "stderr.log"))
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("vfkit exited immediately: %s", string(stderrContent))
	}

	// Wait for VM to get an IP via DHCP
	ip, _ := b.waitForIP(mac, 30*time.Second)

	return &VMInfo{
		ID:        cfg.ID,
		PID:       cmd.Process.Pid,
		IP:        ip,
		StateDir:  vmDir,
		State:     "running",
		CreatedAt: time.Now(),
	}, nil
}

func (b *VfkitBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	// vfkit VMs are process-bound — "restart" means create a new process
	return b.CreateVM(ctx, cfg)
}

func (b *VfkitBackend) StopVM(ctx context.Context, id string) error {
	b.mu.Lock()
	proc, ok := b.procs[id]
	b.mu.Unlock()

	if !ok {
		return nil
	}

	// Try SIGTERM first, then SIGKILL
	if proc.cmd.Process != nil {
		proc.cmd.Process.Signal(syscall.SIGTERM)

		done := make(chan struct{})
		go func() {
			proc.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			proc.cmd.Process.Kill()
			<-done
		}
	}

	return nil
}

func (b *VfkitBackend) DeleteVM(ctx context.Context, id string) error {
	if err := b.StopVM(ctx, id); err != nil {
		return err
	}

	vmDir := filepath.Join(b.cfg.StateDir, id)
	return os.RemoveAll(vmDir)
}

func (b *VfkitBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	b.mu.Lock()
	proc, ok := b.procs[id]
	b.mu.Unlock()

	if !ok {
		// Check if state dir exists (VM was previously created)
		vmDir := filepath.Join(b.cfg.StateDir, id)
		if _, err := os.Stat(vmDir); err == nil {
			return &VMState{ID: id, Status: "stopped"}, nil
		}
		return nil, fmt.Errorf("VM not found: %s", id)
	}

	return &VMState{
		ID:     id,
		PID:    proc.cmd.Process.Pid,
		Status: "running",
	}, nil
}

func (b *VfkitBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var states []*VMState
	for id, proc := range b.procs {
		states = append(states, &VMState{
			ID:     id,
			PID:    proc.cmd.Process.Pid,
			Status: "running",
		})
	}
	return states, nil
}

func (b *VfkitBackend) Close() error {
	b.mu.Lock()
	ids := make([]string, 0, len(b.procs))
	for id := range b.procs {
		ids = append(ids, id)
	}
	b.mu.Unlock()

	for _, id := range ids {
		b.StopVM(context.Background(), id)
	}
	return nil
}

// buildArgs constructs the vfkit CLI arguments.
func (b *VfkitBackend) buildArgs(vmID string, cfg *VMConfig, vmDir string) []string {
	kernelPath := cfg.KernelPath
	if kernelPath == "" {
		kernelPath = b.cfg.KernelPath
	}

	rootfsPath := cfg.RootfsPath

	// Read MAC from saved file or generate
	mac := ""
	if data, err := os.ReadFile(filepath.Join(vmDir, "mac_addr")); err == nil {
		mac = strings.TrimSpace(string(data))
	} else {
		mac = generateMAC()
	}

	cmdline := fmt.Sprintf("\"console=hvc0 root=/dev/vda rw\"")

	args := []string{
		"--cpus", strconv.Itoa(int(cfg.VCPU)),
		"--memory", strconv.Itoa(int(cfg.MemoryMB)),
		"--bootloader", fmt.Sprintf("linux,kernel=%s,cmdline=%s", kernelPath, cmdline),
		"--device", fmt.Sprintf("virtio-blk,path=%s", rootfsPath),
		"--device", fmt.Sprintf("virtio-net,nat,mac=%s", mac),
		"--device", "virtio-rng",
		"--device", fmt.Sprintf("virtio-serial,logFilePath=%s", filepath.Join(vmDir, "console.log")),
		"--restful-uri", fmt.Sprintf("unix://%s", filepath.Join(vmDir, "vfkit-rest.sock")),
	}

	// Add cloud-init if user-data exists
	userDataPath := filepath.Join(vmDir, "user-data")
	metaDataPath := filepath.Join(vmDir, "meta-data")
	if _, err := os.Stat(userDataPath); err == nil {
		args = append(args, "--cloud-init", fmt.Sprintf("%s,%s", userDataPath, metaDataPath))
	}

	return args
}

// writeCloudInit creates cloud-init user-data and meta-data files for SSH key injection.
func (b *VfkitBackend) writeCloudInit(vmDir string, cfg *VMConfig) error {
	hostname := fmt.Sprintf("stockyard-%s", cfg.ID)

	// meta-data
	metaData := fmt.Sprintf("instance-id: i-%s\nlocal-hostname: %s\n", cfg.ID, hostname)
	if err := os.WriteFile(filepath.Join(vmDir, "meta-data"), []byte(metaData), 0644); err != nil {
		return err
	}

	// user-data
	var userData strings.Builder
	userData.WriteString("#cloud-config\n")

	if len(cfg.SSHAuthorizedKeys) > 0 {
		userData.WriteString("ssh_authorized_keys:\n")
		for _, key := range cfg.SSHAuthorizedKeys {
			userData.WriteString(fmt.Sprintf("  - %s\n", key))
		}
	}

	// If CloudInitData is provided (base64), write it as-is instead
	if cfg.CloudInitData != "" {
		// The cloud-init data is already base64-encoded; vfkit expects raw YAML
		// For now, use the SSH keys approach above. CloudInitData compatibility
		// can be added later if needed.
	}

	return os.WriteFile(filepath.Join(vmDir, "user-data"), []byte(userData.String()), 0644)
}

// waitForIP polls the macOS DHCP lease file until the VM's IP appears.
func (b *VfkitBackend) waitForIP(mac string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ip, err := FindIPByMAC(macOSLeaseFile, mac)
		if err == nil {
			return ip, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("timeout waiting for IP for MAC %s", mac)
}

// generateMAC creates a random locally-administered MAC address.
func generateMAC() string {
	buf := make([]byte, 5)
	rand.Read(buf)
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", buf[0], buf[1], buf[2], buf[3], buf[4])
}

// processRunning checks if a process is still alive.
func processRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/vmbackend/ -run "TestVfkit|TestGenerateMAC" -v`
Expected: PASS

- [ ] **Step 5: Run all vmbackend tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/vmbackend/ -v`
Expected: All PASS (including existing Firecracker adapter tests)

- [ ] **Step 6: Commit**

```bash
git add pkg/vmbackend/vfkit.go pkg/vmbackend/vfkit_test.go
git commit -m "feat: implement vfkit VM backend for macOS"
```

---

### Task 3: Add VfkitConfig and wire up in daemon

**Files:**
- Create: `pkg/config/vfkit.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/daemon/daemon.go`

- [ ] **Step 1: Create VfkitConfig type**

```go
// pkg/config/vfkit.go
package config

// VfkitConfig holds configuration for the vfkit VM backend (macOS).
type VfkitConfig struct {
	VfkitBin   string `json:"vfkit_bin"`   // Path to vfkit binary (default: "vfkit")
	KernelPath string `json:"kernel_path"` // Path to arm64 vmlinux kernel
	RootfsPath string `json:"rootfs_path"` // Path to base rootfs image
}
```

- [ ] **Step 2: Add VfkitConfig to Config struct**

In `pkg/config/config.go`, add to the `Config` struct (after `Firecracker`):

```go
type Config struct {
	InstanceID  string            `json:"instance_id"`
	Backend     string            `json:"backend"`
	Secrets     SecretsConfig     `json:"secrets"`
	Daemon      DaemonConfig      `json:"daemon"`
	ZFS         ZFSConfig         `json:"zfs"`
	Firecracker FirecrackerConfig `json:"firecracker"`
	Vfkit       VfkitConfig       `json:"vfkit"`
	VM          VMConfig          `json:"vm"`
	HTTP        HTTPConfig        `json:"http"`
	Rootfs      RootfsConfig      `json:"rootfs"`
}
```

- [ ] **Step 3: Wire up vfkit backend in daemon.go**

In `pkg/daemon/daemon.go`, update the backend selection block (around line 95-111). This file needs a build-tag-aware approach since `VfkitBackend` only exists on darwin. Create a helper file:

Create `pkg/daemon/backend_darwin.go`:
```go
//go:build darwin

package daemon

import (
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createVfkitBackend(cfg *config.Config) (vmbackend.Backend, error) {
	vfkitCfg := vmbackend.VfkitConfig{
		VfkitBin:   cfg.Vfkit.VfkitBin,
		KernelPath: cfg.Vfkit.KernelPath,
		StateDir:   cfg.Daemon.DataDir + "/vms/stockyard",
	}
	return vmbackend.NewVfkitBackend(vfkitCfg), nil
}
```

Create `pkg/daemon/backend_other.go`:
```go
//go:build !darwin

package daemon

import (
	"fmt"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createVfkitBackend(cfg *config.Config) (vmbackend.Backend, error) {
	return nil, fmt.Errorf("vfkit backend is only available on macOS")
}
```

Then in `daemon.go`, update the backend selection to add the vfkit case:

```go
	// Initialize task manager with VM backend
	var backend vmbackend.Backend
	switch cfg.Backend {
	case "", "firecracker":
		if cfg.Firecracker.KernelPath != "" && cfg.Firecracker.RootfsPath != "" {
			fcCfg := firecracker.ClientConfig{
				KernelPath: cfg.Firecracker.KernelPath,
				RootfsPath: cfg.Firecracker.RootfsPath,
				BridgeName: cfg.Firecracker.BridgeName,
				ImagesPath: cfg.ZFS.ImagesPath,
				VMsPath:    cfg.ZFS.VMsPath,
			}
			client, err := firecracker.NewClient(fcCfg, d.zfs)
			if err != nil {
				fmt.Printf("Warning: failed to create firecracker client: %v\n", err)
			} else {
				backend = vmbackend.NewFirecrackerBackend(client)
			}
		}
	case "vfkit":
		var err error
		backend, err = createVfkitBackend(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create vfkit backend: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown backend: %s", cfg.Backend)
	}
	d.tasks = NewTaskManager(d, backend)
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/... -count=1`
Expected: All PASS

- [ ] **Step 5: Build both binaries**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && make build`
Expected: Success

- [ ] **Step 6: Commit**

```bash
git add pkg/config/vfkit.go pkg/config/config.go pkg/daemon/daemon.go pkg/daemon/backend_darwin.go pkg/daemon/backend_other.go
git commit -m "feat: wire up vfkit backend in daemon with config support"
```

---

### Task 4: Wire up APFS rootfs provisioner for vfkit

**Files:**
- Modify: `pkg/daemon/daemon.go`
- Create: `pkg/daemon/rootfs_darwin.go`
- Create: `pkg/daemon/rootfs_other.go`
- Modify: `pkg/daemon/tasks.go`

When using the vfkit backend, the daemon needs to use the APFS provisioner to clone rootfs images before passing them to the backend. The Firecracker backend handles rootfs cloning internally (via ZFS), but the vfkit backend expects a pre-cloned `RootfsPath` in the VMConfig.

- [ ] **Step 1: Create rootfs provisioner factory with build tags**

```go
//go:build darwin

// pkg/daemon/rootfs_darwin.go
package daemon

import (
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/rootfs"
)

func createRootfsProvisioner(cfg *config.Config) rootfs.Provisioner {
	switch cfg.Rootfs.Provider {
	case "apfs":
		return rootfs.NewAPFSProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
	case "copy":
		return rootfs.NewCopyProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
	default:
		// Default to APFS on macOS
		if cfg.Rootfs.BaseImage != "" {
			return rootfs.NewAPFSProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
		}
		return nil
	}
}
```

```go
//go:build !darwin

// pkg/daemon/rootfs_other.go
package daemon

import (
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/rootfs"
)

func createRootfsProvisioner(cfg *config.Config) rootfs.Provisioner {
	if cfg.Rootfs.Provider == "copy" && cfg.Rootfs.BaseImage != "" {
		return rootfs.NewCopyProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
	}
	return nil
}
```

- [ ] **Step 2: Add provisioner to Daemon struct and wire it up**

In `daemon.go`, add to the Daemon struct:
```go
rootfsProvisioner rootfs.Provisioner
```

Add import for `rootfs` package. In `New()`, after backend creation:
```go
	// Initialize rootfs provisioner for non-ZFS backends
	d.rootfsProvisioner = createRootfsProvisioner(cfg)
```

Add accessor:
```go
func (d *Daemon) RootfsProvisioner() rootfs.Provisioner {
	return d.rootfsProvisioner
}
```

- [ ] **Step 3: Update CreateTask in tasks.go to use rootfs provisioner**

In `pkg/daemon/tasks.go`, in the `CreateTask` method, before the `if tm.backend != nil` block, add rootfs cloning for non-Firecracker backends:

```go
	// Clone rootfs for the VM (non-Firecracker backends need this pre-cloned)
	var rootfsPath string
	if tm.daemon.RootfsProvisioner() != nil {
		var err error
		rootfsPath, err = tm.daemon.RootfsProvisioner().Clone(ctx, taskID)
		if err != nil {
			if tm.daemon.IPPool() != nil {
				tm.daemon.IPPool().Release(taskID)
			}
			return nil, fmt.Errorf("failed to clone rootfs: %w", err)
		}
	}
```

Then in the VMConfig construction, set `RootfsPath: rootfsPath` if it was cloned. The Firecracker adapter already gets its rootfs path internally, so this only affects vfkit.

Also update `DestroyTask` to clean up via the provisioner:
```go
	// Clean up rootfs clone (for non-Firecracker backends)
	if tm.daemon.RootfsProvisioner() != nil {
		if err := tm.daemon.RootfsProvisioner().Destroy(ctx, taskID); err != nil {
			fmt.Printf("Warning: failed to destroy rootfs for %s: %v\n", taskID, err)
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/... -count=1`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/rootfs_darwin.go pkg/daemon/rootfs_other.go pkg/daemon/daemon.go pkg/daemon/tasks.go
git commit -m "feat: wire up rootfs provisioner for vfkit backend"
```

---

### Task 5: Move GenerateVMID out of firecracker package

**Files:**
- Modify: `pkg/vmbackend/backend.go`
- Modify: `pkg/daemon/tasks.go`

Currently `tasks.go` imports `pkg/firecracker` just for `GenerateVMID()`. This is a trivial UUID function that should live in the vmbackend package so there's no unnecessary coupling.

- [ ] **Step 1: Add GenerateVMID to vmbackend package**

In `pkg/vmbackend/backend.go`, add:

```go
import (
	"context"
	"time"

	"github.com/google/uuid"
)

// GenerateVMID creates a unique 8-character identifier for a new VM.
func GenerateVMID() string {
	return uuid.New().String()[:8]
}
```

- [ ] **Step 2: Update tasks.go import**

In `pkg/daemon/tasks.go`, replace `firecracker.GenerateVMID()` with `vmbackend.GenerateVMID()`. Then check if the `firecracker` import is still needed — if `CloudInitConfig` is still used, keep it; if not, remove it.

Check what else from `firecracker` is used in tasks.go. If `firecracker.CloudInitConfig` and `firecracker.Generate()` are still used, keep the import. If they can be removed (cloud-init is now handled differently per backend), remove the import.

- [ ] **Step 3: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/... -count=1`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/vmbackend/backend.go pkg/daemon/tasks.go
git commit -m "refactor: move GenerateVMID to vmbackend package"
```

---

### Task 6: Final integration verification

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/... -count=1`
Expected: All PASS

- [ ] **Step 2: Build all binaries**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && make build`
Expected: Success

- [ ] **Step 3: Verify the diff**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && git log --oneline main..HEAD`

Verify the branch has clean, logical commits covering both Phase 1 (interfaces) and Phase 2 (vfkit implementation).

- [ ] **Step 4: Verify vfkit binary is findable**

Run: `which vfkit || echo "vfkit not installed"`

If not installed: `brew install vfkit`

This doesn't affect the build (vfkit is a runtime dependency), but is needed for actually running VMs.

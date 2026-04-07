# VM Backend Interface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract a VM backend interface from stockyard so the daemon can run VMs via Firecracker (Linux) or vfkit/Virtualization.framework (macOS) using the same code paths.

**Architecture:** Introduce a `pkg/vmbackend` package defining a `Backend` interface. Wrap the existing `pkg/firecracker` code to implement it. Introduce a `pkg/rootfs` package defining a `Provisioner` interface, with ZFS and APFS implementations. Update `TaskManager` to use interfaces instead of concrete types. Add an `ip` field to `Task` so macOS VMs (which use direct IP instead of Tailscale hostnames) can be reached via SSH. The vfkit backend itself is NOT part of this plan — this plan extracts the interfaces and wraps the existing code, making the vfkit backend a clean follow-up.

**Tech Stack:** Go, existing stockyard packages, `golang.org/x/sys/unix` (for APFS clonefile on macOS)

---

## File Structure

### New files
- `pkg/vmbackend/backend.go` — `Backend` interface and shared types (`VMConfig`, `VMInfo`, `VMState`)
- `pkg/vmbackend/firecracker.go` — Adapter wrapping `pkg/firecracker.Client` to implement `Backend`
- `pkg/vmbackend/firecracker_test.go` — Tests for the adapter
- `pkg/rootfs/provisioner.go` — `Provisioner` interface
- `pkg/rootfs/zfs.go` — ZFS implementation wrapping `pkg/zfs.Manager`
- `pkg/rootfs/zfs_test.go` — Tests for ZFS provisioner
- `pkg/rootfs/apfs.go` — APFS clonefile implementation (build-tagged `darwin`)
- `pkg/rootfs/apfs_test.go` — Tests for APFS provisioner
- `pkg/rootfs/copy.go` — Fallback file-copy implementation (any platform)
- `pkg/rootfs/copy_test.go` — Tests for copy provisioner

### Modified files
- `pkg/daemon/tasks.go` — Use `vmbackend.Backend` instead of `*firecracker.Client`
- `pkg/daemon/tasks_test.go` — Update to use interface
- `pkg/daemon/daemon.go` — Wire up backend based on config; make DHCP/IP pool conditional
- `pkg/daemon/state.go` — Add `IP` field to `Task` struct, add `UpdateTaskIP` method
- `pkg/daemon/grpc.go` — Pass `IP` through to proto response
- `pkg/daemon/snapshots.go` — Support task-ID-based resolution (not just CID)
- `pkg/daemon/metrics.go` — Make Firecracker-specific metrics optional
- `pkg/config/config.go` — Add `Backend` field and macOS-specific config section
- `api/stockyard.proto` — Add `ip` field to `Task` message
- `pkg/api/v1/stockyard.pb.go` — Regenerated from proto
- `cmd/stockyard/attach.go` — Fall back to `task.IP` when no Tailscale hostname

### NOT changed (intentionally)
- `pkg/firecracker/` — Stays as-is. The adapter wraps it; we don't modify it.
- `pkg/zfs/` — Stays as-is. The ZFS provisioner wraps it.
- Guest binaries (`cmd/stockyard-shell/`, `cmd/stockyard-snapshot/`) — Unchanged.

---

### Task 1: Define the VMBackend interface

**Files:**
- Create: `pkg/vmbackend/backend.go`

- [ ] **Step 1: Create the vmbackend package with interface and types**

```go
// pkg/vmbackend/backend.go
package vmbackend

import (
	"context"
	"time"
)

// Backend abstracts VM lifecycle management across different hypervisors.
type Backend interface {
	// CreateVM provisions and starts a new VM. Returns info needed to reach it.
	CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error)

	// StartVM restarts a previously stopped VM using its existing rootfs.
	StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error)

	// StopVM gracefully stops a running VM.
	StopVM(ctx context.Context, id string) error

	// DeleteVM stops (if running) and removes all resources for a VM.
	DeleteVM(ctx context.Context, id string) error

	// GetVM returns the current state of a VM.
	GetVM(ctx context.Context, id string) (*VMState, error)

	// ListVMs returns all known VMs.
	ListVMs(ctx context.Context) ([]*VMState, error)

	// Close releases any resources held by the backend.
	Close() error
}

// VMConfig specifies what the daemon needs to create a VM.
// Backend-specific concerns (TAP devices, MMDS, CID allocation) are internal
// to each implementation — they do not appear here.
type VMConfig struct {
	ID                string
	VCPU              int32
	MemoryMB          int32
	KernelPath        string
	RootfsPath        string            // Path to this VM's writable rootfs image
	SSHAuthorizedKeys []string
	CloudInitData     string            // Base64-encoded cloud-init user-data
	DotEnv            []byte
	Env               map[string]string
	Metadata          map[string]string // Labels (task-id, task-name, etc.)
}

// VMInfo is returned after a VM is created or started.
type VMInfo struct {
	ID        string
	PID       int       // OS process ID of the hypervisor
	IP        string    // VM's IP address (may be empty if not yet known)
	CID       uint32    // vsock Context ID (Firecracker-specific, 0 if unused)
	VsockPath string    // Path to vsock UDS (Firecracker-specific, empty if unused)
	StateDir  string    // Directory containing VM state files
	State     string
	CreatedAt time.Time
}

// VMState is a lightweight status check for an existing VM.
type VMState struct {
	ID     string
	PID    int
	Status string // "running", "stopped", "unknown"
}
```

- [ ] **Step 2: Verify the package compiles**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go build ./pkg/vmbackend/`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add pkg/vmbackend/backend.go
git commit -m "feat: define VMBackend interface for multi-hypervisor support"
```

---

### Task 2: Define the Rootfs Provisioner interface

**Files:**
- Create: `pkg/rootfs/provisioner.go`

- [ ] **Step 1: Create the rootfs package with interface**

```go
// pkg/rootfs/provisioner.go
package rootfs

import "context"

// Provisioner abstracts rootfs image management across different storage backends.
// Each VM gets its own writable copy of a base image. Implementations handle
// the copy-on-write mechanism (ZFS clones, APFS clonefile, or plain file copy).
type Provisioner interface {
	// Clone creates a writable rootfs for the given VM ID.
	// Returns the filesystem path to the new rootfs image.
	Clone(ctx context.Context, vmID string) (string, error)

	// Destroy removes the rootfs for the given VM ID.
	Destroy(ctx context.Context, vmID string) error

	// EnsureBase verifies the base image is ready for cloning.
	EnsureBase(ctx context.Context) error
}
```

- [ ] **Step 2: Verify the package compiles**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go build ./pkg/rootfs/`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add pkg/rootfs/provisioner.go
git commit -m "feat: define Rootfs Provisioner interface"
```

---

### Task 3: Implement ZFS Provisioner

**Files:**
- Create: `pkg/rootfs/zfs.go`
- Create: `pkg/rootfs/zfs_test.go`

This wraps the existing `pkg/zfs.Manager` and the ZFS clone logic currently inline in `pkg/firecracker/client.go:140-179`.

- [ ] **Step 1: Write the test**

```go
// pkg/rootfs/zfs_test.go
package rootfs

import (
	"context"
	"testing"
)

func TestZFSProvisioner_Clone_NilManager(t *testing.T) {
	// ZFS provisioner requires a non-nil manager
	p := NewZFSProvisioner(nil, "", "", "")
	_, err := p.Clone(context.Background(), "test-vm")
	if err == nil {
		t.Fatal("expected error with nil ZFS manager")
	}
}

func TestZFSProvisioner_Fields(t *testing.T) {
	p := NewZFSProvisioner(nil, "tank", "stockyard/images", "stockyard/vms")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/rootfs/ -run TestZFS -v`
Expected: FAIL — `NewZFSProvisioner` not defined

- [ ] **Step 3: Implement ZFS provisioner**

```go
// pkg/rootfs/zfs.go
package rootfs

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/obra/stockyard/pkg/zfs"
)

// ZFSProvisioner uses ZFS clone for copy-on-write rootfs provisioning.
type ZFSProvisioner struct {
	zfsMgr     *zfs.Manager
	pool       string
	imagesPath string // e.g. "stockyard/images"
	vmsPath    string // e.g. "stockyard/vms"
}

// NewZFSProvisioner creates a provisioner that clones rootfs images via ZFS.
// zfsMgr may be nil (Clone will return an error).
func NewZFSProvisioner(zfsMgr *zfs.Manager, pool, imagesPath, vmsPath string) Provisioner {
	return &ZFSProvisioner{
		zfsMgr:     zfsMgr,
		pool:       pool,
		imagesPath: imagesPath,
		vmsPath:    vmsPath,
	}
}

func (p *ZFSProvisioner) Clone(ctx context.Context, vmID string) (string, error) {
	if p.zfsMgr == nil {
		return "", fmt.Errorf("ZFS manager not available")
	}

	snapshotPath := fmt.Sprintf("%s/%s/rootfs@base", p.pool, p.imagesPath)
	vmDatasetPath := fmt.Sprintf("%s/%s/%s", p.pool, p.vmsPath, vmID)

	// Ensure parent dataset exists
	parentDataset := fmt.Sprintf("%s/%s", p.pool, p.vmsPath)
	exec.CommandContext(ctx, "zfs", "create", "-p", parentDataset).Run()

	cmd := exec.CommandContext(ctx, "zfs", "clone", snapshotPath, vmDatasetPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("zfs clone failed: %w: %s", err, string(output))
	}

	// Get mountpoint
	cmd = exec.CommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", vmDatasetPath)
	output, err := cmd.Output()
	if err != nil {
		// Clean up on failure
		exec.CommandContext(ctx, "zfs", "destroy", "-r", vmDatasetPath).Run()
		return "", fmt.Errorf("zfs get mountpoint failed: %w", err)
	}

	mountpoint := strings.TrimSpace(string(output))
	return filepath.Join(mountpoint, "rootfs.ext4"), nil
}

func (p *ZFSProvisioner) Destroy(ctx context.Context, vmID string) error {
	if p.zfsMgr == nil {
		return nil
	}
	vmDatasetPath := fmt.Sprintf("%s/%s/%s", p.pool, p.vmsPath, vmID)
	cmd := exec.CommandContext(ctx, "zfs", "destroy", "-r", vmDatasetPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs destroy failed: %w: %s", err, string(output))
	}
	return nil
}

func (p *ZFSProvisioner) EnsureBase(ctx context.Context) error {
	snapshotPath := fmt.Sprintf("%s/%s/rootfs@base", p.pool, p.imagesPath)
	cmd := exec.CommandContext(ctx, "zfs", "list", "-t", "snapshot", snapshotPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("base snapshot %s not found: %w", snapshotPath, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/rootfs/ -run TestZFS -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/rootfs/zfs.go pkg/rootfs/zfs_test.go
git commit -m "feat: implement ZFS rootfs provisioner"
```

---

### Task 4: Implement Copy Provisioner (fallback)

**Files:**
- Create: `pkg/rootfs/copy.go`
- Create: `pkg/rootfs/copy_test.go`

This is the platform-agnostic fallback: plain file copy. Used when ZFS isn't available and we're not on macOS/APFS. Already exists inline in `firecracker/client.go:172-178`.

- [ ] **Step 1: Write the test**

```go
// pkg/rootfs/copy_test.go
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

	// Create a small base image
	if err := os.WriteFile(baseImage, []byte("fake rootfs content"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewCopyProvisioner(baseImage, vmsDir)

	// Clone
	path, err := p.Clone(context.Background(), "vm-001")
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	// Verify file exists and has correct content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read cloned rootfs: %v", err)
	}
	if string(data) != "fake rootfs content" {
		t.Errorf("content mismatch: got %q", string(data))
	}

	// Destroy
	if err := p.Destroy(context.Background(), "vm-001"); err != nil {
		t.Fatalf("Destroy failed: %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("expected VM directory to be removed")
	}
}

func TestCopyProvisioner_EnsureBase(t *testing.T) {
	tmpDir := t.TempDir()
	baseImage := filepath.Join(tmpDir, "base.img")

	p := NewCopyProvisioner(baseImage, tmpDir)

	// Should fail — file doesn't exist
	if err := p.EnsureBase(context.Background()); err == nil {
		t.Error("expected error when base image missing")
	}

	// Create it
	os.WriteFile(baseImage, []byte("x"), 0644)

	// Should succeed
	if err := p.EnsureBase(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/rootfs/ -run TestCopy -v`
Expected: FAIL — `NewCopyProvisioner` not defined

- [ ] **Step 3: Implement copy provisioner**

```go
// pkg/rootfs/copy.go
package rootfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyProvisioner creates rootfs copies via plain file copy.
// Works on any platform but is slow for large images.
type CopyProvisioner struct {
	baseImage string
	vmsDir    string
}

// NewCopyProvisioner creates a provisioner that copies the base image per VM.
func NewCopyProvisioner(baseImage, vmsDir string) Provisioner {
	return &CopyProvisioner{
		baseImage: baseImage,
		vmsDir:    vmsDir,
	}
}

func (p *CopyProvisioner) Clone(_ context.Context, vmID string) (string, error) {
	vmDir := filepath.Join(p.vmsDir, vmID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return "", fmt.Errorf("create VM directory: %w", err)
	}

	dst := filepath.Join(vmDir, "rootfs.img")
	if err := copyFile(p.baseImage, dst); err != nil {
		os.RemoveAll(vmDir)
		return "", fmt.Errorf("copy rootfs: %w", err)
	}
	return dst, nil
}

func (p *CopyProvisioner) Destroy(_ context.Context, vmID string) error {
	vmDir := filepath.Join(p.vmsDir, vmID)
	return os.RemoveAll(vmDir)
}

func (p *CopyProvisioner) EnsureBase(_ context.Context) error {
	if _, err := os.Stat(p.baseImage); err != nil {
		return fmt.Errorf("base image not found: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	success := false
	defer func() {
		out.Close()
		if !success {
			os.Remove(dst)
		}
	}()

	buf := make([]byte, 4*1024*1024) // 4MB buffer
	if _, err := io.CopyBuffer(out, in, buf); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}

	success = true
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/rootfs/ -run TestCopy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/rootfs/copy.go pkg/rootfs/copy_test.go
git commit -m "feat: implement copy-based rootfs provisioner (fallback)"
```

---

### Task 5: Implement APFS Provisioner (macOS)

**Files:**
- Create: `pkg/rootfs/apfs.go` (build tag: `darwin`)
- Create: `pkg/rootfs/apfs_test.go` (build tag: `darwin`)

Uses `unix.Clonefile()` for instant copy-on-write on APFS volumes.

- [ ] **Step 1: Write the test**

```go
//go:build darwin

// pkg/rootfs/apfs_test.go
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

	// Create a small base image
	if err := os.WriteFile(baseImage, []byte("fake rootfs content"), 0644); err != nil {
		t.Fatal(err)
	}

	p := NewAPFSProvisioner(baseImage, vmsDir)

	// Clone
	path, err := p.Clone(context.Background(), "vm-001")
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	// Verify file exists and has correct content
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read cloned rootfs: %v", err)
	}
	if string(data) != "fake rootfs content" {
		t.Errorf("content mismatch: got %q", string(data))
	}

	// Destroy
	if err := p.Destroy(context.Background(), "vm-001"); err != nil {
		t.Fatalf("Destroy failed: %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("expected VM directory to be removed")
	}
}

func TestAPFSProvisioner_ClonefileIsCOW(t *testing.T) {
	// Verify that modifying the clone doesn't affect the original
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

	// Modify the clone
	if err := os.WriteFile(path, []byte("modified content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Original should be unchanged
	data, err := os.ReadFile(baseImage)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original content" {
		t.Error("original was modified — not a true clone")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/rootfs/ -run TestAPFS -v`
Expected: FAIL — `NewAPFSProvisioner` not defined

- [ ] **Step 3: Implement APFS provisioner**

```go
//go:build darwin

// pkg/rootfs/apfs.go
package rootfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// APFSProvisioner uses macOS APFS clonefile for instant copy-on-write rootfs cloning.
type APFSProvisioner struct {
	baseImage string
	vmsDir    string
}

// NewAPFSProvisioner creates a provisioner that uses APFS clonefile.
func NewAPFSProvisioner(baseImage, vmsDir string) Provisioner {
	return &APFSProvisioner{
		baseImage: baseImage,
		vmsDir:    vmsDir,
	}
}

func (p *APFSProvisioner) Clone(_ context.Context, vmID string) (string, error) {
	vmDir := filepath.Join(p.vmsDir, vmID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return "", fmt.Errorf("create VM directory: %w", err)
	}

	dst := filepath.Join(vmDir, "rootfs.img")
	if err := unix.Clonefile(p.baseImage, dst, 0); err != nil {
		os.RemoveAll(vmDir)
		return "", fmt.Errorf("clonefile: %w", err)
	}
	return dst, nil
}

func (p *APFSProvisioner) Destroy(_ context.Context, vmID string) error {
	vmDir := filepath.Join(p.vmsDir, vmID)
	return os.RemoveAll(vmDir)
}

func (p *APFSProvisioner) EnsureBase(_ context.Context) error {
	if _, err := os.Stat(p.baseImage); err != nil {
		return fmt.Errorf("base image not found: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Add `golang.org/x/sys` dependency if not already present**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && grep "golang.org/x/sys" go.mod`

If not present:
Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go get golang.org/x/sys`

- [ ] **Step 5: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/rootfs/ -run TestAPFS -v`
Expected: PASS (on macOS; skipped on Linux due to build tag)

- [ ] **Step 6: Commit**

```bash
git add pkg/rootfs/apfs.go pkg/rootfs/apfs_test.go go.mod go.sum
git commit -m "feat: implement APFS clonefile rootfs provisioner (macOS)"
```

---

### Task 6: Wrap Firecracker client as a VMBackend adapter

**Files:**
- Create: `pkg/vmbackend/firecracker.go`
- Create: `pkg/vmbackend/firecracker_test.go`

This is a thin adapter that wraps `*firecracker.Client` to satisfy the `Backend` interface. It delegates every call. The point is that `TaskManager` can now talk to `Backend` instead of `*firecracker.Client` directly.

- [ ] **Step 1: Write the test**

```go
// pkg/vmbackend/firecracker_test.go
package vmbackend

import (
	"testing"
)

func TestFirecrackerBackend_ImplementsInterface(t *testing.T) {
	// Compile-time check that FirecrackerBackend satisfies Backend
	var _ Backend = (*FirecrackerBackend)(nil)
}

func TestFirecrackerBackend_NilClient(t *testing.T) {
	// A nil-client backend should not panic on Close
	b := NewFirecrackerBackend(nil)
	if err := b.Close(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/vmbackend/ -run TestFirecracker -v`
Expected: FAIL — `FirecrackerBackend` not defined

- [ ] **Step 3: Implement the adapter**

```go
// pkg/vmbackend/firecracker.go
package vmbackend

import (
	"context"
	"time"

	"github.com/obra/stockyard/pkg/firecracker"
)

// FirecrackerBackend adapts a firecracker.Client to the Backend interface.
type FirecrackerBackend struct {
	client *firecracker.Client
}

// NewFirecrackerBackend wraps an existing firecracker.Client.
func NewFirecrackerBackend(client *firecracker.Client) *FirecrackerBackend {
	return &FirecrackerBackend{client: client}
}

func (b *FirecrackerBackend) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	fcCfg := &firecracker.VMConfig{
		ID:                cfg.ID,
		Namespace:         "stockyard",
		VCPU:              cfg.VCPU,
		MemoryMB:          cfg.MemoryMB,
		KernelPath:        cfg.KernelPath,
		RootfsPath:        cfg.RootfsPath,
		CloudInitData:     cfg.CloudInitData,
		SSHAuthorizedKeys: cfg.SSHAuthorizedKeys,
		DotEnv:            cfg.DotEnv,
		Metadata:          cfg.Metadata,
	}

	// Pass through Firecracker-specific fields from Env map
	if cfg.Env != nil {
		if v, ok := cfg.Env["_tailscale_auth_key"]; ok {
			fcCfg.TailscaleAuthKey = v
		}
		if v, ok := cfg.Env["_static_ip_args"]; ok {
			fcCfg.StaticIPArgs = v
		}
	}

	// Pass through Tailscale state from metadata
	if cfg.Metadata != nil {
		if v, ok := cfg.Metadata["_tailscale_state"]; ok {
			fcCfg.TailscaleState = []byte(v)
		}
	}

	// NetworkMMDS is set from metadata if present
	if cfg.Metadata != nil {
		if ip, ok := cfg.Metadata["_network_ip"]; ok {
			fcCfg.NetworkMMDS = &firecracker.MMDSNetworkConfig{
				IP:      ip,
				Netmask: cfg.Metadata["_network_netmask"],
				Gateway: cfg.Metadata["_network_gateway"],
				DNS:     cfg.Metadata["_network_dns"],
			}
		}
	}

	vm, err := b.client.CreateVM(ctx, fcCfg)
	if err != nil {
		return nil, err
	}

	return &VMInfo{
		ID:        vm.ID,
		PID:       vm.PID,
		CID:       vm.CID,
		VsockPath: vm.VsockPath,
		StateDir:  "", // Firecracker manages its own state dir
		State:     vm.State,
		CreatedAt: vm.CreatedAt,
	}, nil
}

func (b *FirecrackerBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	fcCfg := &firecracker.VMConfig{
		ID:        cfg.ID,
		Namespace: "stockyard",
		VCPU:      cfg.VCPU,
		MemoryMB:  cfg.MemoryMB,
	}

	vm, err := b.client.StartVM(ctx, fcCfg)
	if err != nil {
		return nil, err
	}

	return &VMInfo{
		ID:        vm.ID,
		PID:       vm.PID,
		CID:       vm.CID,
		VsockPath: vm.VsockPath,
		State:     vm.State,
		CreatedAt: vm.CreatedAt,
	}, nil
}

func (b *FirecrackerBackend) StopVM(ctx context.Context, id string) error {
	return b.client.StopVM(ctx, "stockyard", id)
}

func (b *FirecrackerBackend) DeleteVM(ctx context.Context, id string) error {
	return b.client.DeleteVM(ctx, "stockyard", id)
}

func (b *FirecrackerBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	vm, err := b.client.GetVM(ctx, "stockyard", id)
	if err != nil {
		return nil, err
	}
	return &VMState{
		ID:     vm.ID,
		PID:    vm.PID,
		Status: vm.Status.String(),
	}, nil
}

func (b *FirecrackerBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	vms, err := b.client.ListVMs(ctx, "stockyard")
	if err != nil {
		return nil, err
	}
	var states []*VMState
	for _, vm := range vms {
		states = append(states, &VMState{
			ID:     vm.ID,
			PID:    vm.PID,
			Status: vm.Status.String(),
		})
	}
	return states, nil
}

func (b *FirecrackerBackend) Close() error {
	if b.client == nil {
		return nil
	}
	return b.client.Close()
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/vmbackend/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/firecracker.go pkg/vmbackend/firecracker_test.go
git commit -m "feat: implement Firecracker VMBackend adapter"
```

---

### Task 7: Add IP field to Task and proto

**Files:**
- Modify: `pkg/daemon/state.go` — Add `IP` field to `Task`, `UpdateTaskIP` method, update schema
- Modify: `api/stockyard.proto` — Add `ip` field to `Task` message
- Modify: `pkg/daemon/grpc.go` — Include IP in `taskToProto`

On macOS, VMs won't have a Tailscale hostname — they'll have a direct IP. Both need to be available to the CLI.

- [ ] **Step 1: Add IP field to daemon Task struct**

In `pkg/daemon/state.go`, modify the `Task` struct (around line 34):

```go
type Task struct {
	ID                string
	Name              string
	Command           string
	Status            string
	VMID              string
	CID               uint32
	VsockPath         string
	IP                string // Direct IP address (for macOS/non-Tailscale access)
	Owner             string
	TailscaleHostname string
	CreatedAt         time.Time
	StoppedAt         *time.Time
}
```

- [ ] **Step 2: Update the SQLite schema migration in `initDB`**

Find the `CREATE TABLE IF NOT EXISTS tasks` statement in `state.go` and add the `ip` column. Then add the `UpdateTaskIP` method after `UpdateTaskVsockPath`:

```go
func (s *State) UpdateTaskIP(taskID, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec("UPDATE tasks SET ip = ? WHERE id = ?", ip, taskID)
	return err
}
```

Also update `CreateTask` and `scanTask` to include the `ip` field.

- [ ] **Step 3: Add `ip` field to proto Task message**

In `api/stockyard.proto`, update the `Task` message:

```protobuf
message Task {
    string id = 1;
    string name = 2;
    string status = 3;
    string tailscale_hostname = 4;
    string created_at = 5;
    string stopped_at = 6;
    string ip = 7;
}
```

- [ ] **Step 4: Regenerate protobuf**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && make proto` (or the project's protobuf generation command)

If there's no `make proto` target, run:
```bash
protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative api/stockyard.proto
```

- [ ] **Step 5: Update `taskToProto` in `grpc.go`**

In `pkg/daemon/grpc.go`, update the `taskToProto` function (around line 519):

```go
func taskToProto(t *Task) *pb.Task {
	pt := &pb.Task{
		Id:                t.ID,
		Name:              t.Name,
		Status:            t.Status,
		TailscaleHostname: t.TailscaleHostname,
		Ip:                t.IP,
		CreatedAt:         t.CreatedAt.Format(time.RFC3339),
	}
	if t.StoppedAt != nil {
		pt.StoppedAt = t.StoppedAt.Format(time.RFC3339)
	}
	return pt
}
```

- [ ] **Step 6: Run all tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/daemon/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/daemon/state.go pkg/daemon/grpc.go api/stockyard.proto pkg/api/v1/
git commit -m "feat: add IP field to Task for direct VM access"
```

---

### Task 8: Update TaskManager to use Backend interface

**Files:**
- Modify: `pkg/daemon/tasks.go`
- Modify: `pkg/daemon/tasks_test.go`
- Modify: `pkg/daemon/daemon.go`

This is the core refactoring. `TaskManager` stops holding `*firecracker.Client` and holds `vmbackend.Backend` instead.

- [ ] **Step 1: Update TaskManager struct and constructor in `tasks.go`**

Replace the `fc` field and `FirecrackerConfig` type. The new `TaskManager`:

```go
import (
	// ... existing imports ...
	"github.com/obra/stockyard/pkg/vmbackend"
)

// TaskManager handles the lifecycle of VM-based tasks.
type TaskManager struct {
	daemon  *Daemon
	backend vmbackend.Backend
}

// NewTaskManager creates a TaskManager with the given daemon and VM backend.
func NewTaskManager(d *Daemon, backend vmbackend.Backend) *TaskManager {
	return &TaskManager{
		daemon:  d,
		backend: backend,
	}
}
```

- [ ] **Step 2: Update CreateTask to use the backend**

Replace `tm.fc.CreateVM(ctx, vmCfg)` with `tm.backend.CreateVM(ctx, vmCfg)`. The key changes:

- Rootfs cloning is now done by the daemon (via `Provisioner`) before calling the backend — the backend receives a `RootfsPath` pointing to the already-cloned image.
- Firecracker-specific fields (Tailscale auth key, static IP args, MMDS network config) are passed through `VMConfig.Env` and `VMConfig.Metadata` maps — the Firecracker adapter extracts them.
- After CreateVM, store the VM's IP if the backend provides one.

The `if tm.fc != nil` guard becomes `if tm.backend != nil`.

- [ ] **Step 3: Update StopTask, DestroyTask, RestartTask**

Replace all `tm.fc.StopVM(ctx, "stockyard", task.VMID)` with `tm.backend.StopVM(ctx, task.VMID)`. Same pattern for DeleteVM and StartVM. The namespace is no longer passed — it's internal to the Firecracker adapter.

- [ ] **Step 4: Update DestroyTask rootfs cleanup**

Currently DestroyTask calls `tm.daemon.zfs.DestroyDataset()`. This should be replaced with a call to the rootfs provisioner (which lives on the daemon). The daemon will expose a `RootfsProvisioner()` accessor:

```go
// In DestroyTask:
if tm.daemon.RootfsProvisioner() != nil {
	if err := tm.daemon.RootfsProvisioner().Destroy(ctx, taskID); err != nil {
		fmt.Printf("Warning: failed to destroy rootfs for %s: %v\n", taskID, err)
	}
}
```

- [ ] **Step 5: Update tests**

In `tasks_test.go`, update `NewTaskManager` calls. Currently they pass `nil` for the firecracker config; now they pass `nil` for the backend:

```go
// Before:
tm := NewTaskManager(d, nil)

// After:
tm := NewTaskManager(d, nil)  // Same — nil backend means "no VM" mode
```

The test structure doesn't change because the tests already test with `nil` (no actual VM creation).

- [ ] **Step 6: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/daemon/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/daemon/tasks.go pkg/daemon/tasks_test.go
git commit -m "refactor: TaskManager uses VMBackend interface instead of firecracker.Client"
```

---

### Task 9: Update daemon.go to wire up backend and provisioner

**Files:**
- Modify: `pkg/daemon/daemon.go`
- Modify: `pkg/config/config.go`

- [ ] **Step 1: Add backend config to config.go**

```go
// Add to Config struct:
type Config struct {
	InstanceID  string            `json:"instance_id"`
	Backend     string            `json:"backend"` // "firecracker" (default) or "vfkit"
	Secrets     SecretsConfig     `json:"secrets"`
	Daemon      DaemonConfig      `json:"daemon"`
	ZFS         ZFSConfig         `json:"zfs"`
	Firecracker FirecrackerConfig `json:"firecracker"`
	VM          VMConfig          `json:"vm"`
	HTTP        HTTPConfig        `json:"http"`
	Rootfs      RootfsConfig      `json:"rootfs"`
}

// Add new config type:
type RootfsConfig struct {
	Provider  string `json:"provider"`   // "zfs" (default), "apfs", "copy"
	BaseImage string `json:"base_image"` // Path to base rootfs image (for apfs/copy)
	VMsDir    string `json:"vms_dir"`    // Directory for VM rootfs copies (for apfs/copy)
}
```

- [ ] **Step 2: Update daemon.New() to create backend based on config**

In `daemon.go`, replace the `FirecrackerConfig` handling with backend selection:

```go
// In New():
var backend vmbackend.Backend

switch cfg.Backend {
case "", "firecracker":
	// Existing Firecracker path
	if cfg.Firecracker.KernelPath != "" && cfg.Firecracker.RootfsPath != "" {
		fcCfg := firecracker.ClientConfig{
			KernelPath: cfg.Firecracker.KernelPath,
			RootfsPath: cfg.Firecracker.RootfsPath,
			BridgeName: cfg.Firecracker.BridgeName,
			ImagesPath: cfg.ZFS.ImagesPath,
			VMsPath:    cfg.ZFS.VMsPath,
		}
		client, err := firecracker.NewClient(fcCfg, zfsMgr)
		if err != nil {
			fmt.Printf("Warning: failed to create firecracker client: %v\n", err)
		} else {
			backend = vmbackend.NewFirecrackerBackend(client)
		}
	}
// case "vfkit": — future, not implemented in this plan
default:
	return nil, fmt.Errorf("unknown backend: %s", cfg.Backend)
}

d.tasks = NewTaskManager(d, backend)
```

- [ ] **Step 3: Make DHCP and IP pool conditional on Firecracker backend**

Wrap the DHCP/IP pool initialization in a backend check — only needed for Firecracker:

```go
if cfg.Backend == "" || cfg.Backend == "firecracker" {
	// Initialize DHCP server
	dhcpConfig := network.DHCPConfig{ ... }
	// ... existing code ...

	// Initialize IP pool
	// ... existing code ...
}
```

- [ ] **Step 4: Add rootfs provisioner to daemon**

```go
// In daemon struct:
type Daemon struct {
	// ... existing fields ...
	rootfsProvisioner rootfs.Provisioner
}

// Accessor:
func (d *Daemon) RootfsProvisioner() rootfs.Provisioner {
	return d.rootfsProvisioner
}
```

Wire it up in `New()` based on config — for now, always use ZFS when backend is Firecracker (matching current behavior).

- [ ] **Step 5: Run all tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/... -count=1`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/daemon/daemon.go pkg/config/config.go
git commit -m "refactor: wire up VMBackend and RootfsProvisioner in daemon"
```

---

### Task 10: Update CLI attach command to support direct IP

**Files:**
- Modify: `cmd/stockyard/attach.go`
- Modify: `cmd/stockyard/logs.go`

- [ ] **Step 1: Update attach.go to fall back to IP**

```go
// In attach command RunE, replace the Tailscale hostname check:

// Determine SSH target — prefer Tailscale hostname, fall back to direct IP
sshHost := task.TailscaleHostname
if sshHost == "" {
	sshHost = task.Ip  // proto field name
}
if sshHost == "" {
	return fmt.Errorf("task has no reachable address (no Tailscale hostname or IP)")
}
```

- [ ] **Step 2: Update logs.go similarly**

In `logs.go`, the `streamLogsSSH` call uses `task.TailscaleHostname`. Update to prefer hostname, fall back to IP:

```go
hostname := task.TailscaleHostname
if hostname == "" {
	hostname = task.Ip
}
if hostname == "" {
	return fmt.Errorf("task has no reachable address")
}
return streamLogsSSH(hostname, cfg.VM.User, logsFollow, logsSystem)
```

- [ ] **Step 3: Verify build**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go build ./cmd/stockyard/`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add cmd/stockyard/attach.go cmd/stockyard/logs.go
git commit -m "feat: CLI falls back to direct IP when no Tailscale hostname"
```

---

### Task 11: Make metrics and snapshots backend-aware

**Files:**
- Modify: `pkg/daemon/metrics.go`
- Modify: `pkg/daemon/snapshots.go`

- [ ] **Step 1: Make MetricsPoller optional**

The `MetricsPoller` reads Firecracker-specific NDJSON from a FIFO. On non-Firecracker backends, there's no metrics FIFO. The daemon already guards metrics with `if tm.daemon.metricsPoller != nil`, so we just need to make sure the poller is only created when using Firecracker:

In `daemon.go`, the metrics poller creation is already inside the `if d.cfg.HTTP.Enabled` block. Add a backend check:

```go
// Only create metrics poller for Firecracker backend (uses FIFO)
if d.cfg.HTTP.Enabled && (d.cfg.Backend == "" || d.cfg.Backend == "firecracker") {
	d.metricsPoller = NewMetricsPoller(d, &dashboardMetricsSink{d.metricsCollector}, 5*time.Second)
	d.metricsPoller.Start()
}
```

- [ ] **Step 2: Update snapshot service to handle non-CID VMs**

In `snapshots.go`, the `resolveTaskID` function only handles `cid-*` format. Add support for direct task ID (for backends that don't use CIDs):

```go
func (ss *SnapshotService) resolveTaskID(vmID string) (string, error) {
	if vmID == "unix-client" {
		tasks, err := ss.daemon.state.ListTasks("running")
		if err != nil || len(tasks) == 0 {
			return "", fmt.Errorf("no running tasks")
		}
		return tasks[0].ID, nil
	}

	if strings.HasPrefix(vmID, "cid-") {
		cidStr := strings.TrimPrefix(vmID, "cid-")
		cid, err := strconv.ParseUint(cidStr, 10, 32)
		if err != nil {
			return "", fmt.Errorf("invalid CID: %s", vmID)
		}
		task, err := ss.daemon.state.GetTaskByCID(uint32(cid))
		if err != nil {
			return "", err
		}
		return task.ID, nil
	}

	// Direct task ID (for non-Firecracker backends)
	if _, err := ss.daemon.state.GetTask(vmID); err == nil {
		return vmID, nil
	}

	return "", fmt.Errorf("unknown VM ID format: %s", vmID)
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/daemon/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pkg/daemon/metrics.go pkg/daemon/snapshots.go pkg/daemon/daemon.go
git commit -m "refactor: make metrics and snapshots backend-aware"
```

---

### Task 12: Final integration test — verify all tests pass

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && go test ./pkg/... -count=1`
Expected: All packages PASS

- [ ] **Step 2: Build all binaries**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && make build`
Expected: Successful build

- [ ] **Step 3: Verify no regressions by reviewing the diff**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && git diff main --stat`

Verify:
- `pkg/firecracker/` is **unchanged** (no modifications to existing code)
- `pkg/zfs/` is **unchanged**
- New packages: `pkg/vmbackend/`, `pkg/rootfs/`
- Modified: `pkg/daemon/`, `pkg/config/`, `cmd/stockyard/`, `api/`

- [ ] **Step 4: Commit any remaining changes and tag the branch**

```bash
git status
# If anything unstaged, add and commit
```

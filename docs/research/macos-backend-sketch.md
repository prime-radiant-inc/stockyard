# macOS Backend Sketch: vz vs vfkit

**Date:** 2026-03-31
**Author:** Lyra (Bob 0ebea785 / Opus 4.6)
**Status:** Design sketch / Private
**Prereq:** [macOS Virtualization Options](macos-virtualization-options.md)

## Context

Stockyard's current architecture is: `daemon → firecracker.Client → Firecracker process → VM`. The `firecracker.Client` handles everything: rootfs cloning (via ZFS), TAP/bridge networking, MMDS metadata, VM lifecycle via HTTP API, vsock allocation, process management.

On macOS, two viable approaches exist for the VM backend:

- **Path A:** Use `Code-Hex/vz` Go bindings directly (VM runs in-process)
- **Path B:** Use `vfkit` as a subprocess (one process per VM, like Firecracker today)

Both use Apple's Virtualization.framework underneath. The difference is where the VM lives.

---

## Architecture Comparison

### Path A: Code-Hex/vz Direct

```
stockyardd (Go process)
  ├── VZVirtualMachine (in-process, via cgo → ObjC)
  │     ├── VZLinuxBootLoader (kernel + initramfs)
  │     ├── VZVirtioBlockDevice (rootfs.img)
  │     ├── VZNATNetworkDeviceAttachment (networking)
  │     └── VZVirtioSocketDevice (vsock for shell/control)
  ├── VZVirtualMachine (another VM)
  └── ...
```

### Path B: vfkit Subprocess

```
stockyardd (Go process)
  ├── vfkit process (VM 1)
  │     └── Virtualization.framework VM
  ├── vfkit process (VM 2)
  │     └── Virtualization.framework VM
  └── ...
```

---

## Pros and Cons

### Path A: vz Direct

**Pros:**
- No external binary dependency. Ship one binary.
- Direct API access — full control over every VZ configuration option.
- Can react to VM events (state changes, vsock connections) in-process without IPC.
- Lower latency for VM operations (no subprocess exec + CLI parsing).
- Can use VZVirtioSocketDevice listeners directly in Go — vsock connections are just `net.Conn`.

**Cons:**
- **VM crash can take down the daemon.** A Virtualization.framework bug or kernel panic in the VM could crash the host process. Firecracker avoids this because it's a separate process.
- **cgo dependency.** Builds require macOS SDK, won't cross-compile from Linux. CI must run on macOS.
- **Thread/runtime complexity.** VZ objects must be used from the main thread (ObjC requirement). Code-Hex/vz handles this with `runtime.LockOSThread()`, but it adds subtle constraints.
- **All VMs die if daemon restarts.** VM lifetime = process lifetime. No "reconnect to existing VM" after daemon restart.

### Path B: vfkit Subprocess

**Pros:**
- **Process isolation.** VM crash only kills the vfkit process. Daemon survives.
- **Matches the current Firecracker model.** One subprocess per VM. PID tracking, process reaping — same patterns already in `client.go`.
- **VMs can outlive daemon restarts.** vfkit processes are independent. Daemon can reconnect (read PID file, check process, re-establish vsock).
- **No cgo in the daemon.** Can drive vfkit via CLI or its Go package (`github.com/crc-org/vfkit/pkg/config`). Cleaner build.
- **Battle-tested.** Used by Podman, CRC/OpenShift Local in production.

**Cons:**
- External binary dependency. Need `vfkit` installed (`brew install vfkit`).
- CLI interface may not expose every VZ option we want.
- vsock connections require connecting to a Unix socket file (vfkit exposes vsock as UDS), slightly more indirection.
- Slightly higher latency per VM operation (subprocess exec vs in-process call).

---

## My Take

**Path B (vfkit) is the better starting point.** Here's why:

1. **It mirrors what we already have.** The Firecracker backend is "daemon manages subprocesses." vfkit is the same pattern. The refactoring to support both is smaller.

2. **Daemon crash isolation matters.** If you're running 5 agent VMs and the daemon hiccups, losing all 5 VMs is bad. With vfkit they keep running.

3. **Daemon restart survivability.** On Linux, `reconcileRunningVMs()` already checks if Firecracker processes survived a daemon restart. The same pattern works with vfkit processes.

4. **No cgo in the daemon.** This keeps the build simple and cross-compilable (the daemon binary for macOS doesn't need cgo; only vfkit does, and it's a separate project).

5. **We can always move to Path A later** if we need tighter integration. The VMBackend interface is the same either way.

---

## Sketch: What the Code Would Look Like

### 1. VMBackend Interface

New package `pkg/vmbackend/` with an interface both Firecracker and vfkit implement:

```go
// pkg/vmbackend/backend.go
package vmbackend

type Backend interface {
    CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error)
    StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error)
    StopVM(ctx context.Context, id string) error
    DeleteVM(ctx context.Context, id string) error
    GetVM(ctx context.Context, id string) (*VMState, error)
    ListVMs(ctx context.Context) ([]*VMState, error)
    Close() error
}

type VMConfig struct {
    ID         string
    VCPU       int32
    MemoryMB   int32
    KernelPath string
    RootfsPath string            // Path to this VM's rootfs image
    SSHKeys    []string
    DotEnv     []byte
    Env        map[string]string // Environment to inject
}

type VMInfo struct {
    ID        string
    PID       int               // OS process ID (Firecracker or vfkit)
    IP        string            // VM's IP address (for SSH/rsync)
    State     string
    StateDir  string            // Where VM state files live
}

type VMState struct {
    ID     string
    PID    int
    Status string // "running", "stopped", "unknown"
}
```

Note what's NOT here: no Tailscale, no MMDS, no CID, no TAP, no bridge, no ZFS. Those are implementation details of each backend or the layer above.

### 2. RootfsProvisioner Interface

New package or interface in `pkg/rootfs/`:

```go
// pkg/rootfs/provisioner.go
package rootfs

type Provisioner interface {
    // Clone creates a writable copy of the base rootfs image for a VM.
    // Returns the path to the new rootfs file/device.
    Clone(ctx context.Context, vmID string) (string, error)

    // Destroy removes the VM's rootfs clone.
    Destroy(ctx context.Context, vmID string) error

    // EnsureBase ensures the base image is ready for cloning.
    EnsureBase(ctx context.Context) error
}
```

**ZFS implementation** (Linux): wraps existing `pkg/zfs` — `zfs clone`, `zfs destroy`.

**APFS implementation** (macOS):

```go
// pkg/rootfs/apfs.go
package rootfs

type APFSProvisioner struct {
    baseImage string   // e.g., /var/lib/stockyard/rootfs.img
    vmDir     string   // e.g., /var/lib/stockyard/vms/
}

func (p *APFSProvisioner) Clone(ctx context.Context, vmID string) (string, error) {
    dst := filepath.Join(p.vmDir, vmID, "rootfs.img")
    os.MkdirAll(filepath.Dir(dst), 0755)
    err := unix.Clonefile(p.baseImage, dst, 0)  // Instant CoW copy
    return dst, err
}

func (p *APFSProvisioner) Destroy(ctx context.Context, vmID string) error {
    return os.RemoveAll(filepath.Join(p.vmDir, vmID))
}

func (p *APFSProvisioner) EnsureBase(ctx context.Context) error {
    _, err := os.Stat(p.baseImage)
    return err
}
```

### 3. vfkit Backend

```go
// pkg/vmbackend/vfkit/backend.go
package vfkit

type Backend struct {
    cfg      Config
    rootfs   rootfs.Provisioner
    stateDir string
    procs    map[string]*exec.Cmd
    mu       sync.Mutex
}

type Config struct {
    VfkitBin   string // path to vfkit binary
    KernelPath string // arm64 vmlinux
    StateDir   string
}

func (b *Backend) CreateVM(ctx context.Context, cfg *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
    // 1. Clone rootfs
    rootfsPath, err := b.rootfs.Clone(ctx, cfg.ID)

    // 2. Build vfkit command
    //    vfkit \
    //      --cpus 2 --memory 1024 \
    //      --bootloader linux,kernel=<kernel>,cmdline="..." \
    //      --device virtio-blk,path=<rootfs> \
    //      --device virtio-net,nat \
    //      --device virtio-vsock,port=22,socketURL=<state>/vsock-22.sock \
    //      --device virtio-vsock,port=52,socketURL=<state>/vsock-52.sock
    cmd := exec.Command(b.cfg.VfkitBin, args...)

    // 3. Start process, save PID
    cmd.Start()
    // save PID, track in b.procs, start reaper goroutine
    // (same pattern as firecracker/client.go lines 218-232)

    // 4. Wait for VM to boot, discover IP
    //    Option A: parse vfkit output for DHCP-assigned IP
    //    Option B: have guest report IP over vsock
    //    Option C: deliver SSH key + env via cloud-init in rootfs

    // 5. Deliver dotenv + SSH keys
    //    Option A: bake into rootfs before boot (write files into the ext4 image)
    //    Option B: deliver via vsock after boot (small agent in guest)
    //    Option C: cloud-init in rootfs with NoCloud datasource

    return &vmbackend.VMInfo{
        ID:       cfg.ID,
        PID:      cmd.Process.Pid,
        IP:       vmIP,
        StateDir: vmDir,
    }, nil
}
```

### 4. How TaskManager Changes

Currently `tasks.go` directly calls `tm.fc.CreateVM()`. With the backend interface:

```go
// Before:
type TaskManager struct {
    daemon *Daemon
    fc     *firecracker.Client
}

// After:
type TaskManager struct {
    daemon  *Daemon
    backend vmbackend.Backend
}
```

The `CreateTask` method simplifies because platform-specific concerns (Tailscale, MMDS, TAP, DHCP, IP pool, static IP args) move into the backend or become conditional:

```go
func (tm *TaskManager) CreateTask(ctx context.Context, req *CreateTaskRequest) (*Task, error) {
    taskID := generateID()

    // Platform-agnostic: secrets, env
    env := buildEnv(req, tm.daemon.secrets)

    // Create VM via backend (backend handles rootfs, networking, metadata)
    vm, err := tm.backend.CreateVM(ctx, &vmbackend.VMConfig{
        ID:         taskID,
        VCPU:       req.CPUs,
        MemoryMB:   req.MemoryMB,
        SSHKeys:    req.SSHAuthorizedKeys,
        DotEnv:     req.DotEnv,
        Env:        env,
    })

    // Record task (no more CID, VsockPath, TailscaleHostname on macOS)
    task := &Task{
        ID:     taskID,
        Name:   req.Name,
        Status: "running",
        VMID:   vm.ID,
        IP:     vm.IP,  // <-- new: direct IP instead of Tailscale hostname
    }

    // ... rest unchanged
}
```

### 5. How `stockyard attach` Changes

```go
// Before (always Tailscale):
sshHost := task.TailscaleHostname

// After (use IP directly on macOS, Tailscale on Linux):
sshHost := task.TailscaleHostname
if sshHost == "" {
    sshHost = task.IP  // Direct IP on macOS
}
```

### 6. Config Changes

```json
{
  "backend": "vfkit",

  "vfkit": {
    "kernel_path": "/usr/local/share/stockyard/vmlinux",
    "rootfs_path": "/usr/local/share/stockyard/rootfs.img",
    "vfkit_bin": "/opt/homebrew/bin/vfkit"
  },

  "rootfs": {
    "provider": "apfs",
    "vms_dir": "/Users/mw/.stockyard/vms"
  },

  "firecracker": { ... },
  "zfs": { ... }
}
```

The `backend` field selects which implementation to use. Config sections for unused backends are ignored.

---

## What About the Guest Image?

The guest kernel + rootfs need to be arm64 for Apple Silicon. No reason to use Rosetta if we can just build natively.

- **Kernel:** Cross-compile for arm64 (same config, `ARCH=arm64 CROSS_COMPILE=aarch64-linux-gnu-`). The `vm-image/` scripts need an arm64 variant.
- **Rootfs:** Build arm64 rootfs. Could build on macOS itself inside a VM (bootstrap problem, but solvable — use Lima/vfkit to build the first image).
- **Guest binaries:** `stockyard-shell` and `stockyard-snapshot` already cross-compile (`GOOS=linux GOARCH=arm64`).

---

## What About IP Discovery?

**Virtualization.framework does NOT expose the VM's IP.** `VZNATNetworkDeviceAttachment` is a black box — no property, no callback.

**Best option: Parse `/var/db/dhcpd_leases`.** macOS's bootpd writes DHCP leases to this file. We assign the MAC at VM creation, then match it in the lease file. This is the same pattern as the existing dnsmasq lease parsing in `pkg/network/dhcp.go`, just a different file format. vfkit and Tart both use this approach.

The macOS lease file format (plist-ish):
```
{
  name=linux-vm
  ip_address=192.168.64.3
  hw_address=1,02:aa:bb:cc:dd:ee
  ...
}
```

Parse, match by MAC, done. No guest-side changes needed.

Fallbacks if lease file is slow to update: ARP cache (`arp -an`, match by MAC), or guest reports over vsock.

---

## What About Metadata Delivery?

**Don't bake files into the rootfs.** Too much work, fragile.

The flow is:
1. Boot VM with SSH keys injected (cloud-init NoCloud datasource in the rootfs, or kernel cmdline)
2. VM comes up with SSH accessible
3. `scp` dotenv, any other files the agent needs
4. SSH in, run agent

SSH keys are the only thing that needs to be in the image at boot time. Everything else goes over scp after the VM is up. This matches the existing create → rsync → ssh → rsync → destroy workflow.

---

## Migration Path

### Phase 1: Extract Interfaces (no behavior change)
1. Define `vmbackend.Backend` interface
2. Define `rootfs.Provisioner` interface
3. Wrap existing `firecracker.Client` to implement `vmbackend.Backend`
4. Wrap existing `zfs.Manager` to implement `rootfs.Provisioner`
5. Update `TaskManager` to use interfaces instead of concrete types
6. **All tests still pass. Linux behavior unchanged.**

### Phase 2: Implement macOS Backend
1. Implement `rootfs.APFSProvisioner` (~50 LOC)
2. Implement `vmbackend/vfkit.Backend` (~300-400 LOC)
3. Add `backend` config field, wire up in `daemon.New()`
4. Build arm64 guest image (or test with Rosetta)
5. Update `stockyard attach` / `stockyard logs` to use IP when no Tailscale hostname

### Phase 3: Polish
1. IP discovery mechanism (vsock-based)
2. DotEnv delivery without MMDS (vsock or rootfs injection)
3. Native arm64 guest image build
4. `stockyard run` on macOS skips Tailscale by default (add `--tailscale` flag to opt in)

---

## Effort Estimate

| Phase | Scope | Estimate |
|-------|-------|----------|
| Phase 1 (interfaces) | Refactor, no new features | 2-3 days |
| Phase 2 (macOS backend) | New code + integration | 3-5 days |
| Phase 3 (polish) | IP discovery, arm64 image, UX | 2-3 days |
| **Total** | | **~1-2 weeks** |

Phase 1 is pure refactoring and can be done incrementally without breaking anything. Phase 2 is where the macOS-specific work happens. Phase 3 is polish that can happen over time.

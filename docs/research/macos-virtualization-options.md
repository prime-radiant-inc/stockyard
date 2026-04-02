# macOS Virtualization Options for Agent Isolation

**Date:** 2026-03-31
**Author:** Lyra (Bob 0ebea785 / Opus 4.6)
**Status:** Research / Private

## Executive Summary

Stockyard currently uses Firecracker microVMs on Linux. Firecracker is KVM-dependent and will never run natively on macOS. This document surveys what's available on macOS for quickly running agents in relatively isolated environments.

**The headline findings:**

1. **Apple's Virtualization.framework is mature and production-proven.** It powers OrbStack, Docker Desktop, Lima, Tart, and Anthropic's own Claude Cowork. Go bindings exist (Code-Hex/vz). Sub-second Linux VM boot is achievable.

2. **Apple announced a Containerization framework at WWDC 2025** (macOS 26/Tahoe). It runs one lightweight VM per container with sub-second startup. Open source, Swift. Architecturally very similar to what Stockyard does with Firecracker.

3. **There is no Linux-container-equivalent isolation on macOS without a VM.** macOS lacks namespaces, cgroups, and seccomp. Every path to strong isolation requires a Linux kernel, which means a VM.

4. **The Go integration path is clear.** Code-Hex/vz provides comprehensive Go bindings for Virtualization.framework. vfkit (Red Hat) demonstrates the pattern. Vsock maps directly from Firecracker to Virtualization.framework.

5. **Stockyard is deeply coupled to Firecracker.** There is no abstraction layer. A macOS backend would require extracting a VM interface and implementing networking, filesystem, metadata, and guest communication differently.

---

## Table of Contents

- [1. The macOS Virtualization Stack](#1-the-macos-virtualization-stack)
- [2. Virtualization.framework Deep Dive](#2-virtualizationframework-deep-dive)
- [3. Projects Built on Virtualization.framework](#3-projects-built-on-virtualizationframework)
- [4. Non-VM Isolation on macOS](#4-non-vm-isolation-on-macos)
- [5. Firecracker Alternatives Survey](#5-firecracker-alternatives-survey)
- [6. Current Stockyard Architecture](#6-current-stockyard-architecture)
- [7. Integration Paths for Stockyard](#7-integration-paths-for-stockyard)
- [8. Recommendation](#8-recommendation)

---

## 1. The macOS Virtualization Stack

Apple provides two hypervisor APIs:

| Layer | API | Since | Purpose |
|-------|-----|-------|---------|
| Low-level | **Hypervisor.framework** | OS X 10.10 (2014) | Direct vCPU/memory management. Used by QEMU's HVF backend. |
| High-level | **Virtualization.framework** | macOS 11 (2020) | Complete VM lifecycle with virtio devices. The standard path. |

Virtualization.framework sits on top of Hypervisor.framework and provides a Swift/ObjC API for creating and managing VMs with virtio devices, VirtioFS file sharing, vsock communication, and more.

---

## 2. Virtualization.framework Deep Dive

### Evolution by macOS Version

| macOS | Year | Key Additions |
|-------|------|---------------|
| 11 Big Sur | 2020 | Initial release. Linux guests. Basic virtio devices. |
| 12 Monterey | 2021 | macOS guests (Apple Silicon). VirtioFS file sharing. |
| 13 Ventura | 2022 | **Rosetta in Linux VMs** (x86-64 translation). EFI boot. VirtioGPU 2D. |
| 14 Sonoma | 2023 | NVMe emulation. **VM state save/restore (snapshots)**. Network storage. |
| 15 Sequoia | 2024 | **Nested virtualization on M3+**. iCloud in macOS guests. |
| 26 Tahoe | 2025 | Apple Containerization framework built on top. |

### Capabilities

- **Guest OS:** Linux (arm64), macOS (arm64 on Apple Silicon). No Windows.
- **Rosetta:** Translates x86-64 Linux binaries at ~60-70% native speed. Transparent via binfmt_misc.
- **VirtioFS:** Shared directories. ~3x metadata overhead vs native, near-native sequential I/O. Docker measured 98% reduction in FS operation time vs their old 9p approach.
- **Networking:** NAT (no entitlement needed), bridged (needs entitlement or root). virtio-net.
- **vsock:** Host-guest communication without network stack. Maps directly to Stockyard's current vsock usage.
- **Storage:** virtio-block, NVMe (macOS 14+). Raw disk images.
- **Snapshots:** VM state save/restore since macOS 14. Could enable warm-pool patterns.

### Performance

| Metric | Virtualization.framework | Firecracker (Linux) |
|--------|--------------------------|---------------------|
| CPU overhead | 1-3% (hardware-assisted) | 1-3% (KVM) |
| Memory overhead per VM | <5 MiB achievable | <5 MiB |
| Boot time (minimal kernel) | Sub-second (proven by Apple Containerization) | ~125ms |
| Boot time (full distro) | 1-3 seconds | N/A (not designed for this) |
| VirtioFS overhead | ~3x metadata, near-native throughput | N/A (uses block devices) |

### Limitations

- **No GPU passthrough.** VirtioGPU 2D only for Linux guests. No Metal/OpenCL/CUDA.
- **No PCI passthrough** of any kind.
- **No nested virtualization on M1/M2.** M3+ with macOS 15+ only.
- **macOS guest limit:** Hard limit of 2 concurrent macOS VMs (Linux VMs: unlimited).
- **Entitlement required:** Binary must be signed with `com.apple.security.virtualization`.
- **VM lifetime tied to process.** If host process dies, VM dies.
- **No live migration.**

### API and Language Bindings

- **Native:** Swift and Objective-C
- **Go:** `github.com/Code-Hex/vz/v3` — production-ready, comprehensive, used by vfkit and Lima
- **Python:** PyObjC has auto-generated bindings

---

## 3. Projects Built on Virtualization.framework

### Most Relevant to Stockyard

#### Code-Hex/vz (Go bindings)
- **What:** Go bindings for Virtualization.framework via cgo/ObjC bridge
- **Maturity:** Production-ready. v3 current. Used by vfkit, Lima, Tart.
- **Coverage:** VM lifecycle, VirtioFS, vsock, networking, serial, block devices, boot loaders
- **Repo:** github.com/Code-Hex/vz

#### vfkit (Red Hat / CRC)
- **What:** Minimal CLI hypervisor wrapping Virtualization.framework. Written in Go using Code-Hex/vz.
- **Design:** One process per VM. CLI maps to Virtualization.framework API calls.
- **Go package:** `github.com/crc-org/vfkit/pkg/config` — programmatic API
- **Used by:** Podman Desktop, CRC (OpenShift Local)
- **Relevance:** Most directly comparable to what Stockyard would build. The "just give me a CLI to launch a VM" tool.

#### Apple Containerization (macOS 26)
- **What:** Apple's open-source framework for running Linux containers, each in its own lightweight VM.
- **Architecture:** One VM per container. Custom optimized Linux kernel. Sub-second startup. vminitd (Swift, static, musl) as PID 1. gRPC over vsock.
- **Repos:** `github.com/apple/containerization` (Swift package), `github.com/apple/container` (CLI)
- **Relevance:** Apple built exactly the pattern we want. But it's Swift-only and macOS 26+.

#### Lima
- **What:** CLI tool to launch Linux VMs. CNCF Incubating (promoted Oct 2025).
- **Backend:** vz (Virtualization.framework) preferred, QEMU fallback
- **AI focus:** v2.0+ has explicit AI agent sandboxing features, MCP tool integration
- **CLI:** `limactl create/start/shell/stop`. YAML config.
- **v2.1 (March 2026):** macOS guest support, enhanced AI agent safety
- **Relevance:** Could be used as a higher-level wrapper if we want to move fast.

### Other Notable Projects

| Project | Type | Notes |
|---------|------|-------|
| **OrbStack** | Commercial | Gold standard for Docker/Linux on Mac. 2s startup, 4x less power than Docker Desktop. Closed source. |
| **Tart** (Cirrus Labs) | CI-focused | Swift, OCI registry integration, Orchard orchestrator. Great for macOS CI. |
| **UTM** | GUI app | Wraps QEMU + Virtualization.framework. Not suitable for programmatic use. |
| **Colima** | CLI Docker alt | "Containers on Lima." Lightweight, no GUI. Uses vz driver. |
| **Docker Desktop** | Commercial | Deprecated QEMU on Apple Silicon (July 2025). Now uses Virtualization.framework exclusively. |

### Anthropic's Claude Cowork
Claude Cowork uses Virtualization.framework on macOS to run Claude Code inside sandboxed Linux VMs. Downloads a custom Linux rootfs, boots via VZVirtualMachine, mounts project directories via VirtioFS, controls the agent via MCP/vsock. **This is literally our use case in production.**

---

## 4. Non-VM Isolation on macOS

### The Fundamental Answer

**There is no way to get Linux-container-equivalent isolation on macOS without a VM.** macOS lacks the kernel primitives:

| Isolation Dimension | Linux | macOS |
|---------------------|-------|-------|
| Filesystem namespace | mount namespaces, overlayfs | Nothing (sandbox-exec file rules only) |
| Process namespace | PID namespaces | Nothing |
| Network namespace | network namespaces, veth | Nothing |
| Resource limits | cgroups v2 | Nothing |
| Syscall filtering | seccomp-bpf | sandbox-exec (deprecated, coarser) |
| User namespace | user namespaces | Nothing |

### What macOS CAN Do (Without a VM)

#### sandbox-exec (Seatbelt)
- **Status:** Deprecated since ~2016, but still works on current macOS. No replacement for CLI use.
- **Provides:** File/network ACLs via kernel-enforced SBPL profiles. Deny-by-default possible.
- **Does NOT provide:** Resource limits, PID isolation, network namespaces, filesystem layering.
- **Used by:** OpenAI Codex CLI, Chromium, Firefox, Agent Safehouse, ai-jail, SandVault.
- **Verdict:** Good for "don't let the agent read ~/.ssh" level protection. Not sufficient for true isolation.

#### User Account Isolation
- **Example:** Alcoholless (github.com/AkihiroSuda/alcless) — runs commands as a separate macOS user with a copied working directory.
- **Provides:** File permission separation. Near-instant startup.
- **Does NOT provide:** Network restriction, resource limits, process isolation.

#### Apple Endpoint Security Framework
- Could theoretically build a process jail (intercept/block file/network/exec operations).
- Requires System Extension, Apple Developer ID, notarization, user approval.
- Designed for enterprise security products (Jamf, SentinelOne), not lightweight sandboxing.
- **Verdict:** Impractical for our use case.

### Sandbox Tools for AI Agents (Available Today)

| Tool | Mechanism | Notes |
|------|-----------|-------|
| **Agent Safehouse** | sandbox-exec | Shell script, composable policies, works with Claude Code |
| **SandVault** | User isolation + sandbox-exec | Dual-layer, pre-configured for Claude Code |
| **ai-jail** | bwrap (Linux) / sandbox-exec (macOS) | Cross-platform wrapper |
| **Birdcage** | Seatbelt (macOS) / Landlock (Linux) | Rust library, embeddable |

These provide access control (what files/network the agent can touch) but NOT containment (resource limits, process isolation, filesystem snapshots).

---

## 5. Firecracker Alternatives Survey

### Why Firecracker Won't Work on macOS
Firecracker depends on Linux KVM at a deep level (rust-vmm crates, ioctl-based vCPU management, KVM-specific memory mapping). A PoC exists (issue #5017, Jan 2025) demonstrating boot on Apple Silicon, but the maintainers consider macOS support out of scope.

### Other VMMs

| VMM | macOS Status | Notes |
|-----|--------------|-------|
| **xhyve** | Dead | No Apple Silicon support. Unmaintained since ~2020. |
| **QEMU** | Works | HVF backend for hardware acceleration. No microvm on ARM. Heavyweight. Docker deprecated it. |
| **Cloud Hypervisor** | No | KVM/MSHV only. Rust bindings for Hypervisor.framework exist but unintegrated. |
| **crosvm** | No | KVM only. No macOS work. |
| **Kata Containers** | No | KVM only. Apple Containerization is the macOS spiritual successor. |

### Fastest Path to Linux Userspace on macOS

```
Apple Virtualization.framework
  -> VZLinuxBootLoader (direct kernel boot, no firmware/EFI)
  -> Minimal Linux kernel (virtio drivers built-in, stripped config)
  -> Minimal initramfs or direct root mount
  -> Tiny init process (communicate via vsock)
```

This is exactly what Apple Containerization does. Sub-second boot is proven.

---

## 6. Current Stockyard Architecture

### The Actual Workflow

The real user-facing flow is:

1. **Create** VM (daemon provisions rootfs clone, starts Firecracker, configures networking)
2. **rsync files in** to the VM (over SSH/network)
3. **SSH in**, run agent (claude, codex, serf, etc.)
4. **rsync files out**
5. **Destroy** VM

**Shell access is SSH**, typically over Tailscale (see `cmd/stockyard/attach.go` — execs `ssh` to `task.TailscaleHostname`). Logs are also streamed over SSH. SSH keys from `~/.ssh/*.pub` are injected at create time.

stockyard-shell (vsock port 52) is a lower-level terminal mechanism, not the primary user-facing access path.

### Firecracker Coupling

Stockyard has **no abstraction layer** between the daemon and Firecracker. The coupling map:

```
daemon.Daemon
  -> tasks.TaskManager
       -> firecracker.Client (HTTP REST API over Unix socket)
            -> firecracker.APIClient (/boot-source, /drives, /network-interfaces, etc.)
            -> firecracker.NetworkManager (TAP devices, bridges)
            -> firecracker.VMConfig, VMInfo, CloudInitConfig
  -> snapshots.SnapshotService (hardcoded vsock port 52000, CID-based addressing)
  -> metrics.MetricsPoller (Firecracker NDJSON format from FIFO)
  -> dashboard.Server (shell on vsock port 52)
```

### What's Firecracker-Specific vs Generic

| Component | Firecracker-Specific | Generic/Reusable |
|-----------|---------------------|------------------|
| VM lifecycle (create/start/stop/delete) | Yes — HTTP API to Firecracker process | - |
| Networking (TAP + bridge) | Yes — `ip tuntap add` | - |
| Filesystem (ZFS clone for rootfs) | Partially — ZFS is a separate manager | ZFS manager is clean |
| Guest communication (vsock CID) | Yes — CID allocation, port conventions | - |
| Metadata (MMDS) | Yes — Firecracker-specific API | - |
| Metrics (NDJSON FIFO) | Yes — Firecracker format | - |
| Secrets provider | - | Yes — pluggable interface |
| gRPC API | - | Yes — proto-defined, neutral |
| Guest binaries (shell, snapshot) | Mixed — vsock protocol is portable, CID addressing is not | - |

### MMDS: Easier to Replace Than It Looks

MMDS (Firecracker's metadata service at 169.254.169.254) carries:
- **Tailscale auth key / pre-registered state** (base64)
- **DotEnv file** (base64)
- SSH authorized keys
- Network config (static IP/gateway/DNS)
- Instance ID / hostname

The network config and hostname are trivially replaceable (kernel boot args). SSH keys can go through cloud-init or be baked into the image. The real payload is just Tailscale state and dotenv — both small blobs easily delivered over vsock or a shared file at boot.

### Networking: Also Easier Than It Looks

The Linux networking stack is three pieces:
1. **TAP device per VM** (`network.go`) — virtual ethernet port for Firecracker
2. **Bridge** (`flbr0`) — connects TAP devices + host on shared L2
3. **dnsmasq** (`dhcp.go`) — DHCP server handing out IPs, daemon reads lease file for MAC→IP mapping

On macOS with Virtualization.framework, all three are replaced by a single `VZNATNetworkDeviceAttachment`:
- Each VM gets an IP via built-in DHCP (no dnsmasq)
- Outbound internet via NAT (no bridge)
- No root/sudo needed (unlike TAP on Linux)

~400 lines of Linux plumbing → a few Go calls.

### Tailscale: Not Needed Locally on macOS

On Linux, Tailscale is essential — the daemon runs on a remote EC2 instance, and Tailscale lets you SSH into VMs without hopping through EC2. All user-facing access (`stockyard attach`, `stockyard logs`) goes through Tailscale hostnames.

**On macOS, the VMs are local.** You're already on the machine. SSH to the VM's NAT-assigned IP works directly. Tailscale becomes optional — only needed if you want to reach local VMs from another device.

This eliminates several concerns:
- No double-NAT / DERP relay issues
- No Tailscale auth key management for local VMs
- No pre-registration latency
- Simpler MMDS payload (just dotenv, if even that)

The `stockyard attach` flow on macOS would just SSH to the VM's local IP instead of a Tailscale hostname.

### Rootfs Provisioning: ZFS vs APFS clonefile

**ZFS on macOS is dead.** OpenZFS on OS X hasn't had a commit since 2020. Apple's kext deprecation killed it — DriverKit can't implement filesystems, and loading kexts on Apple Silicon requires Recovery Mode + Reduced Security. No maintained implementation exists.

**APFS `clonefile()` replaces the clone use case.** It creates instant, zero-copy, CoW file clones — same semantics as `zfs clone` but at the file level:

| | ZFS (Linux) | APFS clonefile (macOS) |
|--|-------------|----------------------|
| Create clone | `zfs clone tank/images/rootfs@base tank/vms/123` | `unix.Clonefile("base.img", "vm-123.img", 0)` |
| Destroy | `zfs destroy -r tank/vms/123` | `os.Remove("vm-123.img")` |
| Snapshot | `zfs snapshot tank/vms/123@checkpoint` | Copy file (or APFS snapshots via tmutil) |
| Snapshot diffs | `zfs diff` / `zfs send -i` (incremental streams) | No equivalent |
| Complexity | Pool setup, dataset hierarchy, permissions | One file per VM in a directory |
| Root required | Yes | No |

Go support: `golang.org/x/sys/unix.Clonefile(src, dst, 0)`. The macOS implementation would be ~50 lines of Go behind a `RootfsProvisioner` interface.

**Note:** ZFS snapshot diffs (`zfs diff`, incremental `zfs send`) have no APFS equivalent. If Stockyard moves toward snapshot-based file diffing (e.g., replacing rsync with snapshot diffs), that remains a Linux/production feature. The macOS backend doesn't need feature parity — it just needs create/destroy for local dev workflows.

Lima, OrbStack, and Docker Desktop all avoid ZFS on macOS. They use raw disk images on APFS or qcow2 (QEMU path, being deprecated).

### Guest-Side Services

- **stockyard-shell:** Interactive terminal via vsock port 52. Binary framed protocol (Open/Data/Resize/Exit/Error messages). Creates PTY sessions.
- **stockyard-snapshot:** ZFS snapshot requests via vsock port 52000.

Both use vsock for host-guest communication, which maps directly to Virtualization.framework's vsock support.

### What a VM Backend Interface Would Need

```go
type VMBackend interface {
    CreateVM(ctx context.Context, config *VMConfig) (*VMInfo, error)
    StartVM(ctx context.Context, config *VMConfig) (*VMInfo, error)
    StopVM(ctx context.Context, namespace, id string) error
    DeleteVM(ctx context.Context, namespace, id string) error
    GetVM(ctx context.Context, namespace, id string) (*VM, error)
    ListVMs(ctx context.Context, namespace string) ([]*VM, error)
    Close() error
}

type GuestConnection interface {
    Dial(ctx context.Context, port uint32) (net.Conn, error)
}
```

---

## 7. Integration Paths for Stockyard

### What Changes on macOS (and What Doesn't)

The actual workflow stays the same: create VM, rsync in, SSH in, run agent, rsync out, destroy.

What changes is the plumbing underneath:

| Concern | Linux (today) | macOS (target) | Difficulty |
|---------|---------------|----------------|------------|
| VM lifecycle | Firecracker HTTP API | Code-Hex/vz or vfkit | Medium |
| Networking | TAP + bridge + dnsmasq | `VZNATNetworkDeviceAttachment` | **Easy** — dramatically simpler |
| Rootfs provisioning | ZFS snapshot/clone | Disk image copy (APFS clonefile) | Medium |
| Metadata (MMDS) | Firecracker MMDS endpoint | Deliver dotenv over vsock or shared file | **Easy** — MMDS only carries Tailscale state + dotenv |
| SSH access | Over Tailscale hostname | Direct to VM's NAT IP | **Easy** — simpler than today |
| Tailscale | Required (remote EC2 host) | **Not needed** (VMs are local) | Eliminated |
| rsync | Over Tailscale/SSH | Over local SSH | Same mechanism, local network |
| Guest image | Same kernel + rootfs | Needs arm64 kernel build | Medium (cross-compile) |

### Path A: Direct Virtualization.framework via Code-Hex/vz

**Approach:** Use Code-Hex/vz Go bindings directly in stockyard. Build a new VM backend that creates VMs in-process.

| Aspect | Details |
|--------|---------|
| **Effort** | Medium-high. Extract VMBackend interface, implement VZ backend, replace ZFS with disk image copies, replace TAP with VZ NAT. |
| **Boot time** | Sub-second with optimized kernel |
| **Control** | Maximum. Full API access. |
| **Networking** | VZNATNetworkDeviceAttachment — VM gets IP, SSH works directly |
| **Rootfs** | Raw disk image, copy-per-VM (APFS clonefile for CoW if on APFS) |
| **Risk** | VM runs in-process (crash = daemon crash). Mitigate with vfkit subprocess model. |

### Path B: Wrap vfkit as Subprocess

**Approach:** Use vfkit (process-per-VM) as the macOS equivalent of the Firecracker binary. Drive it from Go via CLI or its Go package.

| Aspect | Details |
|--------|---------|
| **Effort** | Medium. Similar interface extraction, but vfkit handles VM lifecycle. |
| **Boot time** | Same as Path A (same underlying framework) |
| **Control** | Good. vfkit exposes most Virtualization.framework features. |
| **Crash isolation** | Better — VM crash only kills the vfkit process |
| **Maturity** | Battle-tested by Podman/CRC |

### Path C: Use Lima as Higher-Level Wrapper

**Approach:** Drive Lima's `limactl` CLI to create/manage VMs. Lima handles networking, port forwarding, cloud-init, etc.

| Aspect | Details |
|--------|---------|
| **Effort** | Low-medium. Lima handles the hard parts. |
| **Boot time** | 1-3 seconds (Lima adds overhead for cloud-init, SSH setup) |
| **Control** | Less direct. Lima's abstractions may not fit stockyard's model. |
| **AI agent features** | Lima v2.0+ explicitly supports this use case with MCP tools |
| **Risk** | Dependency on Lima's release cycle and design decisions |

### Path D: Target Apple Containerization (macOS 26+)

**Approach:** Use Apple's `container` CLI or Containerization Swift framework. OCI image per agent environment.

| Aspect | Details |
|--------|---------|
| **Effort** | Medium (if using CLI), high (if using Swift framework from Go via FFI) |
| **Boot time** | Sub-second (optimized by Apple) |
| **Isolation** | Best — Apple's own design, hardware-level per container |
| **Requirement** | macOS 26 (Tahoe). Not available on macOS 15/Sequoia. |
| **Maturity** | Very early. No Docker Compose equivalent. |
| **Future-proof** | Most likely to be Apple's long-term direction |

### Path E: Lightweight Sandbox Only (No VM)

**Approach:** Use sandbox-exec / Agent Safehouse style wrapping. No VM at all.

| Aspect | Details |
|--------|---------|
| **Effort** | Low |
| **Boot time** | Instant (same process) |
| **Isolation** | Weak — file/network ACLs only. No resource limits, no PID isolation. |
| **Use case** | "Good enough" for trusted agents during development |
| **Risk** | Deprecated API. No filesystem snapshots. Agent can consume unbounded resources. |

### Comparison Matrix

| | Boot Time | Isolation | Effort | macOS Version | Go Integration |
|--|-----------|-----------|--------|---------------|----------------|
| **A: vz direct** | <1s | VM (strong) | Med-High | 11+ | Native (cgo) |
| **B: vfkit** | <1s | VM (strong) | Medium | 11+ | CLI/package |
| **C: Lima** | 1-3s | VM (strong) | Low-Med | 11+ | CLI wrapper |
| **D: Apple Container** | <1s | VM (strong) | Medium | **26+** | CLI wrapper |
| **E: sandbox-exec** | 0ms | Weak | Low | Any | exec wrapper |

---

## 8. What We Built

### Path B (vfkit) — Implemented

We went with **vfkit as a subprocess** (Path B from the original options). This mirrors the Firecracker model (one process per VM) and provides crash isolation.

**Implementation:**
- `pkg/vmbackend/` — `Backend` interface with Firecracker adapter and vfkit implementation
- `pkg/rootfs/` — `Provisioner` interface with ZFS (Linux), APFS clonefile (macOS), and file copy (fallback)
- Alpine Linux rootfs built via Docker, with Kata Containers arm64 kernel
- SSH key injection via VirtioFS shared directory (no cloud-init)
- Kernel-level DHCP via `ip=dhcp` cmdline (IP at 0.2s, before init)
- IP discovery by parsing kernel console log

**Performance:**
- `stockyard run` to SSH Hello World: **~2.0s**
- Full lifecycle (create + command + destroy): **~3s**
- Kernel boot to init: 0.1s
- DHCP: 0.2s
- sshd ready: ~0.8s

### What the macOS Story Replaced

| Linux complexity | macOS equivalent |
|-----------------|-----------------|
| TAP + bridge + dnsmasq (~400 LOC) | vmnet NAT (zero config) |
| ZFS clone for rootfs | APFS clonefile (one syscall) |
| MMDS for metadata | VirtioFS shared directory |
| Tailscale for SSH access | Direct IP (VMs are local) |
| cloud-init for SSH keys | VirtioFS mount in guest |
| Custom Ubuntu rootfs + kernel | Alpine + Kata kernel (Docker build) |

### Future Optimization Paths

- **Pre-warmed VM pool** — VMs already booted, assign on demand (~100ms)
- **VM state save/restore** (macOS 14+) — resume from snapshot (~200ms)
- **Apple Containerization** (macOS 26+) — Apple's own lightweight VM-per-container framework

---

## Appendix: Key Repositories

| Repo | Language | Description |
|------|----------|-------------|
| [Code-Hex/vz](https://github.com/Code-Hex/vz) | Go | Go bindings for Virtualization.framework |
| [crc-org/vfkit](https://github.com/crc-org/vfkit) | Go | Minimal CLI hypervisor for macOS |
| [lima-vm/lima](https://github.com/lima-vm/lima) | Go | Linux VMs on macOS with file sharing |
| [apple/containerization](https://github.com/apple/containerization) | Swift | Apple's container framework |
| [apple/container](https://github.com/apple/container) | Swift | Apple's container CLI |
| [cirruslabs/tart](https://github.com/cirruslabs/tart) | Swift | macOS/Linux VM management for CI |
| [abiosoft/colima](https://github.com/abiosoft/colima) | Go | Containers on Lima |

## Appendix: Sources

Research conducted by four parallel agents covering:
- Apple Virtualization.framework capabilities and ecosystem
- macOS native isolation mechanisms (sandbox-exec, containers, Endpoint Security)
- Firecracker alternatives and microVM concepts on macOS
- Current Stockyard architecture and Firecracker coupling analysis

Key sources include Apple developer documentation, WWDC 2025 sessions, CNCF project announcements, Docker/OrbStack/Lima documentation, GitHub repositories, and technical blog posts from the macOS virtualization community.

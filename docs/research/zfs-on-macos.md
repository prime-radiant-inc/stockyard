# ZFS on macOS: State of Affairs (Early 2026)

**Date:** 2026-03-31
**Status:** Research
**Context:** Stockyard uses ZFS snapshots/clones on Linux to provision per-VM rootfs copies. This document evaluates whether ZFS is viable on macOS and what alternatives exist.

---

## 1. History of ZFS on macOS

### Apple's Own ZFS Project (2007-2009)

Apple began developing ZFS support for macOS in 2007, shipping read-only ZFS in Mac OS X 10.5 Leopard. They opened a ZFS project on Mac OS Forge, released a "ZFS Beta Seed v1.1" with read-write support, and advertised full ZFS support for Snow Leopard Server.

Then it all quietly died. In October 2009, Apple shut down the ZFS project on Mac OS Forge with no explanation and removed all but the CDDL-licensed portions of the code. Full ZFS support was pulled from Snow Leopard Server before release. The widely held theory is a combination of CDDL licensing incompatibility with Apple's goals and a strategic decision to build their own filesystem (which eventually became APFS, announced in 2016).

### MacZFS (2009-2013)

The community picked up Apple's abandoned code. MacZFS supported zpool version 8 and ZFS version 2. Development ceased in mid-2013 with a message directing users to switch to OpenZFS on OS X.

### OpenZFS on OS X / O3X (2013-2020)

O3X emerged as the successor, maintaining closer alignment with ZFS on Linux and illumos. It reached version 1.9.4 with features including LZ4 compression, deduplication, and ARC caching.

**The last commit to the openzfsonosx/zfs repository was February 18, 2020.** The project has been dormant for over six years. The README still says it is "tested primarily on macOS Mojave" (2018). There are 196 open issues and no activity.

### Current Status in OpenZFS Upstream

The official OpenZFS project (openzfs.org) lists supported platforms as: Alpine Linux, Arch Linux, Debian, Fedora, FreeBSD, Gentoo, NixOS, openSUSE, RHEL, Slackware, and Ubuntu. **macOS is not listed.** There is no macOS port in the upstream OpenZFS project.

**Bottom line: ZFS on macOS is dead. No maintained implementation exists.**

---

## 2. The Kernel Extension Problem

### Apple's Deprecation of Kexts

Apple deprecated kernel extensions starting with macOS Catalina (10.15). Since macOS Big Sur (11.0), kexts using deprecated KPIs no longer load by default. Apple's recommended alternatives:

| Legacy Technology | Modern Alternative |
|---|---|
| Filesystem kexts | **No direct replacement** |
| KAUTH | EndpointSecurity |
| Socket/Network Filter | NetworkExtension |
| IOHIDFamily | HIDDriverKit |
| IOUSBFamily | USBDriverKit |
| IOAudioFamily | AudioDriverKit |

The critical gap: **DriverKit and System Extensions cannot implement filesystems.** DriverKit is designed for hardware drivers (USB, storage controllers, HID, audio, networking). There is no "FileSystemDriverKit." Apple has not provided a modern replacement for filesystem kexts.

### Apple Silicon Restrictions

On Apple Silicon Macs, loading third-party kexts requires:
1. Booting into Recovery Mode
2. Using Startup Security Utility to set "Reduced Security" policy
3. Explicitly enabling kernel extensions
4. Rebooting and approving each kext in System Settings

This is a manual, per-machine process that cannot be automated or managed via MDM easily. Each macOS update can require re-approval. It is designed to be difficult -- Apple wants kexts to go away.

### macFUSE: The Surviving Kext

macFUSE (formerly OSXFUSE) is the one notable project still shipping a filesystem kext. As of December 2025, macFUSE 5.1.3 supports macOS 12 through macOS 26 including Apple Silicon. It uses a kernel extension as a bridge to run filesystem code in userspace.

However, macFUSE's kext is closed-source, and the project depends on Apple continuing to allow kexts at all. Even if someone revived OpenZFS on macOS, it would need either macFUSE or its own kext -- both paths are fragile.

**Bottom line: The kext deprecation makes filesystem-level innovation on macOS increasingly untenable. Even if ZFS were revived, it would be fighting Apple's platform direction.**

---

## 3. APFS as Alternative

### APFS Copy-on-Write Features

APFS, introduced in macOS 10.13 (2017), has several features relevant to our use case:

#### clonefile() -- Instant File Copies

The `clonefile(2)` system call creates a copy-on-write clone of a file or directory:

```c
int clonefile(const char *src, const char *dst, uint32_t flags);
int clonefileat(int src_dirfd, const char *src, int dst_dirfd, const char *dst, uint32_t flags);
int fclonefileat(int srcfd, int dst_dirfd, const char *dst, uint32_t flags);
```

**Behavior:**
- Creates an instant, zero-copy clone that shares data blocks with the original
- Subsequent writes to either file are private (copy-on-write at the block level)
- Atomic operation -- either fully succeeds or creates nothing
- Available since macOS 10.12 / iOS 10.0

**Flags:**
- `CLONE_NOFOLLOW` -- do not follow symlinks in source
- `CLONE_NOOWNERCOPY` -- do not copy ownership when running as root

**Constraints:**
- Source and destination must be on the same APFS volume
- Destination must not already exist
- The filesystem must support cloning (APFS does; HFS+ does not)
- Works on files and directories (directories are cloned recursively)

**For our use case:** `clonefile()` on a 4GB rootfs.ext4 file is near-instant and initially consumes zero additional disk space. As the VM writes to its copy, only modified blocks are allocated. This is functionally equivalent to ZFS clone for a single-file rootfs.

#### APFS Snapshots

APFS supports volume-level snapshots, managed via `diskutil apfs` subcommands:
- `diskutil apfs listSnapshots <volume>`
- `diskutil apfs deleteSnapshot <volume> -name <name>`

Snapshots are read-only, point-in-time captures of an entire APFS volume. They are used heavily by Time Machine. However:
- There is no public API for creating snapshots programmatically (only `tmutil localsnapshot` and `diskutil apfs`)
- You cannot clone a new writable volume from a snapshot (unlike ZFS `zfs clone pool/dataset@snap`)
- Snapshots are per-volume, not per-file or per-directory

**For our use case: APFS snapshots are not useful.** They operate at the wrong granularity (whole volume vs. individual files) and lack the clone-from-snapshot workflow that ZFS provides. `clonefile()` is the right APFS primitive.

### Programmatic API Summary

| Operation | API | Notes |
|---|---|---|
| Clone a file (CoW) | `clonefile(2)` | C syscall, available from any language via FFI. Go: `unix.Clonefile()` in `golang.org/x/sys/unix` |
| Clone a directory (CoW) | `clonefile(2)` | Works recursively on directories |
| Check if clone is supported | `getattrlist(2)` with `ATTR_VOL_CAPABILITIES` | Check `VOL_CAP_INT_CLONE` |
| Copy file (with CoW if available) | `copyfile(3)` with `COPYFILE_CLONE` flag | Falls back to regular copy on non-APFS |
| APFS volume snapshots | `diskutil apfs` CLI | No C API, not useful for per-file cloning |

---

## 4. Other CoW / Disk Image Options on macOS

### Sparse Disk Images

macOS supports two sparse image formats via `hdiutil`:

**SPARSE (UDSP):** Single-file sparse image that grows as data is written. Max capacity 128 PB. Grows in 1MB increments by default.

**SPARSEBUNDLE (UDSB):** Directory bundle containing 8MB "band" files. Max capacity ~8 EB. More reliable for persistent use; easier to back up incrementally.

Both allocate space on demand but are not true copy-on-write -- they do not support creating instant clones or branching from a base image. They are better suited for "allocate a large virtual disk that grows as needed" rather than "clone a base image per VM."

**For our use case: Not directly useful.** You could store a rootfs.ext4 inside a sparse image, but there is no advantage over just having the file on APFS and using `clonefile()`.

### qcow2 (QEMU Copy-on-Write)

qcow2 is QEMU's native disk image format with built-in copy-on-write via backing files:

```bash
# Create a base image
qemu-img create -f qcow2 base.qcow2 10G

# Create an overlay that references the base (instant, tiny file)
qemu-img create -f qcow2 -b base.qcow2 -F qcow2 overlay.qcow2
```

The overlay only stores modified blocks. Multiple overlays can share the same base. This is architecturally identical to ZFS clone for VM disk provisioning.

**However:** Apple's Virtualization.framework only supports raw disk images. It does not support qcow2. If using Virtualization.framework (the recommended path for macOS VMs), qcow2 is not an option -- you must use raw disk images.

QEMU on macOS does support qcow2, but Docker Desktop deprecated QEMU on Apple Silicon in July 2025 in favor of Virtualization.framework, signaling the industry direction.

---

## 5. What Lima / OrbStack / Docker Desktop Do

### Docker Desktop for Mac

Docker Desktop stores all container data in a single large disk image file at `~/Library/Containers/com.docker.docker/Data/vms/0/data`. It supports two formats:
- **Docker.raw** -- raw disk image, reclaims space quickly when images are deleted
- **Docker.qcow2** -- QEMU copy-on-write format, reclaims space via background process

As of mid-2025, Docker Desktop uses Virtualization.framework exclusively on Apple Silicon (QEMU deprecated). The single-VM model means Docker runs one Linux VM and uses Linux-native overlayfs inside it for container layer management. Docker does not use macOS-side CoW for per-container isolation.

### OrbStack

OrbStack runs a single shared Linux VM with a custom optimized kernel. Per-machine "Linux machines" likely share the kernel but have separate rootfs. OrbStack does not document its disk format publicly. File sharing uses VirtioFS with custom caching. The architecture is proprietary and closed-source.

### Lima

Lima is a Go-based VM manager that supports both QEMU and Virtualization.framework backends:
- With **QEMU backend**: Uses qcow2 images. The `qemuimgutil` package in Lima's codebase handles qcow2 operations, likely including backing files for overlays.
- With **VZ backend** (Virtualization.framework): Uses raw disk images, since that is all VZ supports.

Lima creates one VM per instance. Each instance gets its own disk image. There is no documented shared-base-image mechanism -- each VM's disk is independent.

### Podman

Uses `podman machine` to manage a Linux VM. Like Docker Desktop, runs containers inside a single Linux VM. Uses QEMU or Virtualization.framework depending on configuration.

### Summary

**None of these projects use ZFS on macOS.** They all run a Linux VM and handle container/image layering inside Linux using Linux-native mechanisms (overlayfs, device-mapper, etc.). For per-VM disk provisioning on the macOS side, the approach is either:
- qcow2 backing files (QEMU path, being deprecated)
- Independent raw disk image copies (Virtualization.framework path)

---

## 6. Practical Assessment for Stockyard

### Current Architecture (Linux)

Stockyard's ZFS workflow for VM provisioning:

1. Import `rootfs.ext4` into a ZFS dataset (`tank/stockyard/images/rootfs`)
2. Create a snapshot (`tank/stockyard/images/rootfs@base`)
3. For each VM, clone the snapshot (`zfs clone ...@base tank/stockyard/vms/<taskID>`)
4. The clone is instant, shares blocks with the base, and is writable
5. On VM destruction, destroy the clone dataset

This is clean and efficient. The clone is instant, uses minimal additional space, and provides full isolation.

### Best macOS Equivalent: APFS clonefile()

The direct translation for macOS using Virtualization.framework:

1. Store `rootfs.ext4` as a file on an APFS volume (the base image)
2. For each VM, `clonefile("base/rootfs.ext4", "vms/<taskID>/rootfs.ext4", 0)`
3. The clone is instant, shares blocks with the base, and is writable (CoW)
4. On VM destruction, `os.Remove("vms/<taskID>/rootfs.ext4")`

**Properties:**
- **Speed:** Near-instant clone creation (metadata operation only)
- **Space efficiency:** Initially zero additional space; grows only as VM writes blocks
- **Simplicity:** One syscall vs. ZFS dataset/snapshot/clone management
- **No special filesystem setup:** Works on any APFS volume (which is the default on all modern Macs)
- **No root required:** `clonefile()` works as a regular user
- **Go support:** `golang.org/x/sys/unix` provides `unix.Clonefile()`

### Implementation Sketch

```go
import "golang.org/x/sys/unix"

// Clone the base rootfs for a new VM
func cloneRootfs(baseImage, vmDir string) error {
    dst := filepath.Join(vmDir, "rootfs.ext4")
    return unix.Clonefile(baseImage, dst, 0)
}

// Destroy a VM's rootfs
func destroyRootfs(vmDir string) error {
    return os.Remove(filepath.Join(vmDir, "rootfs.ext4"))
}
```

Compare to the current ZFS path which requires `zfs clone`, `zfs get mountpoint`, `zfs destroy -r`, and a running ZFS pool with appropriate datasets configured.

### What You Lose vs. ZFS

| Feature | ZFS | APFS clonefile() |
|---|---|---|
| Instant clone from base | Yes (snapshot + clone) | Yes (clonefile) |
| Space-efficient CoW | Yes | Yes |
| Rollback to snapshot | Yes (`zfs rollback`) | No -- must re-clone from base |
| Multiple named snapshots per VM | Yes | No -- file-level only |
| Send/receive (replication) | Yes (`zfs send/recv`) | No |
| Checksumming / self-healing | Yes | No (APFS has checksums for metadata only) |
| Compression | Yes (lz4, zstd) | Yes (APFS transparent compression, less configurable) |
| In-guest ZFS snapshot coordination | Yes (stockyard-snapshot service) | N/A |

For the specific use case of "clone a base disk image per VM, use it, destroy it," `clonefile()` provides everything needed. The ZFS features that are lost (rollback, named snapshots, send/receive) are not used in the per-VM provisioning path -- they are used for the in-guest snapshot coordination service, which is a separate concern and would run inside the Linux VM anyway (using whatever filesystem the guest uses).

### Recommendation

**Use APFS `clonefile()` for macOS rootfs provisioning.** It is the natural, zero-dependency equivalent of ZFS clone for this use case. No kernel extensions, no special setup, no root access, one syscall.

The `pkg/zfs` package in Stockyard should be abstracted behind an interface:

```go
type RootfsProvisioner interface {
    // CloneBase creates a writable copy of the base rootfs for a VM
    CloneBase(ctx context.Context, taskID string) (rootfsPath string, err error)
    // Destroy removes a VM's rootfs copy
    Destroy(ctx context.Context, taskID string) error
}
```

With two implementations:
- `ZFSProvisioner` -- existing code, wraps `pkg/zfs` Manager
- `APFSProvisioner` -- new code, uses `unix.Clonefile()` for macOS

---

## Appendix: Key References

- OpenZFS on OS X repo (dormant): https://github.com/openzfsonosx/zfs (last commit Feb 2020)
- OpenZFS supported platforms: https://openzfs.github.io/openzfs-docs/Getting%20Started/index.html (macOS not listed)
- Apple kext deprecation: https://developer.apple.com/support/kernel-extensions/
- macFUSE: https://github.com/osxfuse/osxfuse (v5.1.3, Dec 2025, supports macOS 12-26)
- clonefile(2) man page: https://www.manpagez.com/man/2/clonefile/
- clonefile header: https://github.com/apple/darwin-xnu/blob/main/bsd/sys/clonefile.h
- hdiutil disk image formats: https://ss64.com/mac/hdiutil.html
- Apple Virtualization.framework: raw disk images only (Code-Hex/vz storage.go confirms)
- Docker Desktop disk storage: ~/Library/Containers/com.docker.docker/Data/vms/0/data

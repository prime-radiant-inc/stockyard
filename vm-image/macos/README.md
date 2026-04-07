# Stockyard macOS VM Image

Run stockyard VMs on macOS using Apple's Virtualization.framework via [vfkit](https://github.com/crc-org/vfkit).

## Quick Start

### Prerequisites

```bash
brew install vfkit e2fsprogs
```

Docker is also required (for building the Alpine rootfs image). [OrbStack](https://orbstack.dev/) or Docker Desktop both work.

### Build the VM image

```bash
./setup.sh
```

This downloads the Kata Containers arm64 kernel and builds an Alpine rootfs with:
- OpenSSH server (pre-generated host keys for instant startup)
- Node.js, Go, rsync, ripgrep, git, curl, bash
- VirtioFS mount for SSH key injection
- Kernel-level DHCP via `ip=dhcp` cmdline

Output goes to `./output/`:
- `vmlinux` — Kata arm64 kernel (12MB, virtio built-in, no initrd needed)
- `alpine-rootfs.raw` — Alpine ext4 rootfs (~374MB)

### Configure stockyard

```json
{
  "instance_id": "my-mac",
  "backend": "vfkit",
  "vfkit": {
    "kernel_path": "/path/to/vm-image/macos/output/vmlinux",
    "rootfs_path": "/path/to/vm-image/macos/output/alpine-rootfs.raw"
  },
  "rootfs": {
    "provider": "apfs",
    "base_image": "/path/to/vm-image/macos/output/alpine-rootfs.raw",
    "vms_dir": "/path/to/stockyard-data/vms"
  },
  "vm": {
    "user": "stockyard"
  },
  "secrets": {
    "provider": "file",
    "dir": "/path/to/stockyard-data/secrets"
  },
  "daemon": {
    "socket_path": "/path/to/stockyard-data/stockyard.sock",
    "data_dir": "/path/to/stockyard-data"
  }
}
```

### Run

```bash
stockyardd &                              # Start daemon
stockyard run --no-tailscale --name test  # Create VM
stockyard attach <task-id>                # SSH in
stockyard destroy --force <task-id>       # Tear down
```

## Architecture

```
macOS host
  └── stockyardd (daemon)
        └── vfkit (one process per VM)
              └── Virtualization.framework
                    └── Alpine Linux arm64 VM
                          ├── Kata kernel (virtio built-in, ip=dhcp)
                          ├── OpenRC (minimal init)
                          ├── sshd (pre-baked host keys)
                          └── VirtioFS mount (/mnt/stockyard → host shared dir)
```

### How it works

1. **Create:** APFS `clonefile()` copies the base rootfs image (instant, copy-on-write)
2. **Boot:** vfkit spawns with Kata kernel + direct boot (no firmware, no initrd needed)
3. **Network:** Kernel gets IP via DHCP at ~0.2s (built-in DHCP client, vmnet NAT)
4. **SSH keys:** Injected via VirtioFS shared directory (host writes `authorized_keys`, guest mounts it)
5. **SSH ready:** ~1.2s after vfkit launch
6. **Destroy:** SIGKILL vfkit process + remove rootfs clone (~30ms)

### Performance

| Metric | Time |
|--------|------|
| `stockyard run` (create task) | ~0.7s |
| SSH ready (from vfkit launch) | ~1.2s |
| **Total to SSH Hello World** | **~2.0s** |
| Destroy | ~0.03s |
| Full lifecycle (create + command + destroy) | ~3s |

### Differences from Linux (Firecracker)

| | Linux (Firecracker) | macOS (vfkit) |
|--|---------------------|---------------|
| Hypervisor | Firecracker (KVM) | vfkit (Virtualization.framework) |
| Rootfs provisioning | ZFS clone | APFS clonefile |
| Networking | TAP + bridge + dnsmasq | vmnet NAT (built-in DHCP) |
| SSH access | Tailscale hostname | Direct IP |
| Metadata delivery | MMDS (cloud-init) | VirtioFS shared directory |
| Guest OS | Ubuntu (custom image) | Alpine Linux |
| Kernel | Custom x86_64 | Kata arm64 |

## Customizing the image

Edit `Dockerfile.alpine` to add packages or change configuration, then rebuild:

```bash
./build-rootfs.sh
```

The rootfs is built by:
1. Docker builds an Alpine arm64 image with all packages
2. `docker export` extracts the filesystem
3. `.dockerenv` is removed (so OpenRC doesn't run in container-degraded mode)
4. `mkfs.ext4 -d` creates an ext4 image from the directory

### Key files in the image

- `/etc/ssh/sshd_config.d/stockyard.conf` — sshd reads keys from VirtioFS mount
- `/etc/init.d/stockyard-mount` — OpenRC script to mount VirtioFS at boot
- `/etc/network/interfaces` — `eth0` set to `manual` (kernel handles DHCP)

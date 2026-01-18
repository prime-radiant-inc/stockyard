# Stockyard VM Image

Ubuntu 24.04-based VM image for Firecracker, configured for Claude Code sessions.

## Building

### Prerequisites

- Docker
- Go 1.22+
- Root access (for rootfs conversion)

### Quick Start

```bash
make
```

This builds the Docker image and converts it to a Firecracker rootfs.

### Individual Targets

```bash
make docker   # Build Docker image only
make rootfs   # Convert to rootfs (requires sudo)
make clean    # Remove build artifacts
make help     # Show all options
```

Output:
- `output/rootfs.ext4` (~8GB) - Root filesystem
- `output/vmlinux.bin` (~45MB) - Firecracker kernel

### Configuration Options

```bash
# Custom VM user (default: mooby)
VM_USER=myuser ./build.sh

# Custom image name/tag
IMAGE_NAME=my-vm IMAGE_TAG=v1 ./build.sh

# Custom rootfs size
ROOTFS_SIZE=20G sudo ./convert-to-rootfs.sh
```

## What's Included

### Languages & Runtimes
- **Python 3** with uv (modern package manager)
- **Node.js 20** (LTS)
- **Go 1.22**
- **Rust** (via rustup)
- **C/C++** (clang, cmake, build-essential)

### AI Coding Assistants
- **Claude Code** (Anthropic)
- **Codex** (OpenAI)

### Cloud CLIs
- **AWS CLI v2**
- **Azure CLI**
- **Google Cloud CLI**
- **GitHub CLI** (gh)

### Linters & Formatters
- **Go**: golangci-lint
- **Python**: ruff
- **JavaScript/TypeScript**: eslint, prettier, typescript
- **C/C++**: clang-format, clang-tidy

### System Tools
- git, tmux, vim, jq, yq
- Tailscale (userspace mode for Firecracker)

### User
- `mooby` with passwordless sudo (configurable via `VM_USER`)

## Kernel

The image includes the **official Firecracker 6.1 LTS kernel** from the AWS CI bucket:

| Property | Value |
|----------|-------|
| Version | 6.1.155 |
| URL | `https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.14/x86_64/vmlinux-6.1.155` |
| SHA256 | `e41c7048bd2475e7e788153823fcb9166a7e0b78c4c443bd6446d015fa735f53` |

The checksum is verified during Docker build. To upgrade the kernel, update `KERNEL_VERSION` and `KERNEL_SHA256` in the Dockerfile.

**Why 6.1?**
- Latest LTS kernel with Firecracker support
- Proper virtio drivers for disk and network
- Required for modern Go programs (including tailscaled)
- The older 4.14 quickstart kernel causes Go runtime issues

**Installation:**
```bash
sudo cp output/vmlinux.bin /var/lib/stockyard/vmlinux.bin
```

**Note:** tailscaled uses userspace networking mode (`--tun=userspace-networking`) since the Firecracker kernel configuration doesn't include TUN device support. This is configured in `/etc/default/tailscaled`.

## VM Configuration

The image expects metadata via Firecracker MMDS at `169.254.169.254`:

- `meta-data/local-hostname` - Sets VM hostname
- `meta-data/tailscale-auth-key` - Connects to Tailscale

See `init/stockyard-init.sh` for the initialization process.

## ZFS Storage (Production)

In production, the rootfs is stored on ZFS for efficient copy-on-write cloning:

```
tank/stockyard/
├── images/rootfs/           # Base rootfs dataset
│   └── rootfs.ext4          # The actual file
│       @base                # Snapshot for cloning
└── vms/{vmID}/              # Per-VM clones
    └── rootfs.ext4          # CoW copy (~1MB overhead per VM)
```

**Benefits:**
- Instant VM creation (clone vs 4GB file copy)
- 10 VMs use ~1.5GB instead of ~40GB
- Automatic cleanup on VM deletion

The daemon auto-imports the base image on first startup from `Firecracker.RootfsPath` config.

### Initial Setup

After building, install both the rootfs and kernel:

```bash
# Build the image
make

# Install rootfs to ZFS (for copy-on-write cloning)
sudo cp output/rootfs.ext4 /tank/stockyard/images/rootfs/rootfs.ext4
sudo zfs snapshot tank/stockyard/images/rootfs@base

# Install kernel
sudo cp output/vmlinux.bin /var/lib/stockyard/vmlinux.bin
```

### Updating

When rebuilding the image, destroy the old snapshot first:

```bash
make
sudo zfs destroy tank/stockyard/images/rootfs@base
sudo cp output/rootfs.ext4 /tank/stockyard/images/rootfs/rootfs.ext4
sudo zfs snapshot tank/stockyard/images/rootfs@base
sudo cp output/vmlinux.bin /var/lib/stockyard/vmlinux.bin
```

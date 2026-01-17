# Stockyard VM Image

Ubuntu 24.04-based VM image for Firecracker, configured for Claude Code sessions.

## Building

### Prerequisites

- Docker
- Go 1.22+
- Root access (for rootfs conversion)

### Steps

```bash
# 1. Build Docker image (includes stockyard-snapshot binary)
./build.sh

# 2. Convert to Firecracker rootfs (requires sudo)
sudo ./convert-to-rootfs.sh
```

Output: `output/rootfs.ext4` (~4GB)

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

- **Languages**: Python 3, Node.js 20, Go 1.22, Rust
- **Tools**: git, gh (GitHub CLI), Claude Code
- **Networking**: Tailscale (userspace mode for Firecracker)
- **User**: `mooby` with passwordless sudo (configurable via `VM_USER`)

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

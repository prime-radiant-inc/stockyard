# Stockyard Development Guide

## Building

```bash
make build          # Build all binaries to bin/
make build-guest    # Build guest binaries for VM (static Linux binaries)
```

## Deploying (Linux)

```bash
make deploy-daemon  # Build, install, and restart daemon via systemctl
make deploy-image   # Build and deploy VM image
make deploy         # Full deployment (daemon + image)
```

## macOS Setup

```bash
brew install vfkit e2fsprogs
./vm-image/macos/setup.sh        # Download kernel + build Alpine rootfs
```

See `vm-image/macos/README.md` for full instructions.

## Testing

```bash
make test           # Run all tests
go test ./pkg/...   # Run package tests
```

## Project Structure

- `cmd/stockyard/` - CLI client
- `cmd/stockyardd/` - Daemon
- `cmd/stockyard-shell/` - Shell for VM (runs inside guest)
- `cmd/stockyard-snapshot/` - ZFS snapshot coordinator (runs inside guest)
- `pkg/daemon/` - Daemon core logic
- `pkg/dashboard/` - Web dashboard and websocket server
- `pkg/firecracker/` - Firecracker VM management (Linux)
- `pkg/vmbackend/` - VM backend interface + implementations (Firecracker, vfkit)
- `pkg/rootfs/` - Rootfs provisioner interface + implementations (ZFS, APFS, copy)
- `vm-image/` - VM image build scripts (Linux: Docker-based, macOS: Alpine + Kata kernel)

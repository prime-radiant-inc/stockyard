# Stockyard Development Guide

## Building

```bash
make build          # Build all binaries to bin/
make build-shell    # Build stockyard-shell for VM (static Linux binary)
```

## Deploying

```bash
make deploy-daemon  # Build, install, and restart daemon via systemctl
make deploy-image   # Build and deploy VM image
make deploy         # Full deployment (daemon + image)
```

## Testing

```bash
make test           # Run all tests
go test ./pkg/...   # Run package tests
```

## Project Structure

- `cmd/stockyard/` - CLI client
- `cmd/stockyardd/` - Daemon
- `cmd/stockyard-shell/` - Shell for VM (runs inside guest)
- `pkg/daemon/` - Daemon core logic
- `pkg/dashboard/` - Web dashboard and websocket server
- `pkg/firecracker/` - Firecracker VM management
- `vm-image/` - VM image build scripts

# Stockyard Development Guide

## Building

```bash
make build          # Build all binaries to bin/
make build-guest    # Build guest binaries for VM (static Linux binaries)
```

## Testing

```bash
make test           # Package tests (./pkg/... only)
go test ./cmd/...   # CLI tests — NOT covered by make test or CI; run them
```

## Deploying VM Images

There are no root-Makefile deploy targets, and image deploys never restart
the daemon: both targets below register through `stockyard image import`,
and the daemon replaces images live (destroying only tasks on the replaced
image).

```bash
make -C vm-image deploy                              # Linux: build + register the default Firecracker image
make -C vm-image deploy-image REGISTRY_IMAGE=<name>  # Linux: register an already-built rootfs under a name
make container-image                                 # macOS: build the Apple container OCI image
                                                     #        (stockyard.local/stockyard-vm:container)
```

## Config Semantics Worth Knowing

- `firecracker.rootfs_path` — the seed for the image registry's `default`
  image (startup self-heal); also gates Firecracker backend construction.
- `apple_container.image` — the daemon's default task image on macOS,
  overridable per task with `stockyard run --image`.

## Project Structure

- `cmd/stockyard/` - CLI client
- `cmd/stockyardd/` - Daemon
- `cmd/stockyard-shell/` - Shell for VM (runs inside guest)
- `cmd/stockyard-snapshot/` - ZFS snapshot coordinator (runs inside guest)
- `pkg/daemon/` - Daemon core logic
- `pkg/dashboard/` - Web dashboard and websocket server
- `pkg/firecracker/` - Firecracker VM management (Linux)
- `pkg/vmbackend/` - VM backend interface + implementations (Firecracker, apple-container)
- `vm-image/` - VM image build scripts (Linux: Docker-based)

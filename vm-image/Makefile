# Stockyard VM Image Build
#
# Usage:
#   make                       - Build everything (docker image + rootfs)
#   make docker                - Build Docker image only
#   make rootfs                - Convert to Firecracker rootfs (requires sudo)
#   make deploy                - Build + register default image via `stockyard image import`
#   make deploy-image          - Register the already-built output/rootfs.ext4 (no build step)
#   make deploy-image REGISTRY_IMAGE=foo  - Register under a custom name
#   make clean                 - Remove build artifacts
#   make help                  - Show this help

.PHONY: all build-deps docker rootfs deploy deploy-image clean help docker-alpine rootfs-alpine deploy-alpine container-image

# Deployment paths
INSTALL_DIR := /var/lib/stockyard
# REGISTRY_IMAGE: the name used in `stockyard image import`.
# Set to "default" for the canonical base image, or any other name for named
# images (e.g. REGISTRY_IMAGE=alpine for the Alpine variant).
# Do NOT use IMAGE_NAME here — build.sh and convert-to-rootfs.sh own that variable
# for the Docker image name and make exports command-line vars to sub-shells.
REGISTRY_IMAGE ?= default
STOCKYARD_BIN ?= $(abspath ..)/bin/stockyard

# Default target: build everything
all: rootfs

# Build Docker image (stockyard-snapshot is built by build.sh)
docker:
	@./build.sh

# Convert to Firecracker rootfs (depends on docker image)
rootfs: docker
	@sudo ./convert-to-rootfs.sh

# Install the already-built output/rootfs.ext4 under the registry name.
# No build prerequisite on purpose: deploy and deploy-alpine choose what to
# build; this target only installs and registers the result.
# The daemon handles orderly replacement (destroys only tasks on the replaced
# image, then re-imports) — no need to stop/restart the daemon or touch ZFS.
deploy-image:
	@echo "=== Deploying image '$(REGISTRY_IMAGE)' ==="
	sudo cp output/vmlinux.bin $(INSTALL_DIR)/vmlinux.bin
	sudo cp output/rootfs.ext4 $(INSTALL_DIR)/rootfs-$(REGISTRY_IMAGE).ext4
ifeq ($(REGISTRY_IMAGE),default)
	sudo cp output/rootfs.ext4 $(INSTALL_DIR)/rootfs.ext4  # legacy path: config rootfs_path, startup self-heal
endif
	sudo $(STOCKYARD_BIN) image import $(REGISTRY_IMAGE) --rootfs $(INSTALL_DIR)/rootfs-$(REGISTRY_IMAGE).ext4
	@echo "=== Deployment Complete ==="

# Build and deploy the default (Ubuntu) image.
deploy: rootfs
	$(MAKE) deploy-image

# ============================================================================
# Alpine variant targets (Linux Firecracker — separate from the default Ubuntu
# rootfs path. Uses Dockerfile.alpine + VARIANT=alpine ./build.sh.)
# ============================================================================

# Build Alpine Docker image
docker-alpine:
	@VARIANT=alpine ./build.sh

# Convert Alpine image to Firecracker rootfs (depends on docker image)
rootfs-alpine: docker-alpine
	@sudo IMAGE_TAG=alpine ./convert-to-rootfs.sh

# Build and deploy the Alpine variant image.
deploy-alpine: rootfs-alpine
	$(MAKE) deploy-image REGISTRY_IMAGE=alpine

# Build the Apple `container` OCI image (arm64), tagged
# stockyard.local/$(IMAGE_NAME or stockyard-vm):container. Requires Docker + BuildKit.
container-image:
	@TARGET=container ./build.sh

# Remove build artifacts
clean:
	rm -rf output/
	@echo "Cleaned output directory"

# Show help
help:
	@echo "Stockyard VM Image Build"
	@echo ""
	@echo "Targets:"
	@echo "  make                            Build everything (docker + rootfs)"
	@echo "  make docker                     Build Docker image only"
	@echo "  make rootfs                     Convert to Firecracker rootfs (requires sudo)"
	@echo "  make deploy                     Build + register default image (requires sudo)"
	@echo "  make deploy-image               Register output/rootfs.ext4 as REGISTRY_IMAGE (no build step)"
	@echo "  make deploy-image REGISTRY_IMAGE=foo  Register under a custom name"
	@echo "  make docker-alpine              Build Alpine-variant Docker image (Linux Firecracker)"
	@echo "  make rootfs-alpine              Convert Alpine image to Firecracker rootfs (requires sudo)"
	@echo "  make deploy-alpine              Build + register Alpine image (requires sudo)"
	@echo "  make container-image            Build the Apple container OCI image (arm64, tagged stockyard.local/stockyard-vm:container)"
	@echo "  make clean                      Remove build artifacts"
	@echo "  make help                       Show this help"
	@echo ""
	@echo "Deploy process (make deploy / make deploy-image):"
	@echo "  1. Copy vmlinux.bin and rootfs.ext4 to $(INSTALL_DIR)"
	@echo "  2. Run: stockyard image import <REGISTRY_IMAGE> --rootfs ..."
	@echo "  The daemon handles replacement atomically: it destroys only the tasks"
	@echo "  running the replaced image, then re-imports. No daemon restart needed."
	@echo ""
	@echo "Configuration (environment variables):"
	@echo "  REGISTRY_IMAGE   Registry name for this image (default: default)"
	@echo "  STOCKYARD_BIN    Path to stockyard binary (default: ../bin/stockyard)"
	@echo "  VM_USER          VM username (default: mooby)"
	@echo "  IMAGE_NAME       Docker image name (default: stockyard-vm) — owned by build.sh; the container target tags it stockyard.local/<IMAGE_NAME>:container"
	@echo "  IMAGE_TAG        Docker image tag (default: latest) — owned by convert-to-rootfs.sh"
	@echo "  ROOTFS_SIZE      Rootfs size (default: 10G)"
	@echo ""
	@echo "Output: output/rootfs.ext4, output/vmlinux.bin"

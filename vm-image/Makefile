# Stockyard VM Image Build
#
# Usage:
#   make          - Build everything (docker image + rootfs)
#   make docker   - Build Docker image only
#   make rootfs   - Convert to Firecracker rootfs (requires sudo)
#   make deploy   - Full deploy: build, stop daemon, install, restart (requires sudo)
#   make clean    - Remove build artifacts
#   make help     - Show this help

.PHONY: all build-deps docker rootfs deploy clean help docker-alpine rootfs-alpine deploy-alpine

# Deployment paths
INSTALL_DIR := /var/lib/stockyard
ZFS_SNAPSHOT := tank/stockyard/images/rootfs@base

# Default target: build everything
all: rootfs

# Build Docker image (stockyard-snapshot is built by build.sh)
docker:
	@./build.sh

# Convert to Firecracker rootfs (depends on docker image)
rootfs: docker
	@sudo ./convert-to-rootfs.sh

# Full deployment pipeline (requires sudo)
# This is the COMPLETE process - no steps should be skipped
# WARNING: This destroys all existing VMs to deploy fresh image
deploy: rootfs
	@echo ""
	@echo "=== Deploying to $(INSTALL_DIR) ==="
	@echo ""
	@echo "Step 1: Stopping stockyardd daemon..."
	sudo systemctl stop stockyardd
	@echo ""
	@echo "Step 2: Killing any remaining firecracker processes..."
	@sudo pkill -9 firecracker 2>/dev/null || true
	@sleep 1
	@echo ""
	@echo "Step 3: Copying rootfs.ext4..."
	sudo cp output/rootfs.ext4 $(INSTALL_DIR)/rootfs.ext4
	@echo ""
	@echo "Step 4: Copying vmlinux.bin..."
	sudo cp output/vmlinux.bin $(INSTALL_DIR)/vmlinux.bin
	@echo ""
	@echo "Step 5: Destroying ZFS base snapshot and VM clones..."
	@# Use -R to recursively destroy dependent clones (VM filesystems)
	@if sudo zfs list -t snapshot $(ZFS_SNAPSHOT) >/dev/null 2>&1; then \
		echo "Destroying $(ZFS_SNAPSHOT) and dependent clones..."; \
		sudo zfs destroy -R $(ZFS_SNAPSHOT) && echo "Destroyed $(ZFS_SNAPSHOT)" || \
		echo "WARNING: Failed to destroy snapshot (may have active mounts)"; \
	else \
		echo "Snapshot $(ZFS_SNAPSHOT) does not exist, skipping destroy"; \
	fi
	@echo ""
	@echo "Step 6: Restarting stockyardd daemon..."
	sudo systemctl start stockyardd
	@sleep 2
	@echo ""
	@echo "Step 7: Verifying daemon is running..."
	@if systemctl is-active --quiet stockyardd; then \
		echo "stockyardd is running"; \
		systemctl status stockyardd --no-pager | head -5; \
	else \
		echo "ERROR: stockyardd failed to start!"; \
		systemctl status stockyardd --no-pager; \
		exit 1; \
	fi
	@echo ""
	@echo "=== Deployment Complete ==="

# ============================================================================
# Alpine variant targets
# ============================================================================

# Build Alpine Docker image
docker-alpine:
	@VARIANT=alpine ./build.sh

# Convert Alpine image to Firecracker rootfs (depends on docker image)
rootfs-alpine: docker-alpine
	@sudo IMAGE_TAG=alpine ./convert-to-rootfs.sh

# Full Alpine deployment pipeline (requires sudo)
deploy-alpine: rootfs-alpine
	@echo ""
	@echo "=== Deploying Alpine image to $(INSTALL_DIR) ==="
	@echo ""
	@echo "Step 1: Stopping stockyardd daemon..."
	sudo systemctl stop stockyardd
	@echo ""
	@echo "Step 2: Killing any remaining firecracker processes..."
	@sudo pkill -9 firecracker 2>/dev/null || true
	@sleep 1
	@echo ""
	@echo "Step 3: Copying rootfs.ext4..."
	sudo cp output/rootfs.ext4 $(INSTALL_DIR)/rootfs.ext4
	@echo ""
	@echo "Step 4: Copying vmlinux.bin..."
	sudo cp output/vmlinux.bin $(INSTALL_DIR)/vmlinux.bin
	@echo ""
	@echo "Step 5: Destroying ZFS base snapshot and VM clones..."
	@if sudo zfs list -t snapshot $(ZFS_SNAPSHOT) >/dev/null 2>&1; then \
		echo "Destroying $(ZFS_SNAPSHOT) and dependent clones..."; \
		sudo zfs destroy -R $(ZFS_SNAPSHOT) && echo "Destroyed $(ZFS_SNAPSHOT)" || \
		echo "WARNING: Failed to destroy snapshot (may have active mounts)"; \
	else \
		echo "Snapshot $(ZFS_SNAPSHOT) does not exist, skipping destroy"; \
	fi
	@echo ""
	@echo "Step 6: Restarting stockyardd daemon..."
	sudo systemctl start stockyardd
	@sleep 2
	@echo ""
	@echo "Step 7: Verifying daemon is running..."
	@if systemctl is-active --quiet stockyardd; then \
		echo "stockyardd is running"; \
		systemctl status stockyardd --no-pager | head -5; \
	else \
		echo "ERROR: stockyardd failed to start!"; \
		systemctl status stockyardd --no-pager; \
		exit 1; \
	fi
	@echo ""
	@echo "=== Alpine Deployment Complete ==="

# Remove build artifacts
clean:
	rm -rf output/
	@echo "Cleaned output directory"

# Show help
help:
	@echo "Stockyard VM Image Build"
	@echo ""
	@echo "Targets:"
	@echo "  make          Build everything (docker + rootfs)"
	@echo "  make docker   Build Docker image only"
	@echo "  make rootfs   Convert to Firecracker rootfs (requires sudo)"
	@echo "  make deploy   Full deploy: build + install + restart daemon (requires sudo)"
	@echo "  make clean    Remove build artifacts"
	@echo "  make help     Show this help"
	@echo ""
	@echo "Deploy process (make deploy):"
	@echo "  1. Build Docker image"
	@echo "  2. Convert to rootfs"
	@echo "  3. Stop stockyardd daemon"
	@echo "  4. Kill any remaining firecracker processes"
	@echo "  5. Copy rootfs.ext4 and vmlinux.bin to $(INSTALL_DIR)"
	@echo "  6. Destroy ZFS base snapshot AND all VM clones (DESTRUCTIVE!)"
	@echo "  7. Restart stockyardd daemon"
	@echo "  8. Verify daemon is running"
	@echo ""
	@echo "WARNING: 'make deploy' destroys all existing VMs!"
	@echo ""
	@echo "Configuration (environment variables):"
	@echo "  VM_USER       VM username (default: mooby)"
	@echo "  IMAGE_NAME    Docker image name (default: stockyard-vm)"
	@echo "  IMAGE_TAG     Docker image tag (default: latest)"
	@echo "  ROOTFS_SIZE   Rootfs size (default: 10G)"
	@echo ""
	@echo "Output: output/rootfs.ext4, output/vmlinux.bin"

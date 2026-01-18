# Stockyard VM Image Build
#
# Usage:
#   make          - Build everything (docker image + rootfs)
#   make docker   - Build Docker image only
#   make rootfs   - Convert to Firecracker rootfs (requires sudo)
#   make clean    - Remove build artifacts
#   make help     - Show this help

.PHONY: all docker rootfs clean help build-deps

# Build dependencies (shell binary)
build-deps:
	$(MAKE) -C .. build-shell

# Default target: build everything
all: rootfs

# Build Docker image
docker: build-deps
	@./build.sh

# Convert to Firecracker rootfs (depends on docker image)
rootfs: docker
	@sudo ./convert-to-rootfs.sh

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
	@echo "  make clean    Remove build artifacts"
	@echo "  make help     Show this help"
	@echo ""
	@echo "Configuration (environment variables):"
	@echo "  VM_USER       VM username (default: mooby)"
	@echo "  IMAGE_NAME    Docker image name (default: stockyard-vm)"
	@echo "  IMAGE_TAG     Docker image tag (default: latest)"
	@echo "  ROOTFS_SIZE   Rootfs size (default: 10G)"
	@echo ""
	@echo "Output: output/rootfs.ext4"

#!/bin/bash
# Test VM creation with Flintlock
# Run with: sudo ./scripts/test-vm-create.sh

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

if [ "$EUID" -ne 0 ]; then
    error "Please run with sudo"
fi

# Check flintlockd is running
if ! nc -z localhost 9090 2>/dev/null; then
    error "Flintlock is not running on port 9090. Start with: sudo ./scripts/start-flintlock.sh"
fi

log "Flintlock is running"

# Import Docker image to containerd
IMAGE_NAME="docker.io/library/stockyard-vm:latest"
log "Importing Docker image to containerd..."

# Save from Docker and import to containerd
docker save stockyard-vm:latest | ctr -n flintlock images import -

log "Image imported to containerd"

# Verify image is in containerd
log "Verifying image in containerd..."
ctr -n flintlock images ls | grep stockyard-vm || error "Image not found in containerd"

log "Image available: ${IMAGE_NAME}"

# Check kernel
KERNEL_PATH="/var/lib/flintlock/images/vmlinux"
if [ ! -f "$KERNEL_PATH" ]; then
    error "Kernel not found at $KERNEL_PATH"
fi
log "Kernel found: $KERNEL_PATH"

echo ""
log "Ready to create VM!"
echo ""
echo "You can now run stockyard to create a VM, or test directly with grpcurl:"
echo ""
echo "  # List VMs"
echo "  grpcurl -plaintext localhost:9090 microvm.services.api.v1alpha1.MicroVM/ListMicroVMs"
echo ""
echo "  # Or use the stockyard CLI once it's built"
echo "  stockyard run --repo https://github.com/example/repo -- bash"

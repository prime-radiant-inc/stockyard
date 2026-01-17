#!/bin/bash
# build.sh - Build the Stockyard VM Docker image
#
# This script builds the stockyard-snapshot Go binary and then
# builds the Docker image containing all development tools.
#
# Usage:
#   ./build.sh                    # Build with default settings
#   IMAGE_NAME=my-image ./build.sh   # Custom image name
#   IMAGE_TAG=v1.0.0 ./build.sh      # Custom tag

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# Configuration with defaults
IMAGE_NAME="${IMAGE_NAME:-stockyard-vm}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
VM_USER="${VM_USER:-mooby}"

echo "=== Building Stockyard VM Image ==="
echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
echo "VM User: ${VM_USER}"
echo ""

# Step 1: Build the stockyard-snapshot Go binary
echo "=== Step 1: Building stockyard-snapshot binary ==="
cd scripts/stockyard-snapshot

# Detect architecture
GOARCH="${GOARCH:-$(go env GOARCH)}"
GOOS="${GOOS:-linux}"

echo "Building for ${GOOS}/${GOARCH}..."
CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" go build -o stockyard-snapshot .

if [ ! -f stockyard-snapshot ]; then
    echo "ERROR: Failed to build stockyard-snapshot binary"
    exit 1
fi
echo "Built: scripts/stockyard-snapshot/stockyard-snapshot"

cd "${SCRIPT_DIR}"

# Step 2: Build the Docker image
echo ""
echo "=== Step 2: Building Docker image ==="
docker build \
    --build-arg VM_USER="${VM_USER}" \
    -t "${IMAGE_NAME}:${IMAGE_TAG}" \
    -f Dockerfile \
    .

echo ""
echo "=== Build Complete ==="
echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
echo ""
echo "Next steps:"
echo "  - Convert to rootfs: ./convert-to-rootfs.sh"
echo "  - Run container:     docker run -it ${IMAGE_NAME}:${IMAGE_TAG} /bin/bash"

#!/bin/bash
# build.sh - Build the Stockyard VM Docker image
#
# Builds the Docker image containing all development tools.
# stockyard-shell and stockyard-snapshot are NOT baked in —
# they're injected at deploy time so they can update independently.
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

# Build the Docker image
echo "=== Building Docker image ==="
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

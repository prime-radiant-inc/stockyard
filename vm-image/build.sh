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
VARIANT="${VARIANT:-ubuntu}"

# Select Dockerfile based on variant
if [ "$VARIANT" = "alpine" ]; then
    DOCKERFILE="Dockerfile.alpine"
    # Default tag for alpine includes the variant
    if [ "$IMAGE_TAG" = "latest" ]; then
        IMAGE_TAG="alpine"
    fi
else
    DOCKERFILE="Dockerfile"
fi

echo "=== Building Stockyard VM Image ==="
echo "Variant: ${VARIANT}"
echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
echo "VM User: ${VM_USER}"
echo "Dockerfile: ${DOCKERFILE}"
echo ""

# Build the Docker image
echo "=== Building Docker image ==="
docker build \
    --build-arg VM_USER="${VM_USER}" \
    -t "${IMAGE_NAME}:${IMAGE_TAG}" \
    -f "${DOCKERFILE}" \
    .

echo ""
echo "=== Build Complete ==="
echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
echo ""
echo "Next steps:"
echo "  - Convert to rootfs: IMAGE_TAG=${IMAGE_TAG} ./convert-to-rootfs.sh"
echo "  - Run container:     docker run -it ${IMAGE_NAME}:${IMAGE_TAG} /bin/bash"

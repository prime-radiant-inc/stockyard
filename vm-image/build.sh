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
#   TARGET=container ./build.sh      # Apple `container` target → stockyard.local/<name>:container

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# Configuration with defaults
IMAGE_NAME="${IMAGE_NAME:-stockyard-vm}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
VM_USER="${VM_USER:-mooby}"
VARIANT="${VARIANT:-ubuntu}"
# Build target stage: "firecracker" (default) or "container".
TARGET="${TARGET:-firecracker}"
# Platform for the container target (Apple Silicon → arm64).
PLATFORM="${PLATFORM:-linux/arm64}"

# Qualified OCI ref for the Apple `container` target (PRI-2178).
# Unqualified names are normalized to docker.io/library/<name> by
# containerd-style stores (false Docker Hub provenance) and are squattable
# on Docker Hub — anything that PULLS an unqualified ref asks Hub for it.
# stockyard.local declares "built here, never pulled".
# The Firecracker/rootfs pipeline is deliberately NOT qualified: its Docker
# tag is consumed locally by convert-to-rootfs.sh, and the Linux registry
# names images at `stockyard image import` time.
CONTAINER_IMAGE_REF="stockyard.local/${IMAGE_NAME}:container"

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
echo "Target:  ${TARGET}"
if [ "$TARGET" = "container" ]; then
    echo "Image: ${CONTAINER_IMAGE_REF}"
else
    echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
fi
echo "VM User: ${VM_USER}"
echo "Dockerfile: ${DOCKERFILE}"
echo ""

# Build the Docker image
if [ "$TARGET" = "container" ]; then
    echo "=== Building container target (${PLATFORM}) ==="
    docker build \
        --build-arg VM_USER="${VM_USER}" \
        --target container \
        --platform "${PLATFORM}" \
        -t "${CONTAINER_IMAGE_REF}" \
        -f "${DOCKERFILE}" \
        .
else
    echo "=== Building firecracker target ==="
    docker build \
        --build-arg VM_USER="${VM_USER}" \
        --target firecracker \
        -t "${IMAGE_NAME}:${IMAGE_TAG}" \
        -f "${DOCKERFILE}" \
        .
fi

echo ""
echo "=== Build Complete ==="
if [ "$TARGET" = "container" ]; then
    echo "Image: ${CONTAINER_IMAGE_REF}"
    echo ""
    echo "Next steps:"
    echo "  - Load into Apple's container store:"
    echo "      docker save ${CONTAINER_IMAGE_REF} -o /tmp/stockyard-vm-oci.tar"
    echo "      container image load --input /tmp/stockyard-vm-oci.tar"
    echo "  - Run container: container run -d ${CONTAINER_IMAGE_REF}"
else
    echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
    echo ""
    echo "Next steps:"
    echo "  - Convert to rootfs: IMAGE_TAG=${IMAGE_TAG} ./convert-to-rootfs.sh"
    echo "  - Run container:     docker run -it ${IMAGE_NAME}:${IMAGE_TAG} /bin/bash"
fi

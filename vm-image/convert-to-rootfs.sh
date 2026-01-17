#!/bin/bash
# convert-to-rootfs.sh - Convert Docker image to ext4 rootfs for Firecracker
#
# This script extracts a Docker image and creates an ext4 filesystem
# image suitable for use with Firecracker VMs.
#
# Usage:
#   ./convert-to-rootfs.sh                        # Use defaults
#   ROOTFS_SIZE=20G ./convert-to-rootfs.sh        # Custom size
#   IMAGE_NAME=my-image ./convert-to-rootfs.sh    # Custom image

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

# Configuration with defaults
IMAGE_NAME="${IMAGE_NAME:-stockyard-vm}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
ROOTFS_SIZE="${ROOTFS_SIZE:-10G}"
OUTPUT_DIR="${OUTPUT_DIR:-${SCRIPT_DIR}/output}"
ROOTFS_FILE="${OUTPUT_DIR}/rootfs.ext4"

echo "=== Converting Docker Image to Rootfs ==="
echo "Source image: ${IMAGE_NAME}:${IMAGE_TAG}"
echo "Rootfs size:  ${ROOTFS_SIZE}"
echo "Output:       ${ROOTFS_FILE}"
echo ""

# Check if running as root (required for mount operations)
if [ "$EUID" -ne 0 ]; then
    echo "This script requires root privileges for mount operations."
    echo "Please run with: sudo ./convert-to-rootfs.sh"
    exit 1
fi

# Check if Docker image exists
if ! docker image inspect "${IMAGE_NAME}:${IMAGE_TAG}" &>/dev/null; then
    echo "ERROR: Docker image ${IMAGE_NAME}:${IMAGE_TAG} not found"
    echo "Run ./build.sh first to create the image"
    exit 1
fi

# Create output directory
mkdir -p "${OUTPUT_DIR}"

# Create a temporary directory for extraction
TMPDIR=$(mktemp -d)
trap "rm -rf ${TMPDIR}" EXIT

echo "=== Step 1: Exporting Docker image ==="
CONTAINER_ID=$(docker create "${IMAGE_NAME}:${IMAGE_TAG}")
docker export "${CONTAINER_ID}" > "${TMPDIR}/image.tar"
docker rm "${CONTAINER_ID}" > /dev/null
echo "Exported to temporary tarball"

echo ""
echo "=== Step 2: Creating ext4 filesystem ==="
# Create sparse file
truncate -s "${ROOTFS_SIZE}" "${ROOTFS_FILE}"
# Format as ext4
mkfs.ext4 -F "${ROOTFS_FILE}"
echo "Created ${ROOTFS_SIZE} ext4 filesystem"

echo ""
echo "=== Step 3: Extracting image to filesystem ==="
# Mount the filesystem
MOUNT_POINT="${TMPDIR}/rootfs"
mkdir -p "${MOUNT_POINT}"
mount -o loop "${ROOTFS_FILE}" "${MOUNT_POINT}"

# Extract the tarball
tar -xf "${TMPDIR}/image.tar" -C "${MOUNT_POINT}"
echo "Extracted image contents"

# Ensure required directories exist
mkdir -p "${MOUNT_POINT}/dev"
mkdir -p "${MOUNT_POINT}/proc"
mkdir -p "${MOUNT_POINT}/sys"
mkdir -p "${MOUNT_POINT}/run"
mkdir -p "${MOUNT_POINT}/tmp"

# Set proper permissions
chmod 1777 "${MOUNT_POINT}/tmp"
chmod 755 "${MOUNT_POINT}/run"

# Unmount
sync
umount "${MOUNT_POINT}"

echo ""
echo "=== Conversion Complete ==="
echo "Rootfs created: ${ROOTFS_FILE}"
echo "Size: $(du -h "${ROOTFS_FILE}" | cut -f1) (allocated from ${ROOTFS_SIZE})"
echo ""
echo "Usage with Firecracker:"
echo "  Set root_drive.path_on_host to: ${ROOTFS_FILE}"

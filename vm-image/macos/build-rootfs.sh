#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT_DIR="${1:-$SCRIPT_DIR/output}"
IMAGE_NAME="stockyard-alpine-vm"
ROOTFS_SIZE="${ROOTFS_SIZE:-4G}"

GREEN='\033[0;32m'
NC='\033[0m'
info() { echo -e "${GREEN}==>${NC} $*"; }

command -v docker >/dev/null 2>&1 || { echo "Error: docker required"; exit 1; }

# Check for e2fsprogs
MKFS="$(brew --prefix e2fsprogs 2>/dev/null)/sbin/mkfs.ext4" 2>/dev/null || true
if [ ! -x "$MKFS" ]; then
    echo "Error: e2fsprogs required: brew install e2fsprogs"
    exit 1
fi

mkdir -p "$OUTPUT_DIR"

info "Building Alpine VM image..."
docker build --platform linux/arm64 -t "$IMAGE_NAME" -f "$SCRIPT_DIR/Dockerfile.alpine" "$SCRIPT_DIR"

info "Exporting filesystem..."
CONTAINER_ID=$(docker create --platform linux/arm64 "$IMAGE_NAME")
docker export "$CONTAINER_ID" > "$OUTPUT_DIR/rootfs.tar"
docker rm "$CONTAINER_ID" >/dev/null

info "Creating ext4 image (${ROOTFS_SIZE})..."
ROOTFS_PATH="$OUTPUT_DIR/alpine-rootfs.raw"

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR $OUTPUT_DIR/rootfs.tar" EXIT

tar xf "$OUTPUT_DIR/rootfs.tar" -C "$TMPDIR"

# Remove Docker artifacts that confuse OpenRC (it runs in degraded "container" mode otherwise)
rm -f "$TMPDIR/.dockerenv"

# Ensure critical directories exist
mkdir -p "$TMPDIR"/{dev,proc,sys,run,tmp,mnt/stockyard}
chmod 1777 "$TMPDIR/tmp"

# Create ext4 image populated from directory
truncate -s "$ROOTFS_SIZE" "$ROOTFS_PATH"
"$MKFS" -d "$TMPDIR" -L alpine-root "$ROOTFS_PATH"

info "Rootfs: $ROOTFS_PATH ($(du -h "$ROOTFS_PATH" | cut -f1))"
info "Done!"

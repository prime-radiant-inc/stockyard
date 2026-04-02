#!/bin/bash
set -euo pipefail

# Stockyard macOS VM Image Setup
#
# Downloads and prepares an arm64 Linux kernel + rootfs for use with
# the vfkit backend on Apple Silicon Macs.
#
# Kernel: Ubuntu arm64 cloud kernel (uncompressed from cloud-images)
# Initrd: Matching Ubuntu initrd (required for module loading)
# Rootfs: Ubuntu 24.04 arm64 cloud image (converted from qcow2 to raw)
#
# Prerequisites: brew install vfkit qemu
#
# Usage:
#   ./setup.sh              # Download everything to ./output/
#   ./setup.sh /some/path   # Download to a custom directory

OUTPUT_DIR="${1:-$(dirname "$0")/output}"
UBUNTU_RELEASE="noble"
UBUNTU_VERSION="24.04"
ROOTFS_SIZE="10G"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}==>${NC} $*"; }
warn()  { echo -e "${YELLOW}==>${NC} $*"; }
error() { echo -e "${RED}==>${NC} $*" >&2; }

check_prereqs() {
    local missing=()
    command -v vfkit    >/dev/null 2>&1 || missing+=("vfkit (brew install vfkit)")
    command -v docker   >/dev/null 2>&1 || missing+=("docker (OrbStack or Docker Desktop)")
    command -v curl     >/dev/null 2>&1 || missing+=("curl")

    if [ ${#missing[@]} -gt 0 ]; then
        error "Missing prerequisites:"
        for m in "${missing[@]}"; do
            echo "  - $m"
        done
        exit 1
    fi
}

download_kernel() {
    local kernel_path="$OUTPUT_DIR/vmlinux"

    if [ -f "$kernel_path" ]; then
        info "Kernel already exists at $kernel_path"
        return
    fi

    info "Downloading Ubuntu ${UBUNTU_VERSION} arm64 kernel..."

    local url="https://cloud-images.ubuntu.com/releases/${UBUNTU_RELEASE}/release/unpacked/ubuntu-${UBUNTU_VERSION}-server-cloudimg-arm64-vmlinuz-generic"

    curl -L --progress-bar -o "$kernel_path.gz" "$url"

    info "Decompressing kernel..."
    gunzip "$kernel_path.gz"

    local filetype
    filetype=$(file "$kernel_path")
    if echo "$filetype" | grep -q "ARM64"; then
        info "Kernel verified: ARM64 boot executable Image"
    else
        warn "Kernel file type: $filetype"
    fi

    info "Kernel: $kernel_path ($(du -h "$kernel_path" | cut -f1))"
}

download_initrd() {
    local initrd_path="$OUTPUT_DIR/initrd.img"

    if [ -f "$initrd_path" ]; then
        info "Initrd already exists at $initrd_path"
        return
    fi

    info "Downloading Ubuntu ${UBUNTU_VERSION} arm64 initrd..."

    local url="https://cloud-images.ubuntu.com/releases/${UBUNTU_RELEASE}/release/unpacked/ubuntu-${UBUNTU_VERSION}-server-cloudimg-arm64-initrd-generic"

    curl -L --progress-bar -o "$initrd_path" "$url"

    info "Initrd: $initrd_path ($(du -h "$initrd_path" | cut -f1))"
}

download_rootfs() {
    local raw_path="$OUTPUT_DIR/rootfs.raw"

    if [ -f "$raw_path" ]; then
        info "Rootfs already exists at $raw_path"
        return
    fi

    local qcow2_path="$OUTPUT_DIR/rootfs.qcow2"

    if [ ! -f "$qcow2_path" ]; then
        info "Downloading Ubuntu ${UBUNTU_VERSION} arm64 cloud image..."

        local url="https://cloud-images.ubuntu.com/releases/${UBUNTU_RELEASE}/release/ubuntu-${UBUNTU_VERSION}-server-cloudimg-arm64.img"
        curl -L --progress-bar -o "$qcow2_path" "$url"
    fi

    info "Converting qcow2 to raw (vfkit requires raw images)..."
    qemu-img convert -f qcow2 -O raw "$qcow2_path" "$raw_path"

    info "Resizing to ${ROOTFS_SIZE}..."
    qemu-img resize -f raw "$raw_path" "$ROOTFS_SIZE"

    # Clean up qcow2
    rm -f "$qcow2_path"

    info "Rootfs: $raw_path ($(du -h "$raw_path" | cut -f1))"
}

print_summary() {
    local kernel_path="$OUTPUT_DIR/vmlinux"
    local initrd_path="$OUTPUT_DIR/initrd.img"
    local rootfs_path="$OUTPUT_DIR/rootfs.raw"

    echo ""
    info "Setup complete!"
    echo ""
    echo "Files:"
    echo "  Kernel: $kernel_path ($(du -h "$kernel_path" | cut -f1))"
    echo "  Initrd: $initrd_path ($(du -h "$initrd_path" | cut -f1))"
    echo "  Rootfs: $rootfs_path ($(du -h "$rootfs_path" | cut -f1))"
    echo ""
    echo "Stockyard config.json:"
    echo ""
    cat <<EOF
{
  "backend": "vfkit",
  "vfkit": {
    "kernel_path": "$(cd "$OUTPUT_DIR" && pwd)/vmlinux",
    "rootfs_path": "$(cd "$OUTPUT_DIR" && pwd)/rootfs.raw"
  },
  "rootfs": {
    "provider": "apfs",
    "base_image": "$(cd "$OUTPUT_DIR" && pwd)/rootfs.raw",
    "vms_dir": "$(cd "$OUTPUT_DIR" && pwd)/vms"
  },
  "vm": {
    "user": "ubuntu"
  }
}
EOF
    echo ""
    echo "Quick test:"
    echo "  vfkit --cpus 2 --memory 2048 \\"
    echo "    --kernel $(cd "$OUTPUT_DIR" && pwd)/vmlinux \\"
    echo "    --initrd $(cd "$OUTPUT_DIR" && pwd)/initrd.img \\"
    echo "    --kernel-cmdline 'console=hvc0 root=/dev/vda1 rw' \\"
    echo "    --device virtio-blk,path=$(cd "$OUTPUT_DIR" && pwd)/rootfs.raw \\"
    echo "    --device virtio-net,nat \\"
    echo "    --device virtio-rng \\"
    echo "    --device virtio-serial,logFilePath=$(cd "$OUTPUT_DIR" && pwd)/console.log \\"
    echo "    --cloud-init <user-data>,<meta-data>"
}

main() {
    check_prereqs
    mkdir -p "$OUTPUT_DIR"

    info "Output directory: $OUTPUT_DIR"
    echo ""

    download_kernel
    echo ""
    download_initrd
    echo ""
    download_rootfs

    print_summary
}

main

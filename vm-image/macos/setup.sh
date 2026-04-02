#!/bin/bash
set -euo pipefail

# Stockyard macOS VM Image Setup
#
# Downloads the Kata Containers arm64 kernel and builds an Alpine rootfs
# for use with the vfkit backend on Apple Silicon Macs.
#
# Prerequisites: brew install vfkit e2fsprogs; Docker (OrbStack or Docker Desktop)
#
# Usage:
#   ./setup.sh              # Build everything in ./output/
#   ./setup.sh /some/path   # Build to a custom directory

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT_DIR="${1:-$SCRIPT_DIR/output}"
KATA_VERSION="3.12.0"

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

info()  { echo -e "${GREEN}==>${NC} $*"; }
error() { echo -e "${RED}==>${NC} $*" >&2; }

check_prereqs() {
    local missing=()
    command -v vfkit    >/dev/null 2>&1 || missing+=("vfkit (brew install vfkit)")
    command -v docker   >/dev/null 2>&1 || missing+=("docker (OrbStack or Docker Desktop)")
    command -v curl     >/dev/null 2>&1 || missing+=("curl")

    # Check for e2fsprogs (keg-only on macOS)
    local mkfs
    mkfs="$(brew --prefix e2fsprogs 2>/dev/null)/sbin/mkfs.ext4" 2>/dev/null || true
    if [ ! -x "$mkfs" ]; then
        missing+=("e2fsprogs (brew install e2fsprogs)")
    fi

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

    info "Downloading Kata Containers arm64 kernel (v${KATA_VERSION})..."

    local kata_url="https://github.com/kata-containers/kata-containers/releases/download/${KATA_VERSION}/kata-static-${KATA_VERSION}-arm64.tar.xz"
    local tmp_dir
    tmp_dir=$(mktemp -d)

    curl -L --progress-bar -o "$tmp_dir/kata-static.tar.xz" "$kata_url"

    info "Extracting kernel..."
    tar xf "$tmp_dir/kata-static.tar.xz" -C "$tmp_dir" \
        --include='*/vmlinux.container' --include='*/vmlinux-*' 2>/dev/null || true

    local vmlinux
    vmlinux=$(find "$tmp_dir" -name 'vmlinux.container' -o -name 'vmlinux-*' 2>/dev/null | head -1)

    if [ -z "$vmlinux" ] || [ ! -f "$vmlinux" ]; then
        error "Could not find kernel in Kata archive"
        rm -rf "$tmp_dir"
        exit 1
    fi

    cp "$vmlinux" "$kernel_path"
    rm -rf "$tmp_dir"

    info "Kernel: $kernel_path ($(du -h "$kernel_path" | cut -f1))"
    info "$(file "$kernel_path")"
}

build_rootfs() {
    local rootfs_path="$OUTPUT_DIR/alpine-rootfs.raw"

    if [ -f "$rootfs_path" ]; then
        info "Rootfs already exists at $rootfs_path"
        return
    fi

    info "Building Alpine rootfs..."
    "$SCRIPT_DIR/build-rootfs.sh" "$OUTPUT_DIR"
}

print_summary() {
    local kernel_path="$OUTPUT_DIR/vmlinux"
    local rootfs_path="$OUTPUT_DIR/alpine-rootfs.raw"
    local abs_output
    abs_output="$(cd "$OUTPUT_DIR" && pwd)"

    echo ""
    info "Setup complete!"
    echo ""
    echo "Files:"
    echo "  Kernel: $kernel_path ($(du -h "$kernel_path" | cut -f1))"
    echo "  Rootfs: $rootfs_path ($(du -h "$rootfs_path" | cut -f1))"
    echo ""
    echo "Example config.json:"
    echo ""
    cat <<EOF
{
  "instance_id": "my-mac",
  "backend": "vfkit",
  "vfkit": {
    "kernel_path": "$abs_output/vmlinux",
    "rootfs_path": "$abs_output/alpine-rootfs.raw"
  },
  "rootfs": {
    "provider": "apfs",
    "base_image": "$abs_output/alpine-rootfs.raw",
    "vms_dir": "$abs_output/vms"
  },
  "vm": { "user": "stockyard" }
}
EOF
}

main() {
    check_prereqs
    mkdir -p "$OUTPUT_DIR"

    info "Output directory: $OUTPUT_DIR"
    echo ""

    download_kernel
    echo ""
    build_rootfs

    print_summary
}

main

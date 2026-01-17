#!/bin/bash
# Test Flintlock installation
# Run with: ./scripts/test-flintlock.sh

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[✓]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
fail() { echo -e "${RED}[✗]${NC} $1"; }

echo "Testing Flintlock infrastructure..."
echo ""

# Check KVM access
echo "1. Checking KVM access..."
if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
    log "KVM access OK"
else
    fail "Cannot access /dev/kvm - are you in the kvm group?"
    echo "   Run: newgrp kvm (or re-login)"
fi

# Check Firecracker
echo "2. Checking Firecracker..."
if command -v firecracker &>/dev/null; then
    VERSION=$(firecracker --version 2>&1 | head -1)
    log "Firecracker: $VERSION"
else
    fail "Firecracker not found"
fi

# Check Flintlock
echo "3. Checking Flintlock..."
if command -v flintlockd &>/dev/null; then
    log "Flintlockd: $(which flintlockd)"
else
    fail "Flintlockd not found"
fi

if command -v flintlock-metrics &>/dev/null; then
    log "Flintlock metrics: $(which flintlock-metrics)"
else
    warn "Flintlock metrics not found (optional)"
fi

# Check containerd
echo "4. Checking containerd..."
if systemctl is-active --quiet containerd; then
    log "containerd is running"
else
    fail "containerd is not running"
fi

# Check ZFS
echo "5. Checking ZFS..."
if command -v zfs &>/dev/null; then
    log "ZFS tools installed"
    if zpool list tank &>/dev/null; then
        log "ZFS pool 'tank' exists"
        zfs list tank/stockyard 2>/dev/null && log "tank/stockyard dataset exists" || warn "tank/stockyard dataset missing"
    else
        fail "ZFS pool 'tank' not found"
    fi
else
    fail "ZFS not installed"
fi

# Check bridge
echo "6. Checking network bridge..."
if ip link show flbr0 &>/dev/null; then
    log "Bridge flbr0 exists"
    IP=$(ip addr show flbr0 | grep "inet " | awk '{print $2}')
    log "Bridge IP: $IP"
else
    fail "Bridge flbr0 not found"
fi

# Check kernel
echo "7. Checking kernel image..."
if [ -f /var/lib/flintlock/images/vmlinux ]; then
    log "Kernel image exists at /var/lib/flintlock/images/vmlinux"
else
    fail "Kernel image not found"
fi

# Check Flintlock gRPC
echo "8. Checking Flintlock gRPC service..."
if nc -z localhost 9090 2>/dev/null; then
    log "Flintlock gRPC is listening on port 9090"
else
    warn "Flintlock gRPC not responding on port 9090"
    echo "   Start with: sudo ./scripts/start-flintlock.sh"
fi

echo ""
echo "============================================"
echo "Summary"
echo "============================================"

# Quick summary
ISSUES=0

[ -r /dev/kvm ] || ((ISSUES++))
command -v firecracker &>/dev/null || ((ISSUES++))
command -v flintlockd &>/dev/null || ((ISSUES++))
systemctl is-active --quiet containerd || ((ISSUES++))
zpool list tank &>/dev/null || ((ISSUES++))
ip link show flbr0 &>/dev/null || ((ISSUES++))
[ -f /var/lib/flintlock/images/vmlinux ] || ((ISSUES++))

if [ $ISSUES -eq 0 ]; then
    log "All checks passed! Ready to run VMs."
    echo ""
    echo "Next: Start flintlock with: sudo ./scripts/start-flintlock.sh"
else
    warn "$ISSUES issue(s) found. Review the output above."
fi

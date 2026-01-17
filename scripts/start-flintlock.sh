#!/bin/bash
# Start Flintlock with all dependencies
# Run with: sudo ./scripts/start-flintlock.sh

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

if [ "$EUID" -ne 0 ]; then
    error "Please run with sudo: sudo $0"
fi

# Check prerequisites
command -v firecracker >/dev/null || error "Firecracker not installed. Run setup-infrastructure.sh first."
command -v flintlockd >/dev/null || error "Flintlock not installed. Run setup-infrastructure.sh first."
command -v containerd >/dev/null || error "containerd not installed."

# Ensure the bridge exists
BRIDGE_NAME="flbr0"
if ! ip link show "$BRIDGE_NAME" &>/dev/null; then
    log "Creating bridge $BRIDGE_NAME..."
    ip link add "$BRIDGE_NAME" type bridge
    ip addr add 192.168.100.1/24 dev "$BRIDGE_NAME"
    ip link set "$BRIDGE_NAME" up
fi

# Ensure IP forwarding is enabled
sysctl -w net.ipv4.ip_forward=1 >/dev/null

# Set up NAT if not already done
if ! iptables -t nat -C POSTROUTING -s 192.168.100.0/24 ! -o "$BRIDGE_NAME" -j MASQUERADE 2>/dev/null; then
    iptables -t nat -A POSTROUTING -s 192.168.100.0/24 ! -o "$BRIDGE_NAME" -j MASQUERADE
fi

# Start containerd if not running
if ! systemctl is-active --quiet containerd; then
    log "Starting containerd..."
    systemctl start containerd
fi

# Create flintlock namespace in containerd
ctr namespace create flintlock 2>/dev/null || true

# Find the parent network interface
NET_DEVICE=$(ip route show | awk '/default/ {print $5}' | head -1)
log "Using parent interface: $NET_DEVICE"

# Start flintlockd
log "Starting flintlockd..."
log "Bridge: $BRIDGE_NAME"
log "gRPC endpoint: 0.0.0.0:9090"

exec /usr/local/bin/flintlockd run \
    --containerd-socket=/run/containerd/containerd.sock \
    --parent-iface="$NET_DEVICE" \
    --bridge-name="$BRIDGE_NAME" \
    --grpc-endpoint=0.0.0.0:9090 \
    --insecure \
    --verbosity=9

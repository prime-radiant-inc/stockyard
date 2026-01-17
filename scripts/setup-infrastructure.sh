#!/bin/bash
# Stockyard Infrastructure Setup Script
# Run with: sudo ./scripts/setup-infrastructure.sh
#
# This script sets up Flintlock, Firecracker, and ZFS for stockyard

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log() { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    error "Please run with sudo: sudo $0"
fi

REAL_USER="${SUDO_USER:-$USER}"
log "Setting up infrastructure for user: $REAL_USER"

# ============================================
# 1. Add user to kvm group
# ============================================
log "Adding $REAL_USER to kvm group..."
usermod -aG kvm "$REAL_USER"
log "User added to kvm group (re-login required for effect)"

# ============================================
# 2. Install ZFS
# ============================================
log "Installing ZFS..."
apt-get update
apt-get install -y zfsutils-linux

# Create a file-backed ZFS pool for development
ZPOOL_DIR="/var/lib/stockyard"
ZPOOL_FILE="$ZPOOL_DIR/zpool.img"
ZPOOL_SIZE="50G"

mkdir -p "$ZPOOL_DIR"

if ! zpool list tank &>/dev/null; then
    log "Creating file-backed ZFS pool (${ZPOOL_SIZE})..."
    truncate -s "$ZPOOL_SIZE" "$ZPOOL_FILE"
    zpool create tank "$ZPOOL_FILE"
    zfs create tank/stockyard
    zfs create tank/stockyard/workspaces
    zfs set compression=lz4 tank/stockyard
    log "ZFS pool 'tank' created"
else
    log "ZFS pool 'tank' already exists"
fi

zpool status tank

# ============================================
# 3. Install Firecracker
# ============================================
FIRECRACKER_VERSION="v1.10.1"
log "Installing Firecracker ${FIRECRACKER_VERSION}..."

if [ ! -f /usr/local/bin/firecracker ]; then
    cd /tmp
    ARCH=$(uname -m)
    curl -LO "https://github.com/firecracker-microvm/firecracker/releases/download/${FIRECRACKER_VERSION}/firecracker-${FIRECRACKER_VERSION}-${ARCH}.tgz"
    tar -xzf "firecracker-${FIRECRACKER_VERSION}-${ARCH}.tgz"

    mv "release-${FIRECRACKER_VERSION}-${ARCH}/firecracker-${FIRECRACKER_VERSION}-${ARCH}" /usr/local/bin/firecracker
    mv "release-${FIRECRACKER_VERSION}-${ARCH}/jailer-${FIRECRACKER_VERSION}-${ARCH}" /usr/local/bin/jailer
    chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer

    rm -rf "release-${FIRECRACKER_VERSION}-${ARCH}" "firecracker-${FIRECRACKER_VERSION}-${ARCH}.tgz"
    log "Firecracker installed to /usr/local/bin/firecracker"
else
    log "Firecracker already installed"
fi

firecracker --version

# ============================================
# 4. Install Flintlock
# ============================================
FLINTLOCK_VERSION="v0.6.0"
log "Installing Flintlock ${FLINTLOCK_VERSION}..."

if [ ! -f /usr/local/bin/flintlockd ]; then
    cd /tmp
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then
        ARCH="amd64"
    elif [ "$ARCH" = "aarch64" ]; then
        ARCH="arm64"
    fi

    # Flintlock releases are standalone binaries, not tarballs
    curl -L -o flintlockd "https://github.com/liquidmetal-dev/flintlock/releases/download/${FLINTLOCK_VERSION}/flintlockd_${ARCH}"
    curl -L -o flintlock-metrics "https://github.com/liquidmetal-dev/flintlock/releases/download/${FLINTLOCK_VERSION}/flintlock-metrics_${ARCH}"

    mv flintlockd /usr/local/bin/
    mv flintlock-metrics /usr/local/bin/
    chmod +x /usr/local/bin/flintlockd /usr/local/bin/flintlock-metrics

    log "Flintlock installed to /usr/local/bin/flintlockd"
else
    log "Flintlock already installed"
fi

flintlockd version || warn "Could not get flintlock version"

# ============================================
# 5. Configure containerd for Flintlock
# ============================================
log "Configuring containerd..."

# Ensure containerd is running
systemctl enable containerd
systemctl start containerd

# Create Flintlock's containerd namespace
ctr namespace create flintlock 2>/dev/null || true

# Set up devmapper thin pool for containerd snapshotter
# This is required for Flintlock to work with containerd
THINPOOL_DATA="/var/lib/containerd-dev/snapshotter/devmapper/data"
THINPOOL_META="/var/lib/containerd-dev/snapshotter/devmapper/meta"
CONTAINERD_DEV_DIR="/var/lib/containerd-dev"

mkdir -p "$CONTAINERD_DEV_DIR/snapshotter/devmapper"

if [ ! -f "$THINPOOL_DATA" ]; then
    log "Creating devmapper thin pool..."
    truncate -s 10G "$THINPOOL_DATA"
    truncate -s 1G "$THINPOOL_META"
fi

# Create a separate containerd config for Flintlock
CONTAINERD_DEV_CONFIG="/etc/containerd/config-dev.toml"
if [ ! -f "$CONTAINERD_DEV_CONFIG" ]; then
    log "Creating Flintlock containerd configuration..."
    cat > "$CONTAINERD_DEV_CONFIG" << 'EOF'
version = 2

root = "/var/lib/containerd-dev"
state = "/run/containerd-dev"

[grpc]
  address = "/run/containerd-dev/containerd.sock"

[plugins]
  [plugins."io.containerd.snapshotter.v1.devmapper"]
    pool_name = "flintlock-dev-thinpool"
    root_path = "/var/lib/containerd-dev/snapshotter/devmapper"
    base_image_size = "10GB"
    discard_blocks = true

[plugins."io.containerd.grpc.v1.cri".containerd]
  snapshotter = "devmapper"
EOF
    log "Created $CONTAINERD_DEV_CONFIG"
fi

# ============================================
# 6. Set up networking
# ============================================
log "Setting up networking..."

# Install CNI plugins if not present
CNI_VERSION="v1.6.1"
CNI_DIR="/opt/cni/bin"

if [ ! -d "$CNI_DIR" ] || [ -z "$(ls -A $CNI_DIR 2>/dev/null)" ]; then
    log "Installing CNI plugins ${CNI_VERSION}..."
    mkdir -p "$CNI_DIR"
    cd /tmp
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then
        ARCH="amd64"
    fi
    curl -LO "https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-${ARCH}-${CNI_VERSION}.tgz"
    tar -xzf "cni-plugins-linux-${ARCH}-${CNI_VERSION}.tgz" -C "$CNI_DIR"
    rm -f "cni-plugins-linux-${ARCH}-${CNI_VERSION}.tgz"
    log "CNI plugins installed"
else
    log "CNI plugins already installed"
fi

# Create a bridge for Flintlock VMs
BRIDGE_NAME="flbr0"
BRIDGE_IP="192.168.100.1/24"

if ! ip link show "$BRIDGE_NAME" &>/dev/null; then
    log "Creating bridge $BRIDGE_NAME..."
    ip link add "$BRIDGE_NAME" type bridge
    ip addr add "$BRIDGE_IP" dev "$BRIDGE_NAME"
    ip link set "$BRIDGE_NAME" up

    # Enable IP forwarding
    sysctl -w net.ipv4.ip_forward=1
    echo "net.ipv4.ip_forward=1" > /etc/sysctl.d/99-flintlock.conf

    # Set up NAT for the bridge
    iptables -t nat -A POSTROUTING -s 192.168.100.0/24 ! -o "$BRIDGE_NAME" -j MASQUERADE
    iptables -A FORWARD -i "$BRIDGE_NAME" -j ACCEPT
    iptables -A FORWARD -o "$BRIDGE_NAME" -j ACCEPT

    log "Bridge $BRIDGE_NAME created with IP $BRIDGE_IP"
else
    log "Bridge $BRIDGE_NAME already exists"
fi

# ============================================
# 7. Download kernel
# ============================================
log "Downloading Firecracker kernel..."

KERNEL_DIR="/var/lib/flintlock/images"
mkdir -p "$KERNEL_DIR"

if [ ! -f "$KERNEL_DIR/vmlinux" ]; then
    log "Downloading kernel..."
    cd "$KERNEL_DIR"
    # Use Flintlock's recommended kernel
    KERNEL_URL="https://github.com/liquidmetal-dev/flintlock/releases/download/v0.6.0/vmlinux"
    curl -L -o vmlinux "$KERNEL_URL" || {
        # Fallback to Firecracker's kernel
        warn "Could not download Flintlock kernel, trying Firecracker's..."
        curl -L -o vmlinux "https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.10/x86_64/vmlinux-6.1.102"
    }
    log "Kernel downloaded to $KERNEL_DIR/vmlinux"
else
    log "Kernel already exists at $KERNEL_DIR/vmlinux"
fi

# ============================================
# 8. Create directories
# ============================================
log "Creating required directories..."
mkdir -p /var/lib/flintlock/vm
mkdir -p /run/flintlock
mkdir -p /var/log/flintlock
chown -R "$REAL_USER:$REAL_USER" /var/log/flintlock

# ============================================
# 9. Create systemd service for Flintlock
# ============================================
log "Creating Flintlock systemd service..."

NET_DEVICE=$(ip route show | awk '/default/ {print $5}' | head -1)

cat > /etc/systemd/system/flintlockd.service << EOF
[Unit]
Description=Flintlock MicroVM Manager
After=network.target containerd.service
Requires=containerd.service

[Service]
Type=simple
ExecStart=/usr/local/bin/flintlockd run \\
    --containerd-socket=/run/containerd/containerd.sock \\
    --parent-iface=${NET_DEVICE} \\
    --bridge-name=flbr0 \\
    --grpc-endpoint=0.0.0.0:9090 \\
    --insecure
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
log "Flintlock systemd service created"

# ============================================
# Summary
# ============================================
echo ""
echo "============================================"
log "Infrastructure setup complete!"
echo "============================================"
echo ""
echo "Installed components:"
echo "  - Firecracker: $(firecracker --version 2>&1 | head -1)"
echo "  - Flintlock: /usr/local/bin/flintlockd"
echo "  - ZFS pool: tank"
echo "  - Bridge: $BRIDGE_NAME ($BRIDGE_IP)"
echo "  - Kernel: $KERNEL_DIR/vmlinux"
echo ""
echo "Next steps:"
echo "  1. Log out and back in (for kvm group to take effect)"
echo "  2. Start Flintlock: sudo systemctl start flintlockd"
echo "  3. Check status: sudo systemctl status flintlockd"
echo "  4. Build the VM image: cd stockyard && make vm-image"
echo ""
warn "Note: You may need to run 'newgrp kvm' or re-login for kvm group access"

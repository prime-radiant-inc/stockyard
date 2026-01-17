#!/bin/bash
# stockyard-init.sh
# Stockyard VM initialization script
# Fetches metadata from MMDS and configures the VM

set -e

LOG_FILE="/var/log/stockyard/init.log"
mkdir -p /var/log/stockyard

exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== Stockyard Init $(date) ==="

# Wait for network interface
echo "Waiting for network..."
for i in {1..30}; do
    if ip route | grep -q "169.254.169.254"; then
        echo "MMDS route available"
        break
    fi
    sleep 1
done

# Fetch metadata from MMDS
MMDS_URL="http://169.254.169.254/latest"
echo "Fetching metadata from MMDS..."

# Get hostname
HOSTNAME=$(curl -sf "${MMDS_URL}/meta-data/local-hostname" 2>/dev/null || echo "")
if [ -n "$HOSTNAME" ]; then
    echo "Setting hostname to: $HOSTNAME"
    hostnamectl set-hostname "$HOSTNAME"
fi

# Get Tailscale auth key from meta-data
TS_AUTH_KEY=$(curl -sf "${MMDS_URL}/meta-data/tailscale-auth-key" 2>/dev/null || echo "")
if [ -n "$TS_AUTH_KEY" ]; then
    echo "Found Tailscale auth key, connecting..."
    tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh &
fi

# Wait for external network
echo "Waiting for external network..."
for i in {1..30}; do
    if ping -c 1 8.8.8.8 &>/dev/null; then
        echo "External network is up"
        break
    fi
    sleep 1
done

# Setup workspace permissions
if [ -d /workspace ]; then
    chown -R vscode:vscode /workspace 2>/dev/null || true
    echo "Workspace permissions set"
fi

# Verify Tailscale (if configured)
if command -v tailscale &>/dev/null; then
    sleep 5  # Give Tailscale time to connect
    if tailscale status &>/dev/null; then
        echo "Tailscale is connected"
        tailscale status
    else
        echo "Tailscale not connected (may not be configured)"
    fi
fi

# Setup Claude Code hooks for vscode user
if [ -f /etc/stockyard/claude-hooks.json ]; then
    su - vscode -c "/usr/local/bin/setup-claude-hooks.sh" 2>/dev/null || true
fi

# Create run directory for snapshot socket
mkdir -p /run/stockyard
chmod 755 /run/stockyard

echo "=== Stockyard Init Complete ==="

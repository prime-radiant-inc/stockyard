#!/bin/bash
# stockyard-init.sh
# Stockyard VM initialization script
# Fetches metadata from MMDS and configures the VM

set -e

# VM_USER is set in Dockerfile, default to mooby if not set
VM_USER="${VM_USER:-mooby}"

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

# Helper function to strip JSON quotes from MMDS responses
# Firecracker MMDS returns values as JSON strings: "value" -> value
strip_json_quotes() {
    sed 's/^"//;s/"$//'
}

# Get hostname
HOSTNAME_RAW=$(curl -sf "${MMDS_URL}/meta-data/local-hostname" 2>/dev/null || echo "")
HOSTNAME=$(echo "$HOSTNAME_RAW" | strip_json_quotes)
echo "Raw hostname response: $HOSTNAME_RAW"
if [ -n "$HOSTNAME" ]; then
    echo "Setting hostname to: $HOSTNAME"
    hostnamectl set-hostname "$HOSTNAME"
fi

# Get Tailscale auth key from meta-data
TS_AUTH_KEY_RAW=$(curl -sf "${MMDS_URL}/meta-data/tailscale-auth-key" 2>/dev/null || echo "")
TS_AUTH_KEY=$(echo "$TS_AUTH_KEY_RAW" | strip_json_quotes)
echo "Raw auth key response length: ${#TS_AUTH_KEY_RAW}"
if [ -n "$TS_AUTH_KEY" ]; then
    echo "Found Tailscale auth key (${#TS_AUTH_KEY} chars), waiting for tailscaled..."
    # Wait for tailscaled to be ready (up to 30 seconds)
    for i in {1..30}; do
        if tailscale status &>/dev/null; then
            echo "tailscaled is ready"
            break
        fi
        sleep 1
    done
    echo "Connecting to Tailscale..."
    tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh 2>&1 | tee -a "$LOG_FILE" &
else
    echo "No Tailscale auth key found in MMDS"
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
    chown -R "${VM_USER}:${VM_USER}" /workspace 2>/dev/null || true
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

# Setup Claude Code hooks for VM user
if [ -f /etc/stockyard/claude-hooks.json ]; then
    su - "${VM_USER}" -c "/usr/local/bin/setup-claude-hooks.sh" 2>/dev/null || true
fi

# Create run directory for snapshot socket
mkdir -p /run/stockyard
chmod 755 /run/stockyard

echo "=== Stockyard Init Complete ==="

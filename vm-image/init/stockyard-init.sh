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

# Wait for DHCP to configure network
echo "Waiting for network (DHCP)..."
for i in {1..30}; do
    if ip addr show eth0 2>/dev/null | grep -q "inet "; then
        echo "Network configured via DHCP"
        ip addr show eth0 | grep "inet "
        break
    fi
    sleep 1
done

# Set up DNS (systemd-resolved is disabled)
echo "Configuring DNS..."
rm -f /etc/resolv.conf 2>/dev/null || true
printf 'nameserver 8.8.8.8\nnameserver 8.8.4.4\n' > /etc/resolv.conf

# Ensure MMDS route exists (systemd-networkd should add it, but verify)
if ! ip route | grep -q "169.254.169.254"; then
    echo "Adding MMDS route..."
    ip route add 169.254.169.254/32 dev eth0 scope link 2>/dev/null || true
fi

# Verify MMDS is reachable
for i in {1..10}; do
    if curl -sf "http://169.254.169.254/" &>/dev/null; then
        echo "MMDS is reachable"
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

# Get SSH authorized keys from meta-data
SSH_KEYS_RAW=$(curl -sf "${MMDS_URL}/meta-data/ssh-authorized-keys" 2>/dev/null || echo "")
SSH_KEYS=$(echo "$SSH_KEYS_RAW" | strip_json_quotes)
if [ -n "$SSH_KEYS" ]; then
    echo "Installing SSH authorized keys..."
    SSH_DIR="/home/${VM_USER}/.ssh"
    mkdir -p "$SSH_DIR"
    echo "$SSH_KEYS" > "${SSH_DIR}/authorized_keys"
    chmod 700 "$SSH_DIR"
    chmod 600 "${SSH_DIR}/authorized_keys"
    chown -R "${VM_USER}:${VM_USER}" "$SSH_DIR"
    echo "SSH keys installed ($(echo "$SSH_KEYS" | wc -l) keys)"
else
    echo "No SSH authorized keys found in MMDS"
fi

# Get Tailscale auth key from meta-data
TS_AUTH_KEY_RAW=$(curl -sf "${MMDS_URL}/meta-data/tailscale-auth-key" 2>/dev/null || echo "")
TS_AUTH_KEY=$(echo "$TS_AUTH_KEY_RAW" | strip_json_quotes)
echo "Raw auth key response length: ${#TS_AUTH_KEY_RAW}"
if [ -n "$TS_AUTH_KEY" ]; then
    echo "Found Tailscale auth key (${#TS_AUTH_KEY} chars), waiting for tailscaled..."
    # Wait for tailscaled to be ready (up to 60 seconds)
    for i in {1..60}; do
        if tailscale status &>/dev/null; then
            echo "tailscaled is ready after ${i}s"
            break
        fi
        sleep 1
    done
    # Try connecting even if tailscale status fails - the socket might exist
    echo "Connecting to Tailscale..."
    if tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh 2>&1; then
        echo "Tailscale up succeeded"
    else
        echo "Tailscale up failed, retrying in 5s..."
        sleep 5
        tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh 2>&1 | tee -a "$LOG_FILE" &
    fi
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

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

# ============================================================================
# Instrumentation: timing functions
# ============================================================================
BOOT_START=$(date +%s.%N)

log_timing() {
    local now=$(date +%s.%N)
    local elapsed=$(echo "$now - $BOOT_START" | bc)
    echo "[+${elapsed}s] $1"
}

log_timing "=== Stockyard Init Started ==="

# ============================================================================
# Phase 1: Network (DHCP should already be done via systemd-networkd)
# ============================================================================
log_timing "Checking network status..."

# Quick check - network should already be up from network-online.target
if ip addr show eth0 2>/dev/null | grep -q "inet "; then
    log_timing "Network already configured"
    ip addr show eth0 | grep "inet "
else
    # Fallback: wait briefly for DHCP (shouldn't be needed)
    log_timing "Waiting for DHCP (fallback)..."
    for i in {1..10}; do
        if ip addr show eth0 2>/dev/null | grep -q "inet "; then
            log_timing "Network configured after ${i}s"
            ip addr show eth0 | grep "inet "
            break
        fi
        sleep 1
    done
fi

# Set up DNS (systemd-resolved is disabled)
log_timing "Configuring DNS..."
rm -f /etc/resolv.conf 2>/dev/null || true
printf 'nameserver 8.8.8.8\nnameserver 8.8.4.4\n' > /etc/resolv.conf

# Ensure MMDS route exists (systemd-networkd should add it, but verify)
if ! ip route | grep -q "169.254.169.254"; then
    log_timing "Adding MMDS route..."
    ip route add 169.254.169.254/32 dev eth0 scope link 2>/dev/null || true
fi

# ============================================================================
# Phase 2: MMDS metadata fetch
# ============================================================================
log_timing "Checking MMDS reachability..."

# Quick MMDS check - should be available immediately
for i in {1..5}; do
    if curl -sf "http://169.254.169.254/" &>/dev/null; then
        log_timing "MMDS reachable after ${i}s"
        break
    fi
    sleep 1
done

MMDS_URL="http://169.254.169.254/latest"
log_timing "Fetching metadata from MMDS..."

# Helper function to strip JSON quotes from MMDS responses
strip_json_quotes() {
    sed 's/^"//;s/"$//'
}

# Get hostname
HOSTNAME_RAW=$(curl -sf "${MMDS_URL}/meta-data/local-hostname" 2>/dev/null || echo "")
HOSTNAME=$(echo "$HOSTNAME_RAW" | strip_json_quotes)
if [ -n "$HOSTNAME" ]; then
    log_timing "Setting hostname to: $HOSTNAME"
    hostnamectl set-hostname "$HOSTNAME"
fi

# Get SSH authorized keys from meta-data
SSH_KEYS_RAW=$(curl -sf "${MMDS_URL}/meta-data/ssh-authorized-keys" 2>/dev/null || echo "")
SSH_KEYS=$(echo "$SSH_KEYS_RAW" | strip_json_quotes)
if [ -n "$SSH_KEYS" ]; then
    log_timing "Installing SSH authorized keys..."
    SSH_DIR="/home/${VM_USER}/.ssh"
    mkdir -p "$SSH_DIR"
    echo "$SSH_KEYS" > "${SSH_DIR}/authorized_keys"
    chmod 700 "$SSH_DIR"
    chmod 600 "${SSH_DIR}/authorized_keys"
    chown -R "${VM_USER}:${VM_USER}" "$SSH_DIR"
    log_timing "SSH keys installed ($(echo "$SSH_KEYS" | wc -l) keys)"
else
    log_timing "No SSH authorized keys found in MMDS"
fi

# ============================================================================
# Phase 3: Tailscale setup
# ============================================================================
log_timing "Starting Tailscale configuration..."

# Get Tailscale auth key from meta-data
TS_AUTH_KEY_RAW=$(curl -sf "${MMDS_URL}/meta-data/tailscale-auth-key" 2>/dev/null || echo "")
TS_AUTH_KEY=$(echo "$TS_AUTH_KEY_RAW" | strip_json_quotes)

if [ -n "$TS_AUTH_KEY" ]; then
    log_timing "Found Tailscale auth key (${#TS_AUTH_KEY} chars)"

    TAILSCALE_SOCKET="/run/tailscale/tailscaled.sock"

    # tailscaled.service is disabled - we start it manually AFTER DNS is configured
    # This prevents tailscaled from hanging on DNS resolution to control.tailscale.com
    log_timing "Starting tailscaled (DNS is now configured)..."
    mkdir -p /run/tailscale /var/lib/tailscale

    # Check TUN device availability
    if [ -c /dev/net/tun ]; then
        log_timing "TUN device available"
    else
        log_timing "WARNING: TUN device not available, trying userspace networking"
    fi

    # Start tailscaled via systemd (service is disabled for boot but still available)
    # Uses userspace networking mode configured in /etc/default/tailscaled
    log_timing "Starting tailscaled.service..."
    systemctl start tailscaled.service 2>&1 || log_timing "WARNING: systemctl start returned error"

    # Wait for socket (typically ready in <1s with proper kernel)
    tailscaled_ready=false
    for i in {1..15}; do
        if [ -S "$TAILSCALE_SOCKET" ]; then
            log_timing "tailscaled socket ready after ${i}s"
            tailscaled_ready=true
            break
        fi
        sleep 1
    done

    if [ "$tailscaled_ready" = true ]; then
        # Connect to Tailscale
        log_timing "Connecting to Tailscale as '$HOSTNAME'..."
        if tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh --timeout=30s 2>&1; then
            log_timing "Tailscale connected successfully"
            TS_IP=$(tailscale ip -4 2>/dev/null || echo "unknown")
            log_timing "Tailscale IP: $TS_IP"
        else
            log_timing "ERROR: Tailscale up failed"
            journalctl -u tailscaled --no-pager -n 10 2>&1 || true
        fi
    else
        log_timing "ERROR: tailscaled socket not ready after 15s"
        journalctl -u tailscaled --no-pager -n 10 2>&1 || true
    fi
else
    log_timing "No Tailscale auth key found in MMDS"
fi

# ============================================================================
# Phase 4: Workspace setup
# ============================================================================
log_timing "Setting up workspace..."

if [ -d /workspace ]; then
    chown -R "${VM_USER}:${VM_USER}" /workspace 2>/dev/null || true
    log_timing "Workspace permissions set"
fi

# Setup Claude Code hooks for VM user
if [ -f /etc/stockyard/claude-hooks.json ]; then
    su - "${VM_USER}" -c "/usr/local/bin/setup-claude-hooks.sh" 2>/dev/null || true
    log_timing "Claude hooks configured"
fi

# Create run directory for snapshot socket
mkdir -p /run/stockyard
chmod 755 /run/stockyard

# ============================================================================
# Complete
# ============================================================================
BOOT_END=$(date +%s.%N)
TOTAL_TIME=$(echo "$BOOT_END - $BOOT_START" | bc)
log_timing "=== Stockyard Init Complete (total: ${TOTAL_TIME}s) ==="

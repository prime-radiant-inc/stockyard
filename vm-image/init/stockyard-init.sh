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
# Phase 1: Network (kernel may have configured static IP via boot args)
# ============================================================================
log_timing "Checking network status..."

# Check if kernel already configured eth0 (static IP via boot args)
if ip addr show eth0 2>/dev/null | grep -q "inet "; then
    CURRENT_IP=$(ip addr show eth0 | grep "inet " | awk '{print $2}' | cut -d/ -f1)
    log_timing "Network already configured (kernel): $CURRENT_IP"
else
    # Fallback: wait for DHCP (systemd-networkd)
    log_timing "No kernel IP, waiting for DHCP..."
    for i in {1..30}; do
        if ip addr show eth0 2>/dev/null | grep -q "inet "; then
            log_timing "Network configured via DHCP after $((i/10)).$((i%10))s"
            ip addr show eth0 | grep "inet "
            break
        fi
        sleep 0.1
    done
fi

# Set up DNS (systemd-resolved is disabled)
log_timing "Configuring DNS..."
rm -f /etc/resolv.conf 2>/dev/null || true
printf 'nameserver 8.8.8.8\nnameserver 8.8.4.4\n' > /etc/resolv.conf

# Ensure MMDS route exists
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

    # Dynamically choose TUN mode based on kernel support
    # Native TUN is faster than userspace networking when available
    if [ -c /dev/net/tun ]; then
        log_timing "TUN device available, using native networking"
        # Clear the userspace-networking flag set in Dockerfile
        sed -i 's/--tun=userspace-networking//' /etc/default/tailscaled
    else
        log_timing "TUN not available, using userspace networking"
    fi

    # Start tailscaled via systemd (service is disabled for boot but still available)
    log_timing "Starting tailscaled.service..."
    systemctl start tailscaled.service 2>&1 || log_timing "WARNING: systemctl start returned error"

    # Wait for socket with fast polling (100ms intervals, 15s timeout)
    tailscaled_ready=false
    wait_start=$(date +%s.%N)
    while true; do
        if [ -S "$TAILSCALE_SOCKET" ]; then
            elapsed=$(echo "$(date +%s.%N) - $wait_start" | bc)
            log_timing "tailscaled socket ready after ${elapsed}s"
            tailscaled_ready=true
            break
        fi
        elapsed=$(echo "$(date +%s.%N) - $wait_start" | bc)
        if [ "$(echo "$elapsed > 15" | bc)" -eq 1 ]; then
            break
        fi
        sleep 0.1
    done

    if [ "$tailscaled_ready" = true ]; then
        # Connect to Tailscale in BACKGROUND (non-blocking)
        # This saves ~1.5s on startup - SSH will be available shortly after init completes
        log_timing "Starting Tailscale connection (background)..."
        (
            if tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh --timeout=30s 2>&1; then
                TS_IP=$(tailscale ip -4 2>/dev/null || echo "unknown")
                echo "[$(date +%s.%N)] Tailscale connected: $TS_IP" >> /var/log/stockyard/tailscale.log
            else
                echo "[$(date +%s.%N)] Tailscale up failed" >> /var/log/stockyard/tailscale.log
                journalctl -u tailscaled --no-pager -n 10 >> /var/log/stockyard/tailscale.log 2>&1 || true
            fi
        ) &
        log_timing "Tailscale connecting in background (PID: $!)"
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

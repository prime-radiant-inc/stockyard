#!/bin/sh
# stockyard-init-alpine.sh
# Stockyard VM initialization script (Alpine/POSIX sh version)
# Fetches metadata from MMDS and configures the VM

set -e

# VM_USER is set in Dockerfile, default to mooby if not set
VM_USER="${VM_USER:-mooby}"

LOG_FILE="/var/log/stockyard/init.log"
mkdir -p /var/log/stockyard

# Redirect all output to both console and log file.
# Process substitution >(tee ...) is a bashism; use a named pipe instead.
_LOG_PIPE="/tmp/stockyard-init-log.pipe"
mkfifo "$_LOG_PIPE"
tee -a "$LOG_FILE" < "$_LOG_PIPE" &
exec > "$_LOG_PIPE" 2>&1

# ============================================================================
# Instrumentation: timing functions
# ============================================================================
BOOT_START=$(date +%s.%N 2>/dev/null || date +%s)

log_timing() {
    now=$(date +%s.%N 2>/dev/null || date +%s)
    elapsed=$(echo "$now - $BOOT_START" | bc 2>/dev/null || echo "?")
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
    # Fallback: wait for DHCP
    log_timing "No kernel IP, waiting for DHCP..."
    i=0
    while [ $i -lt 30 ]; do
        if ip addr show eth0 2>/dev/null | grep -q "inet "; then
            log_timing "Network configured via DHCP after $((i / 10)).$((i % 10))s"
            ip addr show eth0 | grep "inet "
            break
        fi
        sleep 0.1
        i=$((i + 1))
    done
fi

# Set up DNS (no systemd-resolved on Alpine)
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

# Quick MMDS check — should be available immediately
i=1
while [ $i -le 5 ]; do
    if curl -sf "http://169.254.169.254/" >/dev/null 2>&1; then
        log_timing "MMDS reachable after ${i}s"
        break
    fi
    sleep 1
    i=$((i + 1))
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
    hostname "$HOSTNAME"
    echo "$HOSTNAME" > /etc/hostname
    if ! grep -q "$HOSTNAME" /etc/hosts 2>/dev/null; then
        echo "127.0.1.1   $HOSTNAME" >> /etc/hosts
    fi
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
    # Clear setgid bit — sshd StrictModes rejects .ssh with setgid
    chmod g-s "$SSH_DIR"
    log_timing "SSH keys installed ($(echo "$SSH_KEYS" | wc -l) keys)"
else
    log_timing "No SSH authorized keys found in MMDS"
fi

# Get dotenv file
DOTENV_RAW=$(curl -sf "${MMDS_URL}/meta-data/dotenv" 2>/dev/null || echo "")
DOTENV=$(echo "$DOTENV_RAW" | strip_json_quotes)
if [ -n "$DOTENV" ] && [ "$DOTENV" != "null" ]; then
    log_timing "Installing dotenv file..."
    DOTENV_PATH="/home/${VM_USER}/.env"
    echo "$DOTENV" | base64 -d > "$DOTENV_PATH" 2>/dev/null || echo "$DOTENV" > "$DOTENV_PATH"
    chown "${VM_USER}:${VM_USER}" "$DOTENV_PATH"
    chmod 600 "$DOTENV_PATH"
    log_timing "Dotenv installed"
else
    log_timing "No dotenv file found in MMDS"
fi

# ============================================================================
# Phase 3: Tailscale setup
# ============================================================================
log_timing "Starting Tailscale configuration..."

# Check for pre-registered Tailscale state first
TS_STATE_B64=$(curl -sf "${MMDS_URL}/meta-data/tailscale-state" 2>/dev/null | strip_json_quotes)

if [ -n "$TS_STATE_B64" ] && [ "$TS_STATE_B64" != "null" ]; then
    log_timing "Found pre-registered Tailscale state"

    # Decode and write state before starting tailscaled
    mkdir -p /var/lib/tailscale /run/tailscale
    echo "$TS_STATE_B64" | base64 -d > /var/lib/tailscale/tailscaled.state
    chmod 600 /var/lib/tailscale/tailscaled.state

    log_timing "Starting tailscaled with pre-registered state..."
    TS_SOCKET="/run/tailscale/tailscaled.sock"

    # Start tailscaled in background
    /usr/sbin/tailscaled --state=/var/lib/tailscale/tailscaled.state \
        --socket="$TS_SOCKET" --port="${PORT:-41641}" >/dev/null 2>&1 &
    TAILSCALED_PID=$!
    log_timing "tailscaled started (PID: $TAILSCALED_PID)"

    # Wait for reconnection (should be fast with existing state)
    reconnect_start=$(date +%s.%N 2>/dev/null || date +%s)
    i=0
    while [ $i -lt 50 ]; do
        # First check if socket exists (tailscale CLI hangs without it)
        if [ -S "$TS_SOCKET" ] && timeout 1 tailscale status >/dev/null 2>&1; then
            elapsed=$(echo "$(date +%s.%N 2>/dev/null || date +%s) - $reconnect_start" | bc 2>/dev/null || echo "?")
            TS_IP=$(tailscale ip -4 2>/dev/null || echo "unknown")
            log_timing "Tailscale reconnected in ${elapsed}s: $TS_IP"
            break
        fi
        sleep 0.1
        i=$((i + 1))
    done
    # Log if we timed out waiting for Tailscale
    if [ $i -eq 50 ]; then
        log_timing "Tailscale reconnection timeout (continuing anyway)"
    fi
else
    # Fall back to auth key registration
    TS_AUTH_KEY_RAW=$(curl -sf "${MMDS_URL}/meta-data/tailscale-auth-key" 2>/dev/null || echo "")
    TS_AUTH_KEY=$(echo "$TS_AUTH_KEY_RAW" | strip_json_quotes)

    if [ -n "$TS_AUTH_KEY" ]; then
        log_timing "Using auth key for Tailscale registration (${#TS_AUTH_KEY} chars)"

        TAILSCALE_SOCKET="/run/tailscale/tailscaled.sock"

        log_timing "Starting tailscaled..."
        mkdir -p /run/tailscale /var/lib/tailscale

        # Build flags based on kernel support
        TS_FLAGS=""
        if [ -c /dev/net/tun ]; then
            log_timing "TUN device available, using native networking"
        else
            log_timing "TUN not available, using userspace networking"
            TS_FLAGS="--tun=userspace-networking"
        fi

        /usr/sbin/tailscaled --state=/var/lib/tailscale/tailscaled.state \
            --socket="$TAILSCALE_SOCKET" --port=41641 $TS_FLAGS >/dev/null 2>&1 &
        TAILSCALED_PID=$!
        log_timing "tailscaled started (PID: $TAILSCALED_PID)"

        # Wait for socket
        tailscaled_ready=false
        wait_start=$(date +%s.%N 2>/dev/null || date +%s)
        while true; do
            if [ -S "$TAILSCALE_SOCKET" ]; then
                elapsed=$(echo "$(date +%s.%N 2>/dev/null || date +%s) - $wait_start" | bc 2>/dev/null || echo "?")
                log_timing "tailscaled socket ready after ${elapsed}s"
                tailscaled_ready=true
                break
            fi
            elapsed=$(echo "$(date +%s.%N 2>/dev/null || date +%s) - $wait_start" | bc 2>/dev/null || echo "0")
            if [ "$(echo "$elapsed > 15" | bc 2>/dev/null || echo 0)" -eq 1 ]; then
                break
            fi
            sleep 0.1
        done

        if [ "$tailscaled_ready" = true ]; then
            # Connect to Tailscale in BACKGROUND (non-blocking)
            log_timing "Starting Tailscale connection (background)..."
            (
                if tailscale up --authkey="$TS_AUTH_KEY" --hostname="$HOSTNAME" --accept-routes --ssh --timeout=30s 2>&1; then
                    TS_IP=$(tailscale ip -4 2>/dev/null || echo "unknown")
                    echo "[$(date +%s.%N 2>/dev/null || date +%s)] Tailscale connected: $TS_IP" >> /var/log/stockyard/tailscale.log
                else
                    echo "[$(date +%s.%N 2>/dev/null || date +%s)] Tailscale up failed" >> /var/log/stockyard/tailscale.log
                fi
            ) &
            log_timing "Tailscale connecting in background (PID: $!)"
        else
            log_timing "ERROR: tailscaled socket not ready after 15s"
        fi
    else
        log_timing "No Tailscale configuration found in MMDS"
    fi
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
su - "${VM_USER}" -c "/usr/local/bin/setup-claude-hooks.sh" 2>/dev/null || true
log_timing "Claude hooks configured"

# Create run directory for snapshot socket
mkdir -p /run/stockyard
chmod 755 /run/stockyard

# ============================================================================
# Complete
# ============================================================================
BOOT_END=$(date +%s.%N 2>/dev/null || date +%s)
TOTAL_TIME=$(echo "$BOOT_END - $BOOT_START" | bc 2>/dev/null || echo "?")
log_timing "=== Stockyard Init Complete (total: ${TOTAL_TIME}s) ==="

# Clean up logging pipe
rm -f "$_LOG_PIPE" 2>/dev/null || true

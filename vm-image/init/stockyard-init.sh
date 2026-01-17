#!/bin/bash
# stockyard-init.sh
# Stockyard VM initialization script
# Runs after cloud-init, sets up runtime environment

set -e

LOG_FILE="/var/log/stockyard/init.log"
mkdir -p /var/log/stockyard

exec > >(tee -a "$LOG_FILE") 2>&1
echo "=== Stockyard Init $(date) ==="

# Source environment
if [ -f /etc/stockyard/env ]; then
    source /etc/stockyard/env
    echo "Loaded environment from /etc/stockyard/env"
fi

# Wait for network
echo "Waiting for network..."
for i in {1..30}; do
    if ping -c 1 8.8.8.8 &>/dev/null; then
        echo "Network is up"
        break
    fi
    sleep 1
done

# Setup workspace permissions
if [ -d /workspace ]; then
    chown -R vscode:vscode /workspace 2>/dev/null || true
    echo "Workspace permissions set"
fi

# Run cloud-init if not already done
if [ ! -f /var/lib/cloud/instance/boot-finished ]; then
    echo "Running cloud-init..."
    cloud-init init
    cloud-init modules --mode=config
    cloud-init modules --mode=final
fi

# Verify Tailscale (if configured)
if command -v tailscale &>/dev/null; then
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

#!/bin/sh
# stockyard-container-init.sh — entrypoint for the apple-container VM image.
#
# Responsibilities:
#   1. Opt-in Tailscale: if a Tailscale auth key is present in the environment,
#      start tailscaled in userspace-networking mode and `tailscale up --ssh`.
#      Userspace mode needs no TUN device and no privileges.
#   2. Start llm-proxy in the background.
#   3. exec `sleep infinity` so the container stays alive; access is via
#      `container exec`.
set -eu

# --- 1. Opt-in Tailscale ----------------------------------------------------
# The daemon forwards the auth key via VMConfig.Env. Accept either name.
TS_AUTHKEY="${TAILSCALE_AUTH_KEY:-${TS_AUTHKEY:-}}"
if [ -n "${TS_AUTHKEY}" ]; then
    echo "stockyard-container-init: starting tailscaled (userspace networking)"
    # tailscaled does not create these dirs; the firecracker init path
    # (stockyard-init.sh) pre-creates them for the same reason.
    mkdir -p /var/lib/tailscale /var/run/tailscale
    tailscaled \
        --tun=userspace-networking \
        --state=/var/lib/tailscale/tailscaled.state \
        --socket=/var/run/tailscale/tailscaled.sock &
    # Give tailscaled a moment to create its socket.
    i=0
    while [ ! -S /var/run/tailscale/tailscaled.sock ] && [ "$i" -lt 30 ]; do
        i=$((i + 1))
        sleep 0.2
    done
    if [ ! -S /var/run/tailscale/tailscaled.sock ]; then
        echo "stockyard-container-init: WARNING tailscaled socket never appeared; tailscale up will likely fail"
    fi
    # Bring Tailscale up in the background with a bounded timeout. Tailscale is
    # opt-in extra reachability here (CLI/dashboard access is via `container
    # exec`), so it must never gate the entrypoint:
    #   * --timeout stops a stuck control-plane handshake from blocking forever
    #     — e.g. a node still awaiting tailnet admin approval. Without it,
    #     `tailscale up` blocks (it does not exit non-zero), so the `||` guard
    #     never fires and llm-proxy / `sleep infinity` are never reached.
    #   * backgrounding decouples llm-proxy startup from Tailscale entirely.
    (
        if tailscale up --ssh --timeout=30s --authkey="${TS_AUTHKEY}" \
                --hostname="${STOCKYARD_HOSTNAME:-stockyard}"; then
            echo "stockyard-container-init: tailscale up succeeded"
        else
            echo "stockyard-container-init: WARNING tailscale up failed/timed out; continuing"
        fi
    ) &
else
    echo "stockyard-container-init: no Tailscale auth key; skipping Tailscale"
fi

# --- 2. Start llm-proxy -----------------------------------------------------
if command -v llm-proxy >/dev/null 2>&1; then
    echo "stockyard-container-init: starting llm-proxy"
    llm-proxy &
else
    echo "stockyard-container-init: WARNING llm-proxy not found on PATH"
fi

# --- 3. Keep the container alive -------------------------------------------
echo "stockyard-container-init: ready; sleeping"
exec sleep infinity

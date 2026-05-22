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
    tailscale up --ssh --authkey="${TS_AUTHKEY}" \
        --hostname="${STOCKYARD_HOSTNAME:-stockyard}" || \
        echo "stockyard-container-init: WARNING tailscale up failed; continuing"
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

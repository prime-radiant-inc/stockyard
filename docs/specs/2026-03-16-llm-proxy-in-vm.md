# llm-proxy in Stockyard VMs

**Date:** 2026-03-16

## Goal

Install llm-proxy in every Stockyard VM to transparently log all LLM API traffic from agent CLIs (Claude Code, Codex, etc.).

## Approach

llm-proxy runs inside the VM as a user-level systemd service. No host involvement — the VM is self-contained. No changes to llm-proxy itself or to stockyard Go code. This is purely a VM image change.

## Components

### 1. Binary installation (Dockerfile)

Download the llm-proxy release binary from GitHub during image build. Follows the existing pattern used for yq — direct curl of a versioned binary to `/usr/local/bin/`.

```dockerfile
ARG LLM_PROXY_VERSION=v0.x.x
RUN curl -fsSL https://github.com/prime-radiant-inc/llm-proxy/releases/download/${LLM_PROXY_VERSION}/llm-proxy-linux-amd64 \
    -o /usr/local/bin/llm-proxy \
    && chmod +x /usr/local/bin/llm-proxy
```

Pin to a specific version tag, not `latest`.

### 2. Systemd user service (Dockerfile)

Bake a service file into the image. Runs as the VM user (`mooby`), fixed port 12071.

```ini
[Unit]
Description=LLM API Logging Proxy
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/llm-proxy --port 12071
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
```

Install to `/home/${VM_USER}/.config/systemd/user/llm-proxy.service` and enable it so it starts with the user session.

### 3. Environment variables (Dockerfile)

Add to `/etc/profile.d/stockyard.sh` (already used for other stockyard env vars):

```bash
export ANTHROPIC_BASE_URL="http://localhost:12071/anthropic/api.anthropic.com"
export OPENAI_BASE_URL="http://localhost:12071/openai/api.openai.com"
```

These are picked up by Claude Code, Codex, and other agent CLIs automatically.

### 4. Logs

Default location: `~/.llm-provider-logs/`. No Loki, no config.toml, no special setup. Logs live and die with the VM. Callers who care can collect logs before destroying.

## What we don't need

- `llm-proxy --setup` / `--env` / portfile discovery
- Loki configuration
- config.toml (defaults are fine)
- Any changes to llm-proxy source
- Any changes to stockyard Go code

## Files changed

- `vm-image/Dockerfile` — install binary, service file, env vars

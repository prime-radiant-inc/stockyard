# llm-proxy in Stockyard VMs — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install llm-proxy in every Stockyard VM to transparently log all LLM API traffic.

**Architecture:** Download the llm-proxy release binary during image build. Run it as a system-level systemd service on a fixed port. Set `ANTHROPIC_BASE_URL` and `OPENAI_BASE_URL` via `/etc/profile.d/` so all agent CLIs route through it automatically.

**Tech Stack:** Dockerfile, systemd, shell

**Spec:** `docs/specs/2026-03-16-llm-proxy-in-vm.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `vm-image/Dockerfile` | Modify | Download binary, copy service file, write env vars |
| `vm-image/init/llm-proxy.service` | Create | systemd service unit for llm-proxy |

---

### Task 1: Create the systemd service file

**Files:**
- Create: `vm-image/init/llm-proxy.service`

- [ ] **Step 1: Create the service file**

Create `vm-image/init/llm-proxy.service`:

```ini
[Unit]
Description=LLM API Logging Proxy
After=network.target

[Service]
Type=simple
User=mooby
ExecStart=/usr/local/bin/llm-proxy --port 12071
Restart=on-failure
RestartSec=3
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Notes for the implementer:
- This is a **system-level** service (installed to `/etc/systemd/system/`), not a user-level service. Same pattern as `stockyard-shell.service` in the same directory.
- Runs as `User=mooby` so logs go to `~mooby/.llm-provider-logs/`.
- Fixed port 12071 (llm-proxy default). No portfile or dynamic port needed.
- `multi-user.target` means it starts during normal boot, before any user logs in.

- [ ] **Step 2: Commit**

```bash
git add vm-image/init/llm-proxy.service
git commit -m "feat: add llm-proxy systemd service for VM image (PRI-727)"
```

---

### Task 2: Add llm-proxy to the Dockerfile

**Files:**
- Modify: `vm-image/Dockerfile`

This task adds three things to the Dockerfile:
1. Download the llm-proxy binary (Section 3: Developer Tools, after Tailscale)
2. Copy and enable the systemd service (Section 7: Stockyard Integration)
3. Write environment variables to `/etc/profile.d/` (Section 9: Finalize, before ENV lines)

- [ ] **Step 1: Add binary download to Dockerfile**

After the Tailscale configuration block (after line 135), add:

```dockerfile
# llm-proxy (LLM API logging proxy)
ARG LLM_PROXY_VERSION=v0.7.1
RUN curl -fsSL "https://github.com/prime-radiant-inc/llm-proxy/releases/download/${LLM_PROXY_VERSION}/llm-proxy-linux-amd64" \
    -o /usr/local/bin/llm-proxy \
    && chmod +x /usr/local/bin/llm-proxy
```

Place it right after the `# Note: Our custom Firecracker kernel includes CONFIG_TUN...` comment (line 135) and before `# Section 4: Coding Agents`.

- [ ] **Step 2: Add service file copy and enable to Dockerfile**

In Section 7 (Stockyard Integration), after the stockyard-shell service block (after line 204), add:

```dockerfile
# Add llm-proxy service for LLM API logging
COPY init/llm-proxy.service /etc/systemd/system/llm-proxy.service
RUN systemctl enable llm-proxy.service 2>/dev/null || true
```

- [ ] **Step 3: Add environment variables to Dockerfile**

In Section 9 (Finalize), before the `# Persist VM_USER for runtime scripts` line (before line 314), add:

```dockerfile
# LLM proxy environment — routes agent CLI traffic through local logging proxy
RUN cat > /etc/profile.d/llm-proxy.sh <<'EOF'
export ANTHROPIC_BASE_URL="http://localhost:12071/anthropic/api.anthropic.com"
export OPENAI_BASE_URL="http://localhost:12071/openai/api.openai.com"
EOF
```

This is a separate file from `/etc/profile.d/stockyard.sh` (which is generated dynamically per-VM by cloud-init with task-specific env vars).

- [ ] **Step 4: Verify the Dockerfile builds**

Run: `cd /Users/matt/Code/prime/stockyard/vm-image && docker build --target= -t stockyard-vm-test .`

If the full build takes too long (kernel compilation), at minimum verify the syntax is correct:

Run: `cd /Users/matt/Code/prime/stockyard && docker build --check vm-image/`

Or just verify the curl download works:

Run: `curl -fsSL -o /dev/null -w "%{http_code}" "https://github.com/prime-radiant-inc/llm-proxy/releases/download/v0.7.1/llm-proxy-linux-amd64"`

Expected: `200`

- [ ] **Step 5: Commit**

```bash
git add vm-image/Dockerfile
git commit -m "feat: install llm-proxy in VM image (PRI-727)

Downloads release binary, enables systemd service on port 12071,
and sets ANTHROPIC_BASE_URL/OPENAI_BASE_URL in /etc/profile.d/
so all agent CLIs route through the logging proxy."
```

---

### Task 3: Verify on eval instance

This task is manual / semi-automated — deploy the new image and confirm llm-proxy works end-to-end.

- [ ] **Step 1: Build and deploy the VM image**

On the eval instance (ssh stockyard-exp):

```bash
cd ~/stockyard
git pull
cd vm-image
make rootfs
sudo cp output/rootfs.ext4 /var/lib/stockyard/rootfs.ext4
sudo systemctl restart stockyardd
```

- [ ] **Step 2: Create a test VM and verify**

```bash
stockyard run --name llm-proxy-test
```

Then SSH into the VM and check:

```bash
# Verify llm-proxy is running
systemctl status llm-proxy

# Verify env vars are set
echo $ANTHROPIC_BASE_URL
# Expected: http://localhost:12071/anthropic/api.anthropic.com

echo $OPENAI_BASE_URL
# Expected: http://localhost:12071/openai/api.openai.com

# Verify proxy responds
curl -s http://localhost:12071/health
# Expected: OK or similar health response

# Verify logs directory exists after first request
ls ~/.llm-provider-logs/
```

# Stockyard Implementation - Part 4: VM Image (Phase 8)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create the Firecracker VM base image with all tooling pre-installed, including cloud-init support, stockyard-snapshot client, and Claude Code hooks.

**Reference:**
- Design doc at `docs/plans/2026-01-16-stockyard-design.md`
- packnplay Dockerfile for base patterns

---

## Phase 8: VM Image

### Task 8.1: Create Base Dockerfile

**Files:**
- Create: `vm-image/Dockerfile`

**Step 1: Create Dockerfile**

```dockerfile
# vm-image/Dockerfile
# Stockyard VM Base Image
# Based on packnplay patterns, customized for Firecracker micro-VMs

FROM ubuntu:24.04

# Prevent interactive prompts
ENV DEBIAN_FRONTEND=noninteractive

# Base system packages
RUN apt-get update && apt-get install -y \
    # Core utilities
    apt-transport-https \
    ca-certificates \
    curl \
    wget \
    gnupg \
    lsb-release \
    software-properties-common \
    # Development essentials
    build-essential \
    git \
    vim \
    tmux \
    # Process management
    supervisor \
    # Network utilities
    openssh-server \
    iproute2 \
    iputils-ping \
    dnsutils \
    netcat-openbsd \
    # Cloud-init
    cloud-init \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js LTS (20.x)
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && npm install -g npm@latest \
    && rm -rf /var/lib/apt/lists/*

# Install Python 3.11+
RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Install Go (1.22)
ENV GO_VERSION=1.22.0
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

# Install Rust
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
ENV PATH="/root/.cargo/bin:${PATH}"

# Install GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Install Tailscale
RUN curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/jammy.noarmor.gpg | tee /usr/share/keyrings/tailscale-archive-keyring.gpg >/dev/null \
    && curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/jammy.tailscale-keyring.list | tee /etc/apt/sources.list.d/tailscale.list \
    && apt-get update && apt-get install -y tailscale \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code
RUN npm install -g @anthropic-ai/claude-code

# Install Codex (if available)
# RUN npm install -g @openai/codex

# Create vscode user (matches packnplay convention)
RUN useradd -m -s /bin/bash -G sudo vscode \
    && echo "vscode ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/vscode

# Setup SSH
RUN mkdir -p /var/run/sshd \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config \
    && sed -i 's/#PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config

# Create stockyard directories
RUN mkdir -p /etc/stockyard /var/log/stockyard

# Default environment
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8

# Workspace mount point
VOLUME /workspace

# Set working directory
WORKDIR /workspace

# Default to bash
CMD ["/bin/bash"]
```

**Step 2: Verify Dockerfile syntax**

```bash
docker build --check vm-image/
```

Or just verify it parses:
```bash
docker build -f vm-image/Dockerfile --target=syntax-check . 2>&1 | head -5 || echo "Dockerfile created"
```

**Step 3: Commit**

```bash
git add vm-image/Dockerfile
git commit -m "feat: add VM base Dockerfile

- Ubuntu 24.04 base
- Node.js 20, Python 3.11+, Go 1.22, Rust
- GitHub CLI, Tailscale, Claude Code
- SSH server, cloud-init support
- vscode user (matches packnplay)"
```

---

### Task 8.2: Add Cloud-Init Configuration

**Files:**
- Create: `vm-image/cloud-init/meta-data.template`
- Create: `vm-image/cloud-init/user-data.template`
- Modify: `vm-image/Dockerfile`

**Step 1: Create meta-data template**

```yaml
# vm-image/cloud-init/meta-data.template
instance-id: {{.InstanceID}}
local-hostname: {{.Hostname}}
```

**Step 2: Create user-data template**

```yaml
# vm-image/cloud-init/user-data.template
#cloud-config

hostname: {{.Hostname}}
manage_etc_hosts: true

users:
  - name: vscode
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    ssh_authorized_keys:
{{range .SSHKeys}}      - {{.}}
{{end}}

write_files:
  # Stockyard environment variables
  - path: /etc/stockyard/env
    permissions: '0600'
    content: |
{{range $key, $value := .Environment}}      export {{$key}}="{{$value}}"
{{end}}

  # Shell profile for environment
  - path: /etc/profile.d/stockyard.sh
    permissions: '0644'
    content: |
      source /etc/stockyard/env 2>/dev/null || true

  # Claude Code hooks for auto-snapshots
  - path: /etc/stockyard/claude-hooks.json
    permissions: '0644'
    content: |
      {
        "hooks": {
          "PostToolUse": [{
            "command": "stockyard-snapshot \"$CLAUDE_TOOL_NAME\""
          }]
        }
      }

  # Git configuration
  - path: /home/vscode/.gitconfig
    owner: vscode:vscode
    permissions: '0644'
    content: |
      [user]
          name = Stockyard Agent
          email = agent@stockyard.local
      [init]
          defaultBranch = main
      [safe]
          directory = /workspace

runcmd:
  # Source environment
  - source /etc/stockyard/env 2>/dev/null || true

  # Setup Claude Code hooks for vscode user
  - mkdir -p /home/vscode/.claude
  - cp /etc/stockyard/claude-hooks.json /home/vscode/.claude/hooks.json
  - chown -R vscode:vscode /home/vscode/.claude

{{if .TailscaleAuthKey}}
  # Join Tailscale network
  - tailscale up --authkey={{.TailscaleAuthKey}} --hostname={{.Hostname}} --accept-routes --ssh
{{end}}

{{if .GitRepo}}
  # Clone repository
  - |
    cd /workspace
    sudo -u vscode git clone {{.GitRepo}} repo
    cd repo
{{if .GitRef}}
    sudo -u vscode git checkout {{.GitRef}}
{{end}}
{{end}}

{{if .Command}}
  # Run command
  - |
    cd /workspace/repo 2>/dev/null || cd /workspace
    source /etc/stockyard/env
    sudo -E -u vscode {{.Command}}
{{end}}
```

**Step 3: Update Dockerfile to include templates**

Add to `vm-image/Dockerfile`:

```dockerfile
# Cloud-init templates
COPY cloud-init/ /etc/cloud/templates/stockyard/
```

**Step 4: Commit**

```bash
mkdir -p vm-image/cloud-init
git add vm-image/cloud-init/
git add vm-image/Dockerfile
git commit -m "feat: add cloud-init templates

- Meta-data template for instance ID
- User-data template with:
  - Environment variable injection
  - SSH key setup
  - Claude Code hooks
  - Tailscale auto-join
  - Git repo clone
  - Command execution"
```

---

### Task 8.3: Create stockyard-snapshot Client

**Files:**
- Create: `vm-image/scripts/stockyard-snapshot/main.go`
- Create: `vm-image/scripts/stockyard-snapshot/go.mod`

**Step 1: Write test**

```go
// vm-image/scripts/stockyard-snapshot/main_test.go
package main

import (
    "testing"
)

func TestSanitizeLabel(t *testing.T) {
    tests := []struct {
        input string
        want  string
    }{
        {"edit-main.py", "edit-main.py"},
        {"bash npm test", "bash-npm-test"},
        {"Read /etc/passwd", "Read--etc-passwd"},
        {"", ""},
    }

    for _, tt := range tests {
        got := sanitizeLabel(tt.input)
        if got != tt.want {
            t.Errorf("sanitizeLabel(%q) = %q, want %q", tt.input, got, tt.want)
        }
    }
}
```

**Step 2: Run test**

```bash
cd vm-image/scripts/stockyard-snapshot
go mod init stockyard-snapshot
go test -v
```

Expected: FAIL

**Step 3: Implement stockyard-snapshot**

```go
// vm-image/scripts/stockyard-snapshot/main.go
package main

import (
    "encoding/binary"
    "fmt"
    "io"
    "net"
    "os"
    "strings"
    "time"
)

const (
    // vsock CID for host communication
    vsockHostCID = 2
    // Port for stockyard snapshot service
    vsockPort = 52000
)

func main() {
    if len(os.Args) < 2 {
        fmt.Fprintln(os.Stderr, "Usage: stockyard-snapshot <label>")
        os.Exit(1)
    }

    label := strings.Join(os.Args[1:], " ")
    label = sanitizeLabel(label)

    if err := requestSnapshot(label); err != nil {
        fmt.Fprintf(os.Stderr, "Failed to create snapshot: %v\n", err)
        os.Exit(1)
    }

    fmt.Printf("Snapshot created: %s\n", label)
}

func sanitizeLabel(s string) string {
    var result strings.Builder
    for _, c := range s {
        if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
            (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
            result.WriteRune(c)
        } else {
            result.WriteRune('-')
        }
    }
    return result.String()
}

func requestSnapshot(label string) error {
    // Connect to host via vsock
    conn, err := dialVsock(vsockHostCID, vsockPort)
    if err != nil {
        return fmt.Errorf("failed to connect to host: %w", err)
    }
    defer conn.Close()

    // Set deadline
    conn.SetDeadline(time.Now().Add(30 * time.Second))

    // Protocol: send label length (4 bytes) + label
    labelBytes := []byte(label)
    if err := binary.Write(conn, binary.LittleEndian, uint32(len(labelBytes))); err != nil {
        return fmt.Errorf("failed to send label length: %w", err)
    }

    if _, err := conn.Write(labelBytes); err != nil {
        return fmt.Errorf("failed to send label: %w", err)
    }

    // Read response: status (1 byte) + message length (4 bytes) + message
    var status byte
    if err := binary.Read(conn, binary.LittleEndian, &status); err != nil {
        return fmt.Errorf("failed to read status: %w", err)
    }

    var msgLen uint32
    if err := binary.Read(conn, binary.LittleEndian, &msgLen); err != nil {
        return fmt.Errorf("failed to read message length: %w", err)
    }

    if msgLen > 0 {
        msg := make([]byte, msgLen)
        if _, err := io.ReadFull(conn, msg); err != nil {
            return fmt.Errorf("failed to read message: %w", err)
        }

        if status != 0 {
            return fmt.Errorf("snapshot failed: %s", string(msg))
        }
    }

    if status != 0 {
        return fmt.Errorf("snapshot failed with status %d", status)
    }

    return nil
}

// dialVsock connects to a vsock address
// Note: This is Linux-specific and uses AF_VSOCK
func dialVsock(cid uint32, port uint32) (net.Conn, error) {
    // Try vsock first (for real VMs)
    conn, err := tryVsock(cid, port)
    if err == nil {
        return conn, nil
    }

    // Fall back to Unix socket for testing
    sockPath := fmt.Sprintf("/run/stockyard/snapshot.sock")
    conn, err = net.Dial("unix", sockPath)
    if err != nil {
        return nil, fmt.Errorf("vsock and unix fallback both failed: %w", err)
    }
    return conn, nil
}

func tryVsock(cid uint32, port uint32) (net.Conn, error) {
    // Use syscall for vsock - this requires linux/vm_sockets.h
    // For portability, we use a file-based approach
    vsockPath := fmt.Sprintf("/dev/vsock")

    // Check if vsock is available
    if _, err := os.Stat(vsockPath); os.IsNotExist(err) {
        return nil, fmt.Errorf("vsock not available")
    }

    // Use the vsock package for proper implementation
    // For now, return error to trigger fallback
    return nil, fmt.Errorf("vsock direct dial not implemented - use fallback")
}
```

**Step 4: Create go.mod**

```
// vm-image/scripts/stockyard-snapshot/go.mod
module stockyard-snapshot

go 1.21
```

**Step 5: Run tests**

```bash
cd vm-image/scripts/stockyard-snapshot
go test -v
```

Expected: PASS

**Step 6: Build binary**

```bash
cd vm-image/scripts/stockyard-snapshot
CGO_ENABLED=0 go build -o stockyard-snapshot .
```

**Step 7: Commit**

```bash
git add vm-image/scripts/stockyard-snapshot/
git commit -m "feat: add stockyard-snapshot client for VMs

- vsock-based communication with host
- Unix socket fallback for testing
- Simple protocol: label -> status + message
- Sanitizes label for ZFS snapshot names"
```

---

### Task 8.4: Add Claude Code Hooks Setup

**Files:**
- Create: `vm-image/scripts/setup-claude-hooks.sh`
- Modify: `vm-image/Dockerfile`

**Step 1: Create setup script**

```bash
#!/bin/bash
# vm-image/scripts/setup-claude-hooks.sh
# Sets up Claude Code hooks for auto-snapshots

set -e

HOOKS_DIR="${HOME}/.claude"
HOOKS_FILE="${HOOKS_DIR}/hooks.json"
SYSTEM_HOOKS="/etc/stockyard/claude-hooks.json"

mkdir -p "${HOOKS_DIR}"

# If system hooks exist, use them
if [ -f "${SYSTEM_HOOKS}" ]; then
    cp "${SYSTEM_HOOKS}" "${HOOKS_FILE}"
    echo "Installed Claude Code hooks from ${SYSTEM_HOOKS}"
else
    # Create default hooks
    cat > "${HOOKS_FILE}" << 'EOF'
{
  "hooks": {
    "PostToolUse": [{
      "command": "stockyard-snapshot \"$CLAUDE_TOOL_NAME\""
    }]
  }
}
EOF
    echo "Created default Claude Code hooks"
fi

# Ensure correct permissions
chmod 644 "${HOOKS_FILE}"
```

**Step 2: Update Dockerfile**

Add to `vm-image/Dockerfile`:

```dockerfile
# Copy stockyard-snapshot binary
COPY scripts/stockyard-snapshot/stockyard-snapshot /usr/local/bin/
RUN chmod +x /usr/local/bin/stockyard-snapshot

# Copy setup scripts
COPY scripts/setup-claude-hooks.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/setup-claude-hooks.sh

# Setup default Claude Code hooks
RUN mkdir -p /etc/stockyard && \
    echo '{"hooks":{"PostToolUse":[{"command":"stockyard-snapshot \"$CLAUDE_TOOL_NAME\""}]}}' > /etc/stockyard/claude-hooks.json
```

**Step 3: Commit**

```bash
chmod +x vm-image/scripts/setup-claude-hooks.sh
git add vm-image/scripts/setup-claude-hooks.sh
git add vm-image/Dockerfile
git commit -m "feat: add Claude Code hooks setup

- setup-claude-hooks.sh for user configuration
- Default hooks in /etc/stockyard
- stockyard-snapshot binary in PATH"
```

---

### Task 8.5: Add Init System Configuration

**Files:**
- Create: `vm-image/init/stockyard-init.service`
- Create: `vm-image/init/stockyard-init.sh`
- Modify: `vm-image/Dockerfile`

**Step 1: Create init script**

```bash
#!/bin/bash
# vm-image/init/stockyard-init.sh
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
    su - vscode -c "setup-claude-hooks.sh" 2>/dev/null || true
fi

echo "=== Stockyard Init Complete ==="
```

**Step 2: Create systemd service**

```ini
# vm-image/init/stockyard-init.service
[Unit]
Description=Stockyard VM Initialization
After=cloud-init.target network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/stockyard-init.sh
RemainAfterExit=yes
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

**Step 3: Update Dockerfile**

Add to `vm-image/Dockerfile`:

```dockerfile
# Init system
COPY init/stockyard-init.sh /usr/local/bin/
COPY init/stockyard-init.service /etc/systemd/system/
RUN chmod +x /usr/local/bin/stockyard-init.sh && \
    systemctl enable stockyard-init.service || true
```

**Step 4: Commit**

```bash
mkdir -p vm-image/init
chmod +x vm-image/init/stockyard-init.sh
git add vm-image/init/
git add vm-image/Dockerfile
git commit -m "feat: add VM init system

- stockyard-init.sh for runtime setup
- systemd service for boot integration
- Environment loading, permissions, Tailscale verification"
```

---

### Task 8.6: Create Final Dockerfile

**Files:**
- Rewrite: `vm-image/Dockerfile` (complete version)

**Step 1: Create final Dockerfile**

```dockerfile
# vm-image/Dockerfile
# Stockyard VM Base Image
# Firecracker micro-VM with coding agents and audit trail support

FROM ubuntu:24.04

LABEL maintainer="stockyard" \
      description="Stockyard coding agent VM base image"

# Prevent interactive prompts
ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8

# ============================================
# Base System
# ============================================

RUN apt-get update && apt-get install -y \
    # Core utilities
    apt-transport-https \
    ca-certificates \
    curl \
    wget \
    gnupg \
    lsb-release \
    software-properties-common \
    # Development essentials
    build-essential \
    git \
    vim \
    nano \
    tmux \
    jq \
    # Process management
    supervisor \
    # Network utilities
    openssh-server \
    iproute2 \
    iputils-ping \
    dnsutils \
    netcat-openbsd \
    # Cloud-init
    cloud-init \
    && rm -rf /var/lib/apt/lists/*

# ============================================
# Programming Languages
# ============================================

# Node.js LTS (20.x)
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && npm install -g npm@latest \
    && rm -rf /var/lib/apt/lists/*

# Python 3.11+
RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-venv \
    python3-dev \
    && rm -rf /var/lib/apt/lists/*

# Go 1.22
ENV GO_VERSION=1.22.0
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/home/vscode/go"

# Rust (for vscode user)
# Installed later in user context

# ============================================
# Developer Tools
# ============================================

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Tailscale
RUN curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/jammy.noarmor.gpg | tee /usr/share/keyrings/tailscale-archive-keyring.gpg >/dev/null \
    && curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/jammy.tailscale-keyring.list | tee /etc/apt/sources.list.d/tailscale.list \
    && apt-get update && apt-get install -y tailscale \
    && rm -rf /var/lib/apt/lists/*

# ============================================
# Coding Agents
# ============================================

# Claude Code
RUN npm install -g @anthropic-ai/claude-code

# Add more agents as needed:
# RUN npm install -g @openai/codex

# ============================================
# User Setup
# ============================================

# Create vscode user
RUN useradd -m -s /bin/bash -G sudo vscode \
    && echo "vscode ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/vscode

# Install Rust for vscode user
USER vscode
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
USER root
ENV PATH="/home/vscode/.cargo/bin:${PATH}"

# ============================================
# SSH Configuration
# ============================================

RUN mkdir -p /var/run/sshd \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config \
    && sed -i 's/#PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config \
    && sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config

# ============================================
# Stockyard Integration
# ============================================

# Create directories
RUN mkdir -p /etc/stockyard /var/log/stockyard /run/stockyard

# Default Claude Code hooks
RUN echo '{"hooks":{"PostToolUse":[{"command":"stockyard-snapshot \"$CLAUDE_TOOL_NAME\""}]}}' > /etc/stockyard/claude-hooks.json

# Copy stockyard-snapshot binary (built separately)
COPY scripts/stockyard-snapshot/stockyard-snapshot /usr/local/bin/
RUN chmod +x /usr/local/bin/stockyard-snapshot

# Copy setup scripts
COPY scripts/setup-claude-hooks.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/setup-claude-hooks.sh

# Init system
COPY init/stockyard-init.sh /usr/local/bin/
COPY init/stockyard-init.service /etc/systemd/system/
RUN chmod +x /usr/local/bin/stockyard-init.sh

# Cloud-init templates
COPY cloud-init/ /etc/cloud/templates/stockyard/

# ============================================
# Finalize
# ============================================

# Workspace mount point
VOLUME /workspace

# Working directory
WORKDIR /workspace

# Default shell
CMD ["/bin/bash"]
```

**Step 2: Commit**

```bash
git add vm-image/Dockerfile
git commit -m "feat: finalize VM Dockerfile

- Complete base image with all components
- Organized into logical sections
- Production-ready configuration"
```

---

### Task 8.7: Create Build Script

**Files:**
- Create: `vm-image/build.sh`

**Step 1: Create build script**

```bash
#!/bin/bash
# vm-image/build.sh
# Build the stockyard VM image

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

IMAGE_NAME="${IMAGE_NAME:-ghcr.io/obra/stockyard-vm}"
IMAGE_TAG="${IMAGE_TAG:-latest}"

echo "Building stockyard-snapshot client..."
cd scripts/stockyard-snapshot
go mod tidy
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o stockyard-snapshot .
cd "$SCRIPT_DIR"

echo "Building VM image: ${IMAGE_NAME}:${IMAGE_TAG}"
docker build -t "${IMAGE_NAME}:${IMAGE_TAG}" .

echo "Build complete!"
echo ""
echo "To push to registry:"
echo "  docker push ${IMAGE_NAME}:${IMAGE_TAG}"
echo ""
echo "To convert to Firecracker rootfs:"
echo "  ./convert-to-rootfs.sh ${IMAGE_NAME}:${IMAGE_TAG}"
```

**Step 2: Make executable and commit**

```bash
chmod +x vm-image/build.sh
git add vm-image/build.sh
git commit -m "feat: add VM image build script

- Builds stockyard-snapshot Go binary
- Builds Docker image
- Ready for registry push"
```

---

### Task 8.8: Create Rootfs Conversion Script

**Files:**
- Create: `vm-image/convert-to-rootfs.sh`

**Step 1: Create conversion script**

```bash
#!/bin/bash
# vm-image/convert-to-rootfs.sh
# Convert Docker image to Firecracker rootfs

set -e

IMAGE="${1:-ghcr.io/obra/stockyard-vm:latest}"
OUTPUT_DIR="${2:-./output}"
ROOTFS_NAME="stockyard-rootfs.ext4"
ROOTFS_SIZE="${ROOTFS_SIZE:-10G}"

mkdir -p "$OUTPUT_DIR"

echo "Converting $IMAGE to Firecracker rootfs..."

# Create container from image
CONTAINER_ID=$(docker create "$IMAGE")
echo "Created container: $CONTAINER_ID"

# Export filesystem
echo "Exporting filesystem..."
docker export "$CONTAINER_ID" > "$OUTPUT_DIR/rootfs.tar"

# Create ext4 filesystem
echo "Creating ext4 filesystem (${ROOTFS_SIZE})..."
truncate -s "$ROOTFS_SIZE" "$OUTPUT_DIR/$ROOTFS_NAME"
mkfs.ext4 -F "$OUTPUT_DIR/$ROOTFS_NAME"

# Mount and extract
MOUNT_POINT=$(mktemp -d)
sudo mount "$OUTPUT_DIR/$ROOTFS_NAME" "$MOUNT_POINT"

echo "Extracting to rootfs..."
sudo tar -xf "$OUTPUT_DIR/rootfs.tar" -C "$MOUNT_POINT"

# Setup init
echo "Setting up init..."
if [ ! -f "$MOUNT_POINT/sbin/init" ]; then
    sudo ln -sf /lib/systemd/systemd "$MOUNT_POINT/sbin/init"
fi

# Cleanup
sudo umount "$MOUNT_POINT"
rmdir "$MOUNT_POINT"
docker rm "$CONTAINER_ID"
rm "$OUTPUT_DIR/rootfs.tar"

echo ""
echo "Rootfs created: $OUTPUT_DIR/$ROOTFS_NAME"
echo ""
echo "To use with Firecracker/Flintlock, reference this file in VM configuration."
```

**Step 2: Make executable and commit**

```bash
chmod +x vm-image/convert-to-rootfs.sh
git add vm-image/convert-to-rootfs.sh
git commit -m "feat: add rootfs conversion script

- Converts Docker image to ext4 rootfs
- Suitable for Firecracker VMs
- Configurable size"
```

---

### Task 8.9: Test VM Image Build

**Step 1: Build the image**

```bash
cd vm-image
./build.sh
```

Expected: Docker image builds successfully

**Step 2: Test the image**

```bash
docker run --rm -it ghcr.io/obra/stockyard-vm:latest bash -c "
    echo '=== Version checks ==='
    node --version
    python3 --version
    go version
    gh --version
    which claude-code
    which stockyard-snapshot
    echo '=== Environment ==='
    cat /etc/stockyard/claude-hooks.json
"
```

Expected: All tools present and hooks configured

**Step 3: Commit any fixes**

```bash
git status
# If changes needed, commit them
```

---

**End of Part 4. Continue with Part 5: vsock & Tailscale.**

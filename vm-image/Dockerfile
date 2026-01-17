# Stockyard VM Image
# Ubuntu 24.04 with development tools for Claude Code sessions
#
# This image is designed to be converted to a Firecracker rootfs.
# Build with: ./build.sh
# Convert with: ./convert-to-rootfs.sh

FROM ubuntu:24.04

LABEL maintainer="Stockyard Project"
LABEL description="Development VM image for Claude Code sessions with Firecracker"

# Prevent interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive

# ============================================================================
# Section 1: Base System
# ============================================================================

RUN apt-get update && apt-get install -y \
    # Essential tools
    curl \
    wget \
    git \
    jq \
    unzip \
    gnupg \
    ca-certificates \
    software-properties-common \
    apt-transport-https \
    # SSH server
    openssh-server \
    # Cloud-init for VM configuration
    cloud-init \
    # Build essentials
    build-essential \
    pkg-config \
    # Networking tools
    iproute2 \
    iptables \
    dnsutils \
    # System utilities
    sudo \
    vim \
    less \
    && rm -rf /var/lib/apt/lists/*

# ============================================================================
# Section 2: Programming Languages
# ============================================================================

# Python 3.11+
RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Node.js 20
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Go 1.22
RUN curl -fsSL https://go.dev/dl/go1.22.10.linux-amd64.tar.gz | tar -C /usr/local -xzf - \
    && ln -s /usr/local/go/bin/go /usr/local/bin/go \
    && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

# Rust (system-wide for root, user install happens later)
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y \
    && ln -s /root/.cargo/bin/rustc /usr/local/bin/rustc \
    && ln -s /root/.cargo/bin/cargo /usr/local/bin/cargo \
    && ln -s /root/.cargo/bin/rustup /usr/local/bin/rustup

# ============================================================================
# Section 3: Developer Tools
# ============================================================================

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update \
    && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Tailscale (for secure networking)
RUN curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg | tee /usr/share/keyrings/tailscale-archive-keyring.gpg > /dev/null \
    && curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.tailscale-keyring.list | tee /etc/apt/sources.list.d/tailscale.list \
    && apt-get update \
    && apt-get install -y tailscale \
    && rm -rf /var/lib/apt/lists/*

# ============================================================================
# Section 4: Coding Agents
# ============================================================================

# Claude Code (Anthropic's CLI for Claude)
RUN npm install -g @anthropic-ai/claude-code

# ============================================================================
# Section 5: User Setup
# ============================================================================

# Create vscode user with sudo privileges
RUN useradd -m -s /bin/bash -G sudo vscode \
    && echo "vscode ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/vscode \
    && chmod 0440 /etc/sudoers.d/vscode

# Set up Rust for vscode user
RUN su - vscode -c "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y"

# Create workspace directory
RUN mkdir -p /workspace && chown vscode:vscode /workspace

# ============================================================================
# Section 6: SSH Configuration
# ============================================================================

RUN mkdir -p /var/run/sshd \
    && sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config \
    && sed -i 's/#PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config

# ============================================================================
# Section 7: Stockyard Integration
# ============================================================================

# Create stockyard directories
RUN mkdir -p /etc/stockyard /var/log/stockyard /run/stockyard

# Copy stockyard-snapshot binary (built externally by build.sh)
COPY scripts/stockyard-snapshot/stockyard-snapshot /usr/local/bin/stockyard-snapshot
RUN chmod +x /usr/local/bin/stockyard-snapshot

# Copy Claude Code hooks setup script
COPY scripts/setup-claude-hooks.sh /usr/local/bin/setup-claude-hooks.sh
RUN chmod +x /usr/local/bin/setup-claude-hooks.sh

# Copy init system configuration
COPY init/stockyard-init.sh /usr/local/bin/stockyard-init.sh
RUN chmod +x /usr/local/bin/stockyard-init.sh

COPY init/stockyard-init.service /etc/systemd/system/stockyard-init.service
RUN systemctl enable stockyard-init.service 2>/dev/null || true

# Configure cloud-init for VM initialization
# Use IMDS datasource first (for Firecracker MMDS), fall back to NoCloud
RUN mkdir -p /var/lib/cloud/seed/nocloud /etc/cloud/cloud.cfg.d \
    && cat > /etc/cloud/cloud.cfg.d/99-datasource.cfg <<'EOF'
datasource_list: [ IMDS, NoCloud, None ]
datasource:
  IMDS:
    metadata_urls: ["http://169.254.169.254"]
EOF

# Configure network for Firecracker MMDS access
# Static IP on 192.168.100.0/24 network, plus route to 169.254.169.254 for MMDS
# The IP will be set by cloud-init, but we need the MMDS route to be available early
RUN mkdir -p /etc/systemd/network \
    && cat > /etc/systemd/network/10-eth0.network <<'EOF'
[Match]
Name=eth0

[Network]
Address=192.168.100.2/24
Gateway=192.168.100.1
DNS=8.8.8.8

[Route]
Destination=169.254.169.254/32
Scope=link
EOF
# Enable systemd-networkd
RUN systemctl enable systemd-networkd 2>/dev/null || true

# ============================================================================
# Section 8: MicroVM Kernel
# ============================================================================

# Download a prebuilt kernel compatible with Firecracker/Cloud Hypervisor
RUN curl -fsSL -o /boot/vmlinuz \
    https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/kernels/vmlinux.bin

# ============================================================================
# Section 9: Finalize
# ============================================================================

# Set environment variables
ENV GOPATH=/home/vscode/go
ENV PATH="/home/vscode/.cargo/bin:/usr/local/go/bin:${PATH}"

# Default working directory
WORKDIR /home/vscode

# Expose SSH port
EXPOSE 22

# Default command (will be overridden by cloud-init/systemd in VM)
CMD ["/sbin/init"]

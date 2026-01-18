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

# Configurable VM user (default: mooby)
ARG VM_USER=mooby

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
    cmake \
    # C/C++ tools
    clang \
    clang-format \
    clang-tidy \
    # Networking tools
    iproute2 \
    iptables \
    dnsutils \
    iputils-ping \
    curl \
    # System utilities
    sudo \
    vim \
    less \
    tmux \
    && rm -rf /var/lib/apt/lists/*

# yq (YAML processor) - install from GitHub releases
RUN curl -fsSL https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64 -o /usr/local/bin/yq \
    && chmod +x /usr/local/bin/yq

# ============================================================================
# Section 2: Programming Languages
# ============================================================================

# Python 3.11+
RUN apt-get update && apt-get install -y \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# uv - modern Python package manager (much faster than pip)
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

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

# AWS CLI v2
RUN curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip" \
    && unzip awscliv2.zip \
    && ./aws/install \
    && rm -rf aws awscliv2.zip

# Azure CLI
RUN curl -sL https://aka.ms/InstallAzureCLIDeb | bash

# Google Cloud CLI
RUN curl https://packages.cloud.google.com/apt/doc/apt-key.gpg | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" | tee /etc/apt/sources.list.d/google-cloud-sdk.list \
    && apt-get update \
    && apt-get install -y google-cloud-cli \
    && rm -rf /var/lib/apt/lists/*

# Tailscale (for secure networking)
RUN curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg | tee /usr/share/keyrings/tailscale-archive-keyring.gpg > /dev/null \
    && curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.tailscale-keyring.list | tee /etc/apt/sources.list.d/tailscale.list \
    && apt-get update \
    && apt-get install -y tailscale \
    && rm -rf /var/lib/apt/lists/*

# Configure tailscaled for Firecracker (no netfilter support)
# Use userspace networking mode since the kernel lacks iptables/nftables
RUN echo 'FLAGS="--tun=userspace-networking"' >> /etc/default/tailscaled

# ============================================================================
# Section 4: Coding Agents
# ============================================================================

# Claude Code (Anthropic's CLI for Claude)
RUN npm install -g @anthropic-ai/claude-code

# OpenAI Codex CLI
RUN npm install -g @openai/codex

# ============================================================================
# Section 4b: Linters and Formatters
# ============================================================================

# Go linter (golangci-lint)
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /usr/local/bin

# Python linter/formatter (ruff - extremely fast)
RUN /usr/local/bin/uv tool install ruff \
    && ln -s /root/.local/bin/ruff /usr/local/bin/ruff

# JavaScript/TypeScript linters and formatters
RUN npm install -g eslint prettier typescript

# ============================================================================
# Section 5: User Setup
# ============================================================================

# Create VM user with sudo privileges
RUN useradd -m -s /bin/bash -G sudo ${VM_USER} \
    && echo "${VM_USER} ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/${VM_USER} \
    && chmod 0440 /etc/sudoers.d/${VM_USER}

# Set up Rust for VM user
RUN su - ${VM_USER} -c "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y"

# Create workspace directory
RUN mkdir -p /workspace && chown ${VM_USER}:${VM_USER} /workspace

# ============================================================================
# Section 6: SSH Configuration
# ============================================================================

RUN mkdir -p /var/run/sshd \
    && sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config \
    && sed -i 's/#PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config

# SSH authorized keys are delivered via MMDS at runtime (see stockyard-init.sh)
# This allows per-VM key configuration instead of baking keys into the base image

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

# Disable cloud-init - stockyard-init.sh handles all VM initialization
# cloud-init can hang waiting for IMDS before MMDS route is set up
RUN systemctl mask cloud-init.service cloud-init-local.service \
    cloud-config.service cloud-final.service 2>/dev/null || true

# Configure network for DHCP with MMDS route
# IP is obtained via DHCP from stockyard daemon's dnsmasq
# MMDS route is added for Firecracker metadata access
RUN mkdir -p /etc/systemd/network \
    && cat > /etc/systemd/network/10-eth0.network <<'EOF'
[Match]
Name=eth0

[Network]
DHCP=yes
DNS=8.8.8.8

[DHCP]
UseDNS=false
UseRoutes=true

[Route]
Destination=169.254.169.254/32
Scope=link
EOF
# Enable systemd-networkd
RUN systemctl enable systemd-networkd 2>/dev/null || true

# Disable systemd-resolved (causes boot delays and DNS issues in Firecracker)
# resolv.conf is set up by stockyard-init.sh at boot time
RUN systemctl disable systemd-resolved 2>/dev/null || true

# ============================================================================
# Section 8: MicroVM Kernel
# ============================================================================

# Download a prebuilt kernel compatible with Firecracker/Cloud Hypervisor
RUN curl -fsSL -o /boot/vmlinuz \
    https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/kernels/vmlinux.bin

# ============================================================================
# Section 9: Finalize
# ============================================================================

# Persist VM_USER for runtime scripts
ENV VM_USER=${VM_USER}

# Set environment variables
ENV GOPATH=/home/${VM_USER}/go
ENV PATH="/home/${VM_USER}/.cargo/bin:/usr/local/go/bin:${PATH}"

# Default working directory
WORKDIR /home/${VM_USER}

# Expose SSH port
EXPOSE 22

# Default command (will be overridden by cloud-init/systemd in VM)
CMD ["/sbin/init"]

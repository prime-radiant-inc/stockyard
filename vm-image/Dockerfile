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
    bc \
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

# Google Cloud CLI (standalone archive — apt repo has stale package versions)
RUN curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz | tar -xzf - -C /usr/local \
    && /usr/local/google-cloud-sdk/install.sh --quiet --path-update=false \
    && ln -s /usr/local/google-cloud-sdk/bin/gcloud /usr/local/bin/gcloud \
    && ln -s /usr/local/google-cloud-sdk/bin/gsutil /usr/local/bin/gsutil \
    && ln -s /usr/local/google-cloud-sdk/bin/bq /usr/local/bin/bq

# Tailscale (for secure networking)
RUN curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg | tee /usr/share/keyrings/tailscale-archive-keyring.gpg > /dev/null \
    && curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.tailscale-keyring.list | tee /etc/apt/sources.list.d/tailscale.list \
    && apt-get update \
    && apt-get install -y tailscale \
    && rm -rf /var/lib/apt/lists/*

# Configure tailscaled for Firecracker
# DISABLE auto-start - stockyard-init.sh will start tailscaled AFTER DNS is configured
# This prevents tailscaled from hanging while trying to resolve control.tailscale.com
RUN systemctl disable tailscaled.service 2>/dev/null || true

# Note: Our custom Firecracker kernel includes CONFIG_TUN and CONFIG_NF_TABLES,
# so tailscaled uses native TUN mode (no userspace networking needed)

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

# Copy stockyard-shell binary for vsock terminal access
COPY scripts/stockyard-shell/stockyard-shell /usr/local/bin/stockyard-shell
RUN chmod +x /usr/local/bin/stockyard-shell

# Add the systemd service for stockyard-shell
COPY init/stockyard-shell.service /etc/systemd/system/stockyard-shell.service
RUN systemctl enable stockyard-shell.service 2>/dev/null || true

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

# Network config for eth0 - kernel provides static IP via boot args
# DHCP is disabled since we always use kernel IP (ip=...:eth0:off)
# MMDS route is added for Firecracker metadata access
RUN mkdir -p /etc/systemd/network \
    && cat > /etc/systemd/network/10-eth0.network <<'EOF'
[Match]
Name=eth0

[Network]
# Static IP is configured by kernel boot args, no DHCP needed
DHCP=no
DNS=8.8.8.8
DNS=8.8.4.4

[Route]
# MMDS metadata service
Destination=169.254.169.254/32
Scope=link
EOF
# Enable systemd-networkd
RUN systemctl enable systemd-networkd 2>/dev/null || true

# Configure systemd-networkd-wait-online for fast boot:
# - Only wait for eth0 (the only interface in Firecracker VMs)
# - Use 10s timeout instead of default 120s
# - Accept "degraded" state (has IP but maybe no default route yet)
RUN mkdir -p /etc/systemd/system/systemd-networkd-wait-online.service.d \
    && cat > /etc/systemd/system/systemd-networkd-wait-online.service.d/fast-boot.conf <<'EOF'
[Service]
ExecStart=
ExecStart=/usr/lib/systemd/systemd-networkd-wait-online --interface=eth0 --timeout=10 --operational-state=degraded
EOF

# Disable systemd-resolved (causes boot delays and DNS issues in Firecracker)
# resolv.conf is set up by stockyard-init.sh at boot time
RUN systemctl disable systemd-resolved 2>/dev/null || true

# NOTE: /etc/hosts is handled by convert-to-rootfs.sh, not here.
# Docker manages /etc/hosts as a bind mount, so COPY would be overwritten
# by an empty file during `docker export`.

# ============================================================================
# Section 7b: Disable unnecessary services for fast boot
# ============================================================================
# These services are unnecessary for ephemeral Firecracker VMs and add boot latency.
# See docs/vm-image-services.md for rationale on each.

# Timers - scheduled tasks not needed for ephemeral VMs
RUN systemctl disable apt-daily.timer apt-daily-upgrade.timer 2>/dev/null || true \
    && systemctl disable dpkg-db-backup.timer 2>/dev/null || true \
    && systemctl disable e2scrub_all.timer 2>/dev/null || true \
    && systemctl disable motd-news.timer 2>/dev/null || true \
    && systemctl disable fstrim.timer 2>/dev/null || true

# Services - not needed for VM operation
RUN systemctl disable e2scrub_reap.service 2>/dev/null || true \
    && systemctl disable unattended-upgrades.service 2>/dev/null || true \
    && systemctl disable networkd-dispatcher.service 2>/dev/null || true \
    && systemctl disable systemd-pstore.service 2>/dev/null || true \
    && systemctl disable systemd-timesyncd.service 2>/dev/null || true

# Getty services - we have vsock shell for console access
RUN systemctl disable getty@tty1.service 2>/dev/null || true \
    && systemctl disable console-getty.service 2>/dev/null || true

# Mask services that might get pulled in by dependencies
RUN systemctl mask systemd-tmpfiles-clean.timer 2>/dev/null || true

# ============================================================================
# Section 8: MicroVM Kernel (built from source)
# ============================================================================
# Custom kernel with CONFIG_TUN=y and CONFIG_NF_TABLES=y for native Tailscale
# networking. The stock Firecracker kernel lacks these, forcing slow userspace mode.
# Kernel is extracted by convert-to-rootfs.sh for host-side use as vmlinux.bin.

ARG KERNEL_VERSION=6.1.155
RUN apt-get update && apt-get install -y \
    libncurses-dev flex bison libssl-dev libelf-dev bc \
    && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-${KERNEL_VERSION}.tar.xz \
    | tar -xJf - -C /tmp
COPY kernel.config /tmp/linux-${KERNEL_VERSION}/.config
RUN cd /tmp/linux-${KERNEL_VERSION} \
    && make -j$(nproc) vmlinux \
    && cp vmlinux /boot/vmlinuz \
    && rm -rf /tmp/linux-${KERNEL_VERSION}

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

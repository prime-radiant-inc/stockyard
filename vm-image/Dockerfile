# Stockyard VM Image
# Ubuntu 24.04 with development tools for Claude Code sessions

FROM ubuntu:24.04

# Prevent interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive

# Install base system packages
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
    # Python 3.11+
    python3 \
    python3-pip \
    python3-venv \
    # Networking tools
    iproute2 \
    iptables \
    dnsutils \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 20
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install Go 1.22
RUN curl -fsSL https://go.dev/dl/go1.22.10.linux-amd64.tar.gz | tar -C /usr/local -xzf - \
    && ln -s /usr/local/go/bin/go /usr/local/bin/go \
    && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

# Install Rust
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y \
    && ln -s /root/.cargo/bin/rustc /usr/local/bin/rustc \
    && ln -s /root/.cargo/bin/cargo /usr/local/bin/cargo \
    && ln -s /root/.cargo/bin/rustup /usr/local/bin/rustup

# Install GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update \
    && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Install Tailscale
RUN curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.noarmor.gpg | tee /usr/share/keyrings/tailscale-archive-keyring.gpg > /dev/null \
    && curl -fsSL https://pkgs.tailscale.com/stable/ubuntu/noble.tailscale-keyring.list | tee /etc/apt/sources.list.d/tailscale.list \
    && apt-get update \
    && apt-get install -y tailscale \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code
RUN npm install -g @anthropic-ai/claude-code

# Create vscode user with sudo privileges
RUN useradd -m -s /bin/bash -G sudo vscode \
    && echo "vscode ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/vscode \
    && chmod 0440 /etc/sudoers.d/vscode

# Set up Rust for vscode user
RUN su - vscode -c "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y"

# Configure SSH server
RUN mkdir -p /var/run/sshd \
    && sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config \
    && sed -i 's/#PasswordAuthentication yes/PasswordAuthentication no/' /etc/ssh/sshd_config \
    && sed -i 's/#PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config

# Configure cloud-init for VM initialization
RUN mkdir -p /var/lib/cloud/seed/nocloud

# Copy stockyard-snapshot binary (built externally)
COPY scripts/stockyard-snapshot/stockyard-snapshot /usr/local/bin/stockyard-snapshot
RUN chmod +x /usr/local/bin/stockyard-snapshot

# Copy Claude Code hooks setup script
COPY scripts/setup-claude-hooks.sh /usr/local/bin/setup-claude-hooks.sh
RUN chmod +x /usr/local/bin/setup-claude-hooks.sh

# Copy init system configuration
COPY init/stockyard-init.sh /usr/local/bin/stockyard-init.sh
RUN chmod +x /usr/local/bin/stockyard-init.sh

COPY init/stockyard-init.service /etc/systemd/system/stockyard-init.service
RUN systemctl enable stockyard-init.service || true

# Set environment variables
ENV GOPATH=/home/vscode/go
ENV PATH="/home/vscode/.cargo/bin:/usr/local/go/bin:${PATH}"

# Default working directory
WORKDIR /home/vscode

# Expose SSH port
EXPOSE 22

# Default command (will be overridden by cloud-init/systemd in VM)
CMD ["/sbin/init"]

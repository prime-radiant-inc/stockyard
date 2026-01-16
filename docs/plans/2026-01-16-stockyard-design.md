# Stockyard Design

Stockyard is a CLI and daemon for running coding agents (Claude Code, Codex, etc.) in isolated Firecracker micro-VMs with full audit trail capability.

## Overview

### Goals

- Run 1-50+ coding agents in parallel on a single host
- Complete isolation via Firecracker micro-VMs (not containers)
- Persistent workspaces that survive VM restarts/crashes
- Instant snapshots for audit trails (between every tool call)
- Direct SSH access via Tailscale
- Secure credential injection via 1Password
- API-first design for future web UI integration

### Non-Goals (V1)

- Multi-host / AWS deployment
- AWS Secrets Manager integration
- Web UI / dashboard
- Task queuing
- Cost tracking / API usage telemetry
- DevContainer spec support

## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │           stockyard daemon              │
CLI ───────────────►│  ┌─────────────────────────────────┐   │
                    │  │  gRPC/REST API (authenticated)   │   │
Web UI (future) ───►│  └─────────────────────────────────┘   │
                    │         │                               │
Remote API ────────►│         ▼                               │
                    │  ┌─────────────┐  ┌──────────────┐     │
                    │  │ Task Manager│  │ State (SQLite)│     │
                    │  └─────────────┘  └──────────────┘     │
                    │         │                               │
                    │    ┌────┴────┬──────────┬─────────┐    │
                    │    ▼         ▼          ▼         ▼    │
                    │ Flintlock   ZFS    Tailscale   1Password│
                    │  (VMs)   (snapshots)  (net)    (secrets)│
                    └─────────────────────────────────────────┘
                              │
                              ▼
                    ┌──────────────────┐
                    │  Firecracker VMs │◄── virtio-fs (workspace)
                    │                  │◄── vsock (snapshots)
                    │                  │◄── Tailscale (SSH)
                    └──────────────────┘
```

### Key Components

| Component | Purpose |
|-----------|---------|
| stockyardd | Daemon managing VMs, storage, API |
| stockyard CLI | Thin client to daemon API |
| Flintlock | Firecracker VM lifecycle (gRPC) |
| ZFS pool | Workspace persistence + instant snapshots |
| virtio-fs | Share workspace from host into VM |
| vsock | Host↔VM communication for snapshot triggers |
| Tailscale | VMs auto-join tailnet for SSH access |
| 1Password CLI | Secrets management (abstracted for future AWS SM) |
| SQLite | Task state, snapshot metadata, logs index |

## CLI Interface

### Primary Commands

```bash
# Start a VM with a coding agent
stockyard run --repo github.com/org/repo --ref branch-name -- claude-code [args...]

# List running VMs
stockyard list

# Attach to a running VM (SSH wrapper)
stockyard attach <task-id>

# Stop a VM (workspace persists)
stockyard stop <task-id>

# Stop and remove workspace
stockyard destroy <task-id>

# Manual snapshot
stockyard snapshot <task-id> [label]

# List snapshots for a task
stockyard snapshots <task-id>

# Restore to a snapshot
stockyard restore <task-id> <snapshot>

# Pull logs/files from VM
stockyard logs <task-id>
stockyard cp <task-id>:/path/to/file ./local/path

# Interactive configuration (TUI)
stockyard configure

# Initialize instance
stockyard init --instance <instance-name>
```

### Run Command Flags

```bash
stockyard run \
  --repo github.com/org/repo      # Required: git repo to clone
  --ref main                       # Branch, tag, or commit (default: main)
  --name my-task                   # Optional: human-readable name
  --cpus 2                         # CPU cores (default: 2)
  --memory 4G                      # RAM (default: 4G)
  --no-tailscale                   # Skip Tailscale join
  --env KEY=value                  # Pass environment variables
  --                               # Delimiter
  claude-code [args...]            # Command to run
```

### Example Usage

```bash
# Run Claude Code on a feature branch
stockyard run --repo github.com/obra/myproject --ref feature-auth \
  -- claude-code --dangerously-skip-permissions -p "implement OAuth login"

# Run without a prompt (agent figures it out)
stockyard run --repo github.com/obra/myproject --ref issue-42 \
  -- claude-code --dangerously-skip-permissions

# Run a different agent
stockyard run --repo github.com/obra/myproject --ref main \
  -- codex "fix the failing tests"
```

## Secrets Management

### Instance-Scoped Secrets

Secrets are namespaced by stockyard instance to isolate environments:

```
1Password Vault Structure:
op://Stockyard/
  ├── flower-garden/           # Instance identifier
  │   ├── anthropic-api-key
  │   ├── openai-api-key
  │   ├── github-token
  │   ├── tailscale-auth-key
  │   └── ssh-private-key
  └── aws-prod/                # Future AWS instance
      └── ...
```

### Configuration

```bash
# Initialize instance
stockyard init --instance flower-garden

# Stored in ~/.config/stockyard/config.json
{
  "instance_id": "flower-garden",
  "secrets": {
    "provider": "1password",
    "vault": "Stockyard",
    "prefix": "flower-garden"
  }
}
```

### Secret Resolution

```go
type SecretProvider interface {
    GetSecret(name string) (string, error)
}

// Implementations:
// - OnePasswordProvider (v1)
// - AWSSecretsManagerProvider (future)
```

Secrets are:
- Fetched on host at VM start time
- Injected via cloud-init as environment variables
- Never written to disk in VM

## Storage & Snapshots

### ZFS Layout

```
tank/stockyard/
  └── workspaces/
      ├── task-abc123/     # Dataset per task, mounted at /workspace in VM
      ├── task-def456/
      └── ...
```

- One ZFS dataset per task
- Mounted into VM via virtio-fs at `/workspace`
- Stockyard is agnostic about internal filesystem layout
- Agent clones repos, creates files - not stockyard's business

### Snapshot Architecture

```
Host                                    VM
┌─────────────────────┐                ┌──────────────────────┐
│  stockyard daemon   │◄───vsock──────►│  stockyard-snapshot  │
│         │           │                │  (tiny CLI client)   │
│         ▼           │                └──────────────────────┘
│  zfs snapshot       │                         │
│  tank/.../task@name │                   Called by Claude Code
└─────────────────────┘                   PostToolUse hook
```

### Snapshot Naming

```
tank/stockyard/workspaces/task-abc123@2026-01-16T14:32:01-edit-src-main.py
tank/stockyard/workspaces/task-abc123@2026-01-16T14:32:45-bash-npm-test
tank/stockyard/workspaces/task-abc123@2026-01-16T14:33:12-manual-checkpoint
```

### Claude Code Auto-Snapshot Hook

Pre-installed in VM image:

```json
{
  "hooks": {
    "PostToolUse": [{
      "command": "stockyard-snapshot \"$CLAUDE_TOOL_NAME\""
    }]
  }
}
```

### Why ZFS?

- Snapshots are nearly instant (milliseconds)
- Copy-on-write: snapshots are almost free (only changed blocks cost space)
- Can have thousands of snapshots per dataset
- Full diff capability between any two snapshots
- Perfect for "snapshot between every tool call" audit trail

### Initial Setup

File-backed pool for prototyping (can migrate to dedicated disk later):

```bash
zpool create tank /var/lib/stockyard/zpool.img
zfs create tank/stockyard
zfs create tank/stockyard/workspaces
zfs set compression=lz4 tank/stockyard
```

## Networking

### Network Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Host (flower-garden)                                    │
│                                                          │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐          │
│  │ VM task-1│    │ VM task-2│    │ VM task-3│          │
│  │ tap0     │    │ tap1     │    │ tap2     │          │
│  └────┬─────┘    └────┬─────┘    └────┬─────┘          │
│       │               │               │                 │
│       └───────────────┴───────────────┘                 │
│                       │                                  │
│                   NAT/bridge                             │
│                       │                                  │
│               host network + internet                    │
└─────────────────────────────────────────────────────────┘
```

### Tailscale Integration

At VM boot (via cloud-init):

```bash
tailscale up --authkey=${TAILSCALE_AUTH_KEY} --hostname=stockyard-${TASK_ID}
```

### Access Paths

```bash
# Direct via Tailscale (from anywhere on tailnet)
ssh stockyard-task-abc123

# Via CLI (convenience wrapper)
stockyard attach task-abc123
```

### Firewall

- VMs can reach internet (outbound)
- VMs reachable via Tailscale (inbound from tailnet only)
- No direct inbound from public internet

## VM Base Image

### Approach

Copy packnplay's Dockerfile and customize for VM boot. Own the image, don't inherit.

```
stockyard/
  └── vm-image/
      ├── Dockerfile          # Based on packnplay's, customized
      ├── kernel/             # Linux kernel for Firecracker
      └── scripts/
          ├── stockyard-snapshot
          └── init-setup.sh
```

### What's Included

From packnplay:
- Base: Ubuntu 24.04
- Languages: Node.js LTS, Python 3.11+, Go, Rust
- Coding agents: Claude Code, Codex, etc.
- Dev tooling: git, gh, build-essential, vim, etc.

Added for stockyard:
- Cloud-init for credential injection
- Tailscale daemon
- stockyard-snapshot vsock client
- Claude Code hooks for auto-snapshots
- VM boot requirements (kernel, init)

Potentially trimmed:
- Cloud CLIs (AWS, Azure, GCloud) if not needed
- Anything that bloats image unnecessarily

## Logging

### Log Sources

```
VM
├── /var/log/cloud-init.log      # Boot/credential injection
├── /var/log/tailscale.log       # Network join
├── Agent stdout/stderr          # Captured by stockyard
└── /workspace/.claude/          # Claude Code's own logs
```

### Log Capture

`stockyard run` captures agent stdout/stderr:
- Streams to terminal
- Saves to `/var/lib/stockyard/logs/task-abc123/agent.log`

### Log Retrieval

```bash
# Stream logs from running VM
stockyard logs task-abc123

# Stream and follow
stockyard logs -f task-abc123

# Get boot/system logs
stockyard logs --system task-abc123

# Copy specific files
stockyard cp task-abc123:/workspace/.claude/logs ./local-dir/
```

## Implementation

### Technology Choices

| Choice | Rationale |
|--------|-----------|
| Go | Best Flintlock SDK, standard for infra tooling |
| Flintlock | Actively maintained, handles VM lifecycle, supports OCI images |
| Firecracker | Real isolation (micro-VMs), fast startup, production-proven |
| ZFS | Instant snapshots, CoW efficiency, perfect for audit trails |
| virtio-fs | Share host filesystem into VM |
| vsock | Direct host↔VM communication without networking |
| 1Password CLI | Lightweight secrets, no infrastructure to run |
| SQLite | Simple state persistence, no external database |
| Cobra | Standard Go CLI framework |

### Project Structure

```
stockyard/
├── cmd/
│   ├── stockyard/          # CLI binary
│   │   ├── main.go
│   │   └── root.go
│   └── stockyardd/         # Daemon binary
│       └── main.go
├── pkg/
│   ├── api/                # gRPC/REST API definitions
│   ├── daemon/             # Daemon core logic
│   ├── flintlock/          # VM lifecycle via Flintlock
│   ├── zfs/                # ZFS dataset/snapshot management
│   ├── secrets/            # 1Password (+ future AWS SM)
│   ├── vsock/              # Host↔VM communication
│   ├── tailscale/          # Tailscale auth key injection
│   ├── config/             # Configuration management
│   └── agents/             # Agent definitions
├── vm-image/
│   ├── Dockerfile
│   └── scripts/
└── docs/
    └── plans/
```

### Interfaces

```go
// pkg/runtime/runtime.go
type VMRuntime interface {
    Create(config VMConfig) (VM, error)
    Start(id string) error
    Stop(id string) error
    Destroy(id string) error
    List() ([]VM, error)
    Exec(id string, cmd []string) error
}

// pkg/secrets/provider.go
type SecretProvider interface {
    GetSecret(name string) (string, error)
}

// pkg/snapshots/snapshots.go
type SnapshotManager interface {
    Create(taskID, label string) error
    List(taskID string) ([]Snapshot, error)
    Restore(taskID, snapshotName string) error
}
```

### Reference: packnplay

Study these packnplay patterns (don't fork, implement fresh):

| Pattern | Location | Use For |
|---------|----------|---------|
| Agent abstraction | `pkg/agents/` | Defining coding agent configs |
| AWS credential_process | `pkg/aws/` | AWS credential handling |
| Config TUI | `pkg/config/` | Interactive configuration |
| CLI structure | `cmd/` | Cobra command organization |

## Open Questions

Defer to implementation:

- Exact Flintlock configuration details
- VM resource defaults (start with 2 CPU / 4GB RAM, tune later)
- Snapshot retention policy defaults
- API authentication mechanism (API keys vs Tailscale identity)

## Future Phases

### Phase 2: Web UI & Remote API
- Authenticated REST/gRPC API (groundwork in v1)
- Web dashboard for task monitoring
- Remote task submission

### Phase 3: Multi-Host & AWS
- AWS Secrets Manager integration
- Firecracker on EC2 bare metal
- Multi-host orchestration
- Task distribution

### Phase 4: Advanced Features
- Task queuing
- Cost tracking / API usage telemetry
- Custom VM images per task
- Multi-agent coordination

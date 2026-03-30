# Stockyard

Coding agent VM orchestrator. Runs coding agents in isolated Firecracker micro-VMs with ZFS-based audit trail snapshots.

## Quick Start

```bash
# Initialize stockyard
stockyard init --instance my-dev

# Start the daemon (in another terminal)
stockyardd

# Create a VM
stockyard run --name my-task --env-file .env

# Attach to the running VM
stockyard attach <task-id>

# List running tasks
stockyard list
```

## Creating VMs

```bash
stockyard run [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--name` | | Human-readable task name |
| `--env-file` | | Path to .env file to include in the VM |
| `--env` | | Environment variables (`KEY=value`, repeatable) |
| `--cpus` | 2 | Number of CPU cores |
| `--memory` | 4G | Memory allocation |
| `--no-tailscale` | false | Skip Tailscale setup |
| `--tailscale-auth-key` | | Tailscale auth key (overrides 1Password lookup) |

SSH public keys from `~/.ssh/*.pub` are automatically injected into the VM.

### Environment Configuration

The `--env-file` flag delivers a `.env` file into the VM via Firecracker's MMDS metadata service at boot. This is the primary way to pass API keys and tokens.

Tailscale auth keys are handled separately via `--tailscale-auth-key` or automatic 1Password lookup.

## Exec and Command Queues (Experimental)

> **Note:** `exec` and command queues are an experiment in programmatic VM orchestration. The API works but it's not clear this is the right abstraction — running commands via SSH into the VM's Tailscale address is simpler and may be the better pattern. This interface may change significantly or be removed.

`stockyard exec` runs commands inside a VM:

```bash
stockyard exec <task-id> -- go mod download
stockyard exec <task-id> -- claude-code -p "implement OAuth"
```

Commands are managed through named queues. Two are created automatically with each VM:

- **`default`** — serial execution. Commands run one at a time.
- **`admin`** — concurrent. For interactive/debug shells.

```bash
stockyard queue list <task-id>
stockyard queue status <task-id> default
stockyard command logs <task-id> <command-id> --follow
```

## Remote Access

The CLI can connect to remote stockyard daemons using the `--url` flag or `STOCKYARD_URL` environment variable.

### URL Formats

| Scheme | Description | Example |
|--------|-------------|---------|
| `unix://` | Local Unix socket | `unix:///var/run/stockyard/stockyard.sock` |
| `grpc://` | Remote gRPC (no TLS) | `grpc://stockyard-server:65433` |
| `grpcs://` | Remote gRPC with TLS | `grpcs://stockyard-server:65433` |
| `host:port` | Defaults to `grpc://` | `stockyard-server:65433` |

### Examples

```bash
# Connect to a remote daemon via flag
stockyard --url grpc://stockyard-server:65433 list

# Or via environment variable
export STOCKYARD_URL=grpc://stockyard-server:65433
stockyard list

# Shell alias for frequent remote access
alias stockyard-prod='stockyard --url grpc://stockyard-prod:65433'
stockyard-prod list
```

### Connection Resolution

The CLI resolves the daemon connection in this order:

1. `--url` flag (highest priority)
2. `STOCKYARD_URL` environment variable
3. System config (`/etc/stockyard/config.json` socket path)
4. Default: `unix:///var/run/stockyard/stockyard.sock`

### Daemon Configuration

To enable remote access, configure the daemon to listen on TCP:

```json
{
  "daemon": {
    "socket_path": "/var/run/stockyard/stockyard.sock",
    "grpc_addr": ":65433"
  }
}
```

When `grpc_addr` is set, the daemon listens on both the Unix socket (for local access) and TCP (for remote access).

**Note:** For secure remote access, use Tailscale or a reverse proxy with TLS. The daemon does not yet support TLS directly.

## VM Services

VMs include built-in services that communicate with the host via vsock:

| Service | Port | Description |
|---------|------|-------------|
| `stockyard-shell` | 52 | Terminal access via vsock (no SSH needed) |
| `stockyard-snapshot` | 51 | ZFS snapshot coordination |
| `llm-proxy` | 12071 | LLM API logging proxy (routes Anthropic/OpenAI traffic) |

### Terminal Access

The dashboard provides browser-based terminal access to VMs using `stockyard-shell`. This eliminates SSH key management and works even when VM networking is misconfigured.

See [docs/specs/vsock-shell-service.md](docs/specs/vsock-shell-service.md) for protocol details.

# Stockyard

Coding agent VM orchestrator. Runs coding agents in isolated Firecracker micro-VMs with ZFS-based audit trail snapshots.

## Quick Start

```bash
# Initialize stockyard
stockyard init --instance my-dev

# Start the daemon (in another terminal)
stockyardd

# Run a coding agent
stockyard run --repo github.com/org/repo -- claude-code -p "your prompt"

# Attach to the running VM
stockyard attach <task-id>

# List running tasks
stockyard list
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

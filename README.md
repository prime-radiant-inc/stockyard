# Stockyard

Coding agent VM orchestrator. Runs coding agents in isolated VMs — Firecracker micro-VMs on Linux (with ZFS-based audit-trail snapshots), and Apple's `container` tool on macOS.

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

The `--env-file` flag delivers a `.env` file into the VM at boot. The Firecracker backend ships it via the MMDS metadata service; the apple-container backend forwards it as `container run --env` flags (explicit task env overrides `.env` entries). Either way, this is the primary way to pass API keys and tokens.

Tailscale auth keys are handled separately via `--tailscale-auth-key` or automatic 1Password lookup.

## Destroying Tasks

Without `--force`, `stockyard destroy` previews the selected task and does not
delete anything:

```bash
stockyard destroy <task-id>
```

An unnamed task requires `--force`:

```bash
stockyard destroy <task-id> --force
```

A named task requires both `--force` and its exact name:

```bash
stockyard destroy <task-id> --force --confirm-name='my-task'
```

The name comparison is byte-for-byte. Existing scripts that destroy named tasks
with `--force` alone now fail closed and must supply an independently known
expected name. Copying both the ID and name from the same selected row defeats
the additional selection check.

This confirmation applies to the `stockyard destroy` command only.
`stockyard gc`, the dashboard, and direct API clients destroy tasks without it.

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
  "backend": "firecracker",
  "daemon": {
    "socket_path": "/var/run/stockyard/stockyard.sock",
    "grpc_addr": ":65433"
  }
}
```

When `grpc_addr` is set, the daemon listens on both the Unix socket (for local access) and TCP (for remote access).

The top-level `backend` key selects the VM backend. Valid values are `"firecracker"` (default, Linux) and `"apple-container"` (macOS). The apple-container backend skips the Firecracker-only setup steps — no ZFS, no kernel/rootfs install — and uses Apple's `container` CLI to manage VMs. Its task image is set via `apple_container.image` (e.g. `"stockyard.local/stockyard-vm:container"`, built by `make container-image`).

**Note:** For secure remote access, use Tailscale or a reverse proxy with TLS. The daemon does not yet support TLS directly.

#### Console log archive

When a VM is destroyed, its console logs (`stdout.log`, `stderr.log`) are the
only record of a VM that failed to boot. When enabled, the daemon preserves
them in a bounded host-local archive before removing the VM state directory.
Each entry is `<archive dir>/<utc-timestamp>-<vm-id>-<random-suffix>/`
containing the console files and a `meta.json`; in-progress entries use a
`.staging-` prefix so a partial archive is distinguishable. Archiving is
best-effort and never blocks a destroy; outcomes are logged with
`console_archive_*` tokens.

Archiving is **opt-in** — it is off unless `enabled` is set:

```json
{
  "console_archive": {
    "enabled": true,
    "dir": "/var/lib/stockyard/console-archive",
    "max_total_bytes": 1073741824,
    "max_age_days": 14,
    "max_entry_bytes": 67108864
  }
}
```

Only `enabled` is required to turn archiving on; the other values above are
the defaults. Files larger than `max_entry_bytes` keep their head and tail
with a truncation marker between them. Retention prunes expired entries and
then the oldest entries until the archive fits `max_total_bytes`.

## VM Services

VMs ship with `llm-proxy` (port 12071) — an outbound HTTP proxy that logs Anthropic/OpenAI API traffic. It runs in-guest on both backends.

Terminal access and snapshot coordination work differently per backend:

| Capability | Firecracker (Linux) | apple-container (macOS) |
|------------|---------------------|--------------------------|
| Terminal | In-guest `stockyard-shell` listens on vsock port 52; dashboard dials in | Host runs `container exec` under a PTY |
| Audit snapshots | In-guest `stockyard-snapshot` dials host on vsock port 51; daemon does `zfs snapshot` | Not applicable (no ZFS) |

Both vsock services exist because Firecracker VMs are otherwise isolated from the host. On apple-container, native container tooling covers the same needs — so neither guest binary is built into or needed in the apple-container image.

See [docs/specs/vsock-shell-service.md](docs/specs/vsock-shell-service.md) for the Firecracker vsock-shell protocol.

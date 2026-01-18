# CLI Remote Connections

## Overview

The `stockyard` CLI should support connecting to both local and remote daemons. This spec defines the connection model, URL format, and configuration approach.

## URL Format

```
STOCKYARD_URL=<scheme>://<host>:<port>
```

### Schemes

| Scheme | Description | Example |
|--------|-------------|---------|
| `unix` | Local Unix socket | `unix:///var/run/stockyard/stockyard.sock` |
| `grpc` | Remote gRPC over TCP | `grpc://stockyard.example.com:65432` |
| `grpcs` | Remote gRPC over TLS | `grpcs://stockyard.example.com:65432` |

If no scheme is provided, assume `grpc://` for `host:port` format.

### Examples

```bash
# Local socket (default)
STOCKYARD_URL=unix:///var/run/stockyard/stockyard.sock

# Remote, no TLS
STOCKYARD_URL=grpc://192.168.1.100:65432
STOCKYARD_URL=stockyard-server:65432  # scheme defaults to grpc://

# Remote with TLS
STOCKYARD_URL=grpcs://stockyard.example.com:65432
```

## Connection Resolution

The CLI resolves the daemon connection in this order:

1. `--url` flag (highest priority)
2. `STOCKYARD_URL` environment variable
3. System config `/etc/stockyard/config.json` → construct `unix://<socket_path>`
4. Default: `unix:///var/run/stockyard/stockyard.sock`

```
┌─────────────────────────────────────────────────────────┐
│                     CLI invocation                       │
└─────────────────────────────────────────────────────────┘
                           │
                           ▼
                   ┌───────────────┐
                   │ --url flag?   │──yes──▶ Use flag value
                   └───────────────┘
                           │ no
                           ▼
                   ┌───────────────┐
                   │ STOCKYARD_URL │──yes──▶ Use env var
                   │    env var?   │
                   └───────────────┘
                           │ no
                           ▼
                   ┌───────────────┐
                   │ System config │──yes──▶ Use unix://<socket_path>
                   │    exists?    │
                   └───────────────┘
                           │ no
                           ▼
                   Use default: unix:///var/run/stockyard/stockyard.sock
```

## Configuration Files

### System Config (`/etc/stockyard/config.json`)

Used by the daemon. CLI only reads `daemon.socket_path` to construct local Unix URL.

```json
{
  "daemon": {
    "socket_path": "/var/run/stockyard/stockyard.sock"
  }
  // ... other daemon settings (ignored by CLI)
}
```

No user config file. Remote connections use `--url` flag or `STOCKYARD_URL` env var.

## CLI Changes

### Global Flag

Add `--url` flag to root command:

```bash
stockyard --url grpc://remote:65432 list
stockyard --url unix:///tmp/test.sock run --repo github.com/foo/bar
```

### Help Text

```
Global Flags:
  --url string   Daemon URL (env: STOCKYARD_URL)
                 Formats: unix:///path/to/socket, grpc://host:port, grpcs://host:port
```

## Client Package Changes

### Current

```go
func New(socketPath string) (*Client, error)
```

### Proposed

```go
func NewFromURL(url string) (*Client, error)

// URL parsing:
// - unix:///path  → Unix socket connection
// - grpc://host:port → TCP connection, no TLS
// - grpcs://host:port → TCP connection with TLS
// - host:port → TCP connection, no TLS (grpc:// assumed)
```

## Daemon Changes

The daemon currently listens on a Unix socket only. For remote CLI access, it needs to also listen on TCP.

### Option A: Separate gRPC TCP listener

Add config option:

```json
{
  "daemon": {
    "socket_path": "/var/run/stockyard/stockyard.sock",
    "grpc_addr": ":65432",      // TCP listener (optional)
    "grpc_tls_cert": "",        // TLS cert path (optional)
    "grpc_tls_key": ""          // TLS key path (optional)
  }
}
```

### Option B: Use existing HTTP server

The daemon already has an HTTP server on `:65432`. We could:
- Add gRPC-Web support to the existing HTTP server
- Or use gRPC's HTTP/2 support on the same port

Option A is simpler and keeps gRPC separate from the dashboard.

## Security Considerations

### Local (Unix socket)
- Filesystem permissions control access
- No authentication needed beyond Unix permissions

### Remote (TCP)
- **Without TLS**: Traffic is unencrypted. Only use on trusted networks.
- **With TLS**: Encrypts traffic. Recommended for production.
- **Authentication**: Not in initial scope. Options for later:
  - mTLS (client certs)
  - Token-based auth (API keys)
  - Tailscale (network-level auth)

### Recommendation

For now, rely on network-level security:
- Run stockyard daemon on Tailscale network
- Use `grpc://` (no TLS) since Tailscale encrypts traffic
- Remote URL would be `grpc://stockyard-server:65432` (Tailscale hostname)

## Migration

1. Delete user config `~/.config/stockyard/config.json` (no longer used)
2. System config remains the source of truth for daemon settings
3. CLI uses `--url` or `STOCKYARD_URL` for remote connections

## Implementation Phases

### Phase 1: URL-based connection
- Add `--url` flag and `STOCKYARD_URL` support
- Update client package to parse URLs
- CLI works with local socket via URL format

### Phase 2: Remote TCP support
- Daemon listens on TCP (configurable)
- CLI can connect to remote daemons
- No TLS initially

### Phase 3: TLS support (optional)
- Add `grpcs://` support
- Daemon TLS configuration

## Examples

```bash
# Local (uses system config socket path)
stockyard list

# Explicit local socket
stockyard --url unix:///var/run/stockyard/stockyard.sock list

# Remote server via env var
export STOCKYARD_URL=grpc://stockyard-prod:65432
stockyard list
stockyard run --repo github.com/foo/bar

# Remote server via flag (overrides env)
STOCKYARD_URL=grpc://prod:65432 stockyard --url grpc://dev:65432 list

# Shell alias for frequent remote access
alias stockyard-prod='stockyard --url grpc://stockyard-prod:65432'
stockyard-prod list
```

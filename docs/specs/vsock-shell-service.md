# vsock Shell Service Specification

## Overview

Provide terminal access to VMs via vsock instead of SSH. This eliminates the need for SSH key management and works even when VM networking is misconfigured.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Host                                                         │
│  ┌──────────────┐      ┌──────────────┐                     │
│  │   Browser    │◄────►│  Dashboard   │                     │
│  │  (WebSocket) │      │   Server     │                     │
│  └──────────────┘      └──────┬───────┘                     │
│                               │                              │
│                               │ vsock connect(CID, port 52)  │
│                               ▼                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                     Firecracker                         │ │
│  │  ┌──────────────────────────────────────────────────┐  │ │
│  │  │ VM (CID: assigned at boot)                       │  │ │
│  │  │                                                  │  │ │
│  │  │   ┌─────────────────┐      ┌─────────────────┐  │  │ │
│  │  │   │ stockyard-shell │◄────►│   login shell   │  │  │ │
│  │  │   │ (vsock :52)     │      │   (bash/zsh)    │  │  │ │
│  │  │   └─────────────────┘      └─────────────────┘  │  │ │
│  │  └──────────────────────────────────────────────────┘  │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## vsock Port

**Port 52** - defined as constant `ShellPort` in `pkg/shell/protocol.go`

Other stockyard vsock ports:
- Port 51: stockyard-snapshot service

## Protocol

Simple length-prefixed binary messages over vsock.

### Message Format

```
┌────────────┬────────────┬─────────────────┐
│ Type (1B)  │ Length (4B)│ Payload (var)   │
└────────────┴────────────┴─────────────────┘
```

- **Type**: uint8 message type
- **Length**: uint32 big-endian payload length
- **Payload**: type-specific data

### Message Types

| Type | Name    | Direction    | Payload                                              |
|------|---------|--------------|------------------------------------------------------|
| 0x01 | Open    | Host → VM    | JSON: `{"user": "mooby", "term": "xterm-256color", "cols": 80, "rows": 24}` |
| 0x02 | Data    | Bidirectional| Raw terminal data                                    |
| 0x03 | Resize  | Host → VM    | JSON: `{"cols": 80, "rows": 24}`                     |
| 0x04 | Exit    | VM → Host    | JSON: `{"code": 0}`                                  |
| 0x05 | Error   | VM → Host    | JSON: `{"error": "message"}`                         |

### Session Flow

```
Host                                    VM (stockyard-shell)
  │                                            │
  │─────── connect to vsock port 52 ──────────►│
  │                                            │
  │─────── Open {user, term, cols, rows} ─────►│
  │                                            │ validate user
  │                                            │ spawn PTY with TERM set
  │                                            │ set initial window size
  │                                            │
  │◄──────────── Data (shell output) ──────────│
  │───────────── Data (user input) ───────────►│
  │              ... bidirectional I/O ...     │
  │                                            │
  │─────── Resize {cols, rows} ───────────────►│ (on terminal resize)
  │                                            │
  │◄──────────── Exit {code} ──────────────────│ (shell exits)
  │                                            │
  │◄──────── connection closes ────────────────│
```

**Key points:**
- Open message includes initial terminal size and TERM value
- No separate Resize needed after Open (size is in Open)
- Connection close is the termination signal in both directions
- If host disconnects, VM cleans up the shell session
- If VM sends Exit, it then closes the connection

### Timeouts

- **Open message timeout**: 5 seconds after connection, if no valid Open received, close connection
- **No idle timeout**: Shell sessions can be idle indefinitely (matches SSH behavior)

### User Selection

The **Open** message specifies which user to spawn the shell as:
- `"root"` - root shell
- `"mooby"` (or configured VM_USER) - normal user shell

The VM service validates the user exists before spawning.

## VM-Side Service: stockyard-shell

### Requirements

- **Must run as root**: The `login -f` command requires root privileges to spawn shells as other users
- **vsock kernel support**: Firecracker VMs have vsock enabled by default

### Implementation

A Go binary that:
1. Listens on vsock port 52 (from shared constant)
2. Accepts connections with 5-second timeout for Open message
3. Parses Open message, validates user exists
4. Sets TERM environment variable from Open message
5. Creates PTY with initial size from Open message
6. Spawns login shell via `login -f <user>`
7. Copies data between vsock and PTY
8. Handles Resize by calling `pty.Setsize()`
9. Sends Exit when shell terminates, then closes connection
10. Handles SIGTERM for graceful shutdown

### systemd Service

```ini
[Unit]
Description=Stockyard Shell Service (vsock terminal access)
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/stockyard-shell
Restart=always
RestartSec=1
StandardOutput=journal
StandardError=journal

# Graceful shutdown
TimeoutStopSec=5
KillMode=mixed
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

### Signal Handling

- **SIGTERM**: Stop accepting new connections, wait for existing sessions to finish (up to 5s), then exit
- **SIGINT**: Same as SIGTERM (for interactive testing)

### Error Handling

- **Open timeout**: If no valid Open message within 5 seconds, close connection silently
- **Invalid user**: Send Error message with "user not found", close connection
- **PTY creation fails**: Send Error message, close connection
- **Shell exits**: Send Exit with exit code, close connection
- **Host disconnects**: Kill shell process, clean up
- **vsock not available**: Service fails to start (systemd will log the error)

## Host-Side Changes

### Dashboard Terminal Handler

Replace SSH connection logic with vsock:

```go
func (h *TerminalHandler) connectVsock(cid uint32, user string, cols, rows int) (*VsockSession, error) {
    conn, err := vsock.Dial(cid, shell.ShellPort, nil)
    if err != nil {
        return nil, err
    }

    // Send Open message with terminal info
    openMsg := shell.OpenMessage{
        User: user,
        Term: "xterm-256color",
        Cols: cols,
        Rows: rows,
    }
    payload, _ := openMsg.Marshal()
    if err := shell.WriteMessage(conn, shell.MsgOpen, payload); err != nil {
        conn.Close()
        return nil, err
    }

    return &VsockSession{conn: conn}, nil
}
```

### Getting VM CID

The CID is stored when the VM is created. The dashboard needs access to it:

```go
GetVMCID(ctx context.Context, taskID string) (uint32, error)
```

## Security Considerations

1. **vsock is host-local only** - cannot be accessed from the network
2. **No authentication needed** - only the host can connect to VM vsock
3. **User selection is trusted** - the host chooses which user to spawn as
4. **No encryption** - vsock traffic never leaves the host memory
5. **Root required** - stockyard-shell runs as root to use `login -f`

## Build Integration

### Build Order

1. `make build-shell` - builds `vm-image/scripts/stockyard-shell/stockyard-shell`
2. `make -C vm-image` - Docker build copies the binary into the image

The vm-image Makefile must depend on the shell binary being built first.

### Shared Constants

Port number and message types are defined in `pkg/shell/protocol.go` and used by both:
- `cmd/stockyard-shell/` (VM side)
- `pkg/dashboard/` (host side)

## Testing Strategy

1. **Unit tests**: Protocol encoding/decoding, message types
2. **Integration tests**: Protocol flow over net.Pipe()
3. **Session tests**: Skip on non-root (login -f requires root)
4. **End-to-end test**: Manual test with rebuilt VM image

## Implementation Plan

1. **VM side**: Create `pkg/shell/` protocol package
2. **VM side**: Create `cmd/stockyard-shell/` binary
3. **VM side**: Add systemd service to vm-image
4. **VM side**: Update vm-image build to include stockyard-shell
5. **Host side**: Update TerminalHandler to use vsock (separate plan)
6. **Test**: Rebuild VM image and verify terminal works

## Future Considerations

- **Session recording**: Could log all terminal I/O for audit
- **Idle timeout**: Disconnect shells that are idle too long
- **Rate limiting**: Prevent too many concurrent shells to one VM

## Implementation Status

- **VM side**: Complete (`cmd/stockyard-shell`, `pkg/shell`)
- **Host side**: Complete (`pkg/dashboard` terminal handler)

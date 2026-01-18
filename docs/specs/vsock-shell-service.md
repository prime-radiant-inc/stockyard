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

**Port 52** - chosen to be memorable (52 = "shell" if you squint) and not conflict with:
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

| Type | Name    | Direction    | Payload                          |
|------|---------|--------------|----------------------------------|
| 0x01 | Open    | Host → VM    | JSON: `{"user": "mooby"}`        |
| 0x02 | Data    | Bidirectional| Raw terminal data                |
| 0x03 | Resize  | Host → VM    | JSON: `{"cols": 80, "rows": 24}` |
| 0x04 | Exit    | VM → Host    | JSON: `{"code": 0}`              |
| 0x05 | Error   | VM → Host    | JSON: `{"error": "message"}`     |

### Session Flow

1. Host connects to VM's vsock port 52
2. Host sends **Open** message with desired user
3. VM spawns PTY with login shell for that user
4. Bidirectional **Data** messages flow (terminal I/O)
5. Host sends **Resize** on terminal size changes
6. When shell exits, VM sends **Exit** with exit code
7. Connection closes

### User Selection

The **Open** message specifies which user to spawn the shell as:
- `"root"` - root shell
- `"mooby"` (or configured VM_USER) - normal user shell

The VM service validates the user exists before spawning.

## VM-Side Service: stockyard-shell

### Implementation

A small Go binary (similar to stockyard-snapshot) that:
1. Listens on vsock port 52
2. Accepts connections
3. Parses Open message, validates user
4. Creates PTY, spawns login shell (`login -f <user>` or `su - <user>`)
5. Copies data between vsock and PTY
6. Handles resize by calling `pty.Setsize()`
7. Sends Exit when shell terminates

### systemd Service

```ini
[Unit]
Description=Stockyard Shell Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/stockyard-shell
Restart=always
RestartSec=1

[Install]
WantedBy=multi-user.target
```

### Error Handling

- If user doesn't exist: send Error message, close connection
- If PTY creation fails: send Error message, close connection
- If shell crashes: send Exit with code 1
- Multiple connections: each gets its own shell session (no limit for now)

## Host-Side Changes

### Dashboard Terminal Handler

Replace SSH connection logic with vsock:

```go
func (h *TerminalHandler) connectVsock(cid uint32, user string) (*VsockSession, error) {
    conn, err := vsock.Dial(cid, 52, nil)
    if err != nil {
        return nil, err
    }

    // Send Open message
    openMsg := OpenMessage{User: user}
    if err := writeMessage(conn, MsgOpen, openMsg); err != nil {
        conn.Close()
        return nil, err
    }

    return &VsockSession{conn: conn}, nil
}
```

### Task Struct

Already has CID field - no changes needed:

```go
type Task struct {
    // ...
    CID uint32 // Firecracker vsock Context ID
    // ...
}
```

### DaemonAPI Changes

Add method to get VM's CID:

```go
GetVMCID(ctx context.Context, taskID string) (uint32, error)
```

Or just include CID in the Task struct returned by GetTask (may already be there via VMID lookup).

## Security Considerations

1. **vsock is host-local only** - cannot be accessed from the network
2. **No authentication needed** - only the host can connect to VM vsock
3. **User selection is trusted** - the host chooses which user to spawn as
4. **No encryption** - vsock traffic never leaves the host memory

## Implementation Plan

1. **VM side**: Create `stockyard-shell` binary
2. **VM side**: Add systemd service to vm-image
3. **Host side**: Update TerminalHandler to use vsock instead of SSH
4. **Host side**: Add vsock session management
5. **Test**: Verify terminal works through dashboard

## Future Considerations

- **Session recording**: Could log all terminal I/O for audit
- **Idle timeout**: Disconnect shells that are idle too long
- **Rate limiting**: Prevent too many concurrent shells to one VM

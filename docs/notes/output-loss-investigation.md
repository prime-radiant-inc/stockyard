# Output Loss Investigation — Goddard, 2026-03-13

## Problem

Fast commands (like `echo hello world`) intermittently lose their output. The command completes with exit code 0, but the output file is empty. This affects roughly 1 in 3 fast commands.

## Architecture

Two processes involved:

1. **stockyard-shell** (runs inside VM, `cmd/stockyard-shell/main.go`): Receives commands over vsock, runs them in a PTY, bridges PTY↔vsock I/O.
2. **stockyardd** (runs on host, `pkg/daemon/queue_manager_exec.go`): Connects to shell via vsock, sends MsgOpen, reads MsgData/MsgExit, writes output to disk.

### Shell side flow (stockyard-shell)

```
handleConnection():
  1. Read MsgOpen from vsock
  2. Create PTY session (NewSession)
  3. Spawn two goroutines:
     - PTY→vsock: reads PTY, sends MsgData  (the "pty goroutine")
     - vsock→PTY: reads vsock, writes to PTY stdin
  4. session.Wait()  — blocks until process exits
  5. ptyDone.Wait()  — waits for PTY→vsock goroutine to drain
  6. Send MsgExit with exit code
  7. connCancel() + ioWg.Wait()
```

### Host side flow (queue_manager_exec.go)

```
runVsockCommand():
  1. Create output file
  2. Connect to vsock, CONNECT handshake
  3. Send MsgOpen
  4. Loop reading messages:
     - MsgData → write to output file
     - MsgExit → return exit code IMMEDIATELY   ← potential issue
     - MsgError → return error
```

## What we tried

### Fix 1: Wait for PTY drain before MsgExit (shell side)

**Commit:** `cfac018` (PRI-705: Fix output loss for fast commands in stockyard-shell)

Before: `session.Wait()` → send MsgExit → `connCancel()` → `ioWg.Wait()`
After:  `session.Wait()` → `ptyDone.Wait()` → send MsgExit → `connCancel()`

Added a separate `ptyDone` WaitGroup tracking only the PTY→vsock goroutine. We wait for it to drain before sending MsgExit. Can't use `ioWg.Wait()` because the vsock→PTY goroutine blocks on `ReadMessage(conn)` indefinitely.

**Result:** Improved from ~1/3 to ~2/3 commands having output, but didn't fully fix it.

### Fix 2 (not yet attempted): Drain remaining MsgData after MsgExit (host side)

The host's `runVsockCommand` returns immediately when it sees MsgExit (line 92-95). If any MsgData is still in the network buffer or arrives slightly after MsgExit, it's lost. The connection is closed by `defer conn.Close()`.

Proposed fix: after receiving MsgExit, continue reading with a short deadline. Any remaining MsgData gets written to the output file. Stop on EOF, error, or timeout.

```go
case shell.MsgExit:
    var exitMsg shell.ExitMessage
    exitMsg.Unmarshal(payload)
    // Drain any remaining MsgData before returning
    conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
    for {
        msgType, payload, err := shell.ReadMessage(reader)
        if err != nil {
            break
        }
        if msgType == shell.MsgData {
            outFile.Write(payload)
        }
    }
    return exitMsg.Code, nil
```

## Hypotheses for remaining failures after Fix 1

1. **PTY EIO race**: On Linux, reading from a PTY master after the slave process exits can return EIO. If this happens before all buffered data is read, output is lost. The goroutine treats EIO as an error and exits.

2. **Goroutine scheduling**: `session.Wait()` returns, then we call `ptyDone.Wait()`. If the PTY→vsock goroutine hasn't been scheduled to run its Read() call yet, and the kernel has already marked the PTY as closed, the Read might return EOF/EIO before draining the buffer. (Unlikely but possible under heavy load.)

3. **Network buffering**: Even if the shell correctly sends MsgData before MsgExit, the host might receive them in a different order or the MsgData might still be in a kernel buffer when MsgExit arrives and the host closes the connection.

4. **The `defer connCancel()` in the PTY goroutine**: Defers run LIFO, so `connCancel()` runs before `ptyDone.Done()`. This means the context is cancelled before we signal "done" — but this shouldn't affect already-completed writes.

## Recommended approach

The most robust fix is **belt and suspenders**: fix both sides.

- **Shell side** (already done): Wait for PTY drain before sending MsgExit
- **Host side** (Fix 2 above): After MsgExit, drain remaining data from connection

The host-side fix is the more important one because it handles any case where MsgData arrives after MsgExit, regardless of the cause. It's also the simpler fix.

Additionally, investigating the PTY EIO behavior on the actual VM kernel would help. Check if `session.PTY().Read()` is returning EIO and whether data was lost. Adding temporary logging to the PTY→vsock goroutine in stockyard-shell would confirm this:

```go
n, err := session.PTY().Read(buf)
if err != nil {
    log.Printf("PTY read: n=%d err=%v", n, err)  // temporary debug logging
    ...
}
```

## Files involved

- `cmd/stockyard-shell/main.go` — shell side (VM binary, requires image rebuild to deploy)
- `pkg/daemon/queue_manager_exec.go` — host side (daemon binary, easy to deploy)
- `pkg/shell/protocol.go` — message framing (ReadMessage/WriteMessage)
- `pkg/shell/session.go` — PTY session management

## Test instance

- Host: `stockyard-exp-1` (Tailscale)
- Current task: `5f4e43ce`
- Branch: `feat/command-queues`
- Deploying shell changes requires image rebuild: `make build-shell && cd vm-image && make rootfs && sudo cp output/rootfs.ext4 /var/lib/stockyard/rootfs.ext4` then destroy+recreate task

# Remove Tailscale Pre-Registration

**Date:** 2026-04-07
**Author:** Winry (Bob 1a4b0301 / Opus 4.6)
**Status:** Draft

## Problem

Tailscale pre-registration adds ~1.6s of host-side latency to every VM creation, then the VM spends another ~1.6s reconnecting with the pre-registered state. The total Tailscale overhead is ~3.2s — the control plane round-trip paid twice.

Pre-registration was designed to make VM-side Tailscale reconnection faster (~0.2-0.3s predicted). In practice, reconnection takes ~1.3-1.6s regardless — nearly identical to fresh registration (~1.5-1.7s). The measured benefit is reliability (node is pre-registered), not speed.

### Benchmark Evidence

Full E2E lifecycle (create → SSH echo → destroy), measured on stockyard-ip-10-50-1-107 with minimal kernel:

| Configuration | Create-to-SSH-Echo | Total Lifecycle |
|---|---|---|
| With pre-reg, Tailscale enabled | 4580ms | 4720ms |
| **No pre-reg, Tailscale enabled** | **1505ms** | **1648ms** |
| No Tailscale (direct IP) | 1412ms | 1544ms |

Removing pre-registration cuts wall clock time by ~66% when Tailscale is enabled.

## Design

### What Changes

**`pkg/daemon/tasks.go`** — Remove the pre-registration goroutine, `sync.WaitGroup`, and state injection. After `CreateVM`, if Tailscale is configured, poll until the VM's hostname appears as a peer on the host's Tailscale. Block the `CreateTask` gRPC call until the peer is reachable or timeout expires.

**`pkg/tailscale/wait.go`** (new) — Add a `WaitForPeer(ctx context.Context, hostname string, timeout time.Duration) error` function. Polls `tailscale status --json` on the host, parses the peer list, returns `nil` when the hostname appears with a reachable status. Polls every 250ms. Returns an error if the context expires or the timeout is reached.

**`pkg/tailscale/preregister.go`** — Delete. Dead code once pre-reg is removed.

**`pkg/tailscale/preregister_test.go`** — Delete.

### What Stays the Same

- **VM init scripts** (`stockyard-init.sh`, `stockyard-init-alpine.sh`) — Unchanged. They already handle both paths: pre-registered state and direct auth key. With pre-reg removed, they always take the auth key path.
- **MMDS metadata** — Still passes `tailscale-auth-key` to the VM. The `tailscale-state` field is simply never populated.
- **`stockyard run` CLI** — Unchanged. Same contract: returns when VM is SSH-ready.
- **`stockyard destroy`** — Unchanged. Already calls `tailscale.RemoveDevice` to clean up the Tailscale node.
- **`VMConfig` struct** — `TailscaleState` field can be left in place (always nil) or removed. Removing is cleaner.

### Flow

```
Before (with pre-reg):
  0.0s  daemon starts host-side tailscaled for pre-reg
  1.6s  pre-reg done, state extracted, tailscaled killed
  1.7s  VM created with pre-registered state in MMDS
  2.5s  kernel + init done, tailscaled loads state
  4.1s  VM tailscale reconnects with control plane
  4.9s  stockyard run returns
        --- user can SSH ---

After (no pre-reg):
  0.0s  VM created with auth key in MMDS
  0.8s  kernel + init done, tailscale up --authkey starts
  2.4s  VM tailscale registered, peer appears on host
  2.5s  daemon detects peer, stockyard run returns
        --- user can SSH ---
```

### WaitForPeer Implementation

```go
// WaitForPeer blocks until the given hostname appears as a Tailscale peer.
func WaitForPeer(ctx context.Context, hostname string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    ticker := time.NewTicker(250 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            if time.Now().After(deadline) {
                return fmt.Errorf("timeout waiting for Tailscale peer %s after %v", hostname, timeout)
            }
            if peerReachable(hostname) {
                return nil
            }
        }
    }
}

func peerReachable(hostname string) bool {
    cmd := exec.Command("tailscale", "status", "--json")
    output, err := cmd.Output()
    if err != nil {
        return false
    }
    // Parse JSON, check if hostname appears in Peer map
    // with Online == true
    var status struct {
        Peer map[string]struct {
            HostName string
            Online   bool
        }
    }
    if json.Unmarshal(output, &status) != nil {
        return false
    }
    for _, peer := range status.Peer {
        if peer.HostName == hostname && peer.Online {
            return true
        }
    }
    return false
}
```

### Integration in tasks.go

After `CreateVM` returns successfully:

```go
if tailscaleHostname != "" {
    log.Printf("Waiting for Tailscale peer %s...", tailscaleHostname)
    waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
    defer waitCancel()
    if err := tailscale.WaitForPeer(waitCtx, tailscaleHostname, 60*time.Second); err != nil {
        log.Printf("Warning: Tailscale peer not ready: %v (VM may still be connecting)", err)
        // Don't fail the task — Tailscale might come up shortly after
    }
}
```

### Error Handling

- If `WaitForPeer` times out, log a warning but don't fail the task. The VM is running and accessible via direct IP. Tailscale may still connect after the timeout.
- If the `tailscale` CLI is not available on the host, `WaitForPeer` returns an error on first poll. Log and continue — same behavior as today when pre-reg fails.

### Cleanup

Files to delete:
- `pkg/tailscale/preregister.go`
- `pkg/tailscale/preregister_test.go`

Fields to remove from `pkg/firecracker/types.go`:
- `VMConfig.TailscaleState`

Fields to remove from `pkg/firecracker/cloudinit.go`:
- MMDS `tailscale-state` field (stop populating it; VM init handles its absence gracefully)

### Testing

1. Build modified daemon, deploy to test host
2. Run E2E benchmark: `stockyard run` → SSH echo via Tailscale hostname → `stockyard destroy`
3. Verify Tailscale hostname resolves and SSH works immediately after `stockyard run` returns
4. Verify `--no-tailscale` still works (should be unaffected)
5. Verify destroy still cleans up Tailscale node
6. Verify graceful degradation when Tailscale times out

### Risks

**Low risk:** The VM init script already handles the auth-key-only path — it's the fallback when pre-reg fails today. We're just making it the only path.

**MagicDNS propagation:** There may be a brief window between "peer appears in `tailscale status`" and "MagicDNS hostname resolves." If this causes SSH failures, we can add a DNS resolution check or a brief SSH probe after peer detection.

**Ephemeral key behavior:** The auth key must be an ephemeral key so nodes auto-deregister when the VM is destroyed. This is already the case (documented in the tailscale-ephemeral-consolidation spec).

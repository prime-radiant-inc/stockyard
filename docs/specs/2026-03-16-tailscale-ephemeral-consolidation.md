# Tailscale Ephemeral Node Consolidation

**Issue:** PRI-710 — Stockyard VMs not registering as ephemeral Tailscale nodes
**Date:** 2026-03-16

## Problem

Stockyard VM instances persist in the Tailscale admin panel after destruction. They should be ephemeral nodes that auto-remove when disconnected.

Root cause: the host-side pre-registration path (`preregister.go`) runs `tailscale up` without `--ephemeral`. Since this is the primary registration path, nodes are created as persistent. Additionally, there are redundant `tailscale up` invocations across four code paths — a result of evolutionary layering where newer approaches were added without removing older ones.

## Current State

Five places generate `tailscale up` commands:

| Path | Location | `--ephemeral`? | Status |
|------|----------|----------------|--------|
| Pre-registration (host) | `pkg/tailscale/preregister.go:84` | No | **Active — primary path** |
| Cloud-init runcmd (firecracker) | `pkg/firecracker/cloudinit.go:106` | No | Active — redundant with init script |
| Cloud-init runcmd (flintlock) | `pkg/flintlock/cloudinit.go:105` | No | Active — redundant with init script |
| Init script fallback (VM) | `vm-image/init/stockyard-init.sh:210` | Yes | Active — fallback path |
| Cloud-init template | `vm-image/cloud-init/user-data.template:87` | No | Dead code |
| Helper method | `pkg/tailscale/tailscale.go:49` | No | Dead code |

The VM boot sequence (in `pkg/daemon/tasks.go`) runs pre-registration AND generates cloud-init with a `tailscale up` runcmd. Inside the VM, the init script also handles Tailscale. This creates up to three competing `tailscale up` invocations for a single VM.

## Design

### Single authority: the init script

The init script (`stockyard-init.sh`) becomes the sole owner of Tailscale setup inside the VM. It already handles both cases correctly:

- **Pre-registered state exists:** Start `tailscaled` with the state file, wait for reconnection. No `tailscale up` needed.
- **No state (fallback):** Run `tailscale up --authkey=... --ephemeral` to register fresh.

### Changes

1. **`pkg/tailscale/preregister.go`** — Add `--ephemeral` to `tailscale up` args (line 84-93). This is the bug fix.

2. **`pkg/firecracker/cloudinit.go`** — Remove the Tailscale `tailscale up` command from cloud-init runcmd generation. The init script handles this.

3. **`pkg/flintlock/cloudinit.go`** — Same as above.

4. **`pkg/daemon/tasks.go`** — Stop passing `TailscaleAuthKey` to `CloudInitConfig` (it's only used to generate the now-removed runcmd). The auth key still reaches the VM via MMDS metadata for the init script's fallback path.

5. **Delete dead code:**
   - `vm-image/cloud-init/user-data.template`
   - `GenerateCloudInitScript` method from `pkg/tailscale/tailscale.go`

6. **`pkg/tailscale/tailscale.go:RemoveDevice`** — No change. The function documents that it relies on ephemeral key expiration, which will now actually work.

### What stays the same

- The MMDS metadata pipeline (auth key and pre-registered state passed to VM)
- The init script's two-branch logic
- `RemoveDevice` behavior (relies on ephemeral expiry)
- `CloudInitConfig` struct still has `TailscaleAuthKey`/`TailscaleHostname` fields (removing them is optional cleanup; they become unused by the firecracker provider but may still be referenced by flintlock)

### Testing

- Existing `pkg/flintlock/cloudinit_test.go` tests will need updating to reflect that `tailscale up` is no longer in runcmd output
- Verify `preregister.go` change with existing `pkg/tailscale/preregister_test.go`

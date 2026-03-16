# Tailscale Ephemeral Node Consolidation

**Issue:** PRI-710 — Stockyard VMs not registering as ephemeral Tailscale nodes
**Date:** 2026-03-16
**Status:** Done

## Problem

Stockyard VM instances persist in the Tailscale admin panel after destruction. They should be ephemeral nodes that auto-remove when disconnected.

## Root Cause

Two issues:

1. **The Tailscale auth key was not an ephemeral key.** In modern Tailscale (1.x+), ephemeral behavior is a property of the auth key, not a CLI flag. The `--ephemeral` flag to `tailscale up` no longer exists. Keys must be generated with the "Ephemeral" option in the Tailscale admin console.

2. **Redundant `tailscale up` code paths.** Four separate places generated `tailscale up` commands — a result of evolutionary layering where newer approaches were added without removing older ones. This made it unclear which path actually ran and created a risk of double-registration.

## What Was Done

### Auth key fix

Replaced the Tailscale auth key (in AWS SSM Parameter Store) with an ephemeral auth key generated from the Tailscale admin console. Nodes registered with this key auto-remove from the tailnet after disconnecting.

### Code consolidation

Made the VM init script (`stockyard-init.sh`) the single authority for Tailscale setup inside the VM:

- **Removed `tailscale up` from cloud-init runcmd** in both `pkg/firecracker/cloudinit.go` and `pkg/flintlock/cloudinit.go`. The init script handles Tailscale via MMDS metadata.
- **Removed `TailscaleAuthKey`/`TailscaleHostname` fields** from `CloudInitConfig` structs (firecracker and flintlock). The auth key reaches the VM via MMDS metadata, not cloud-init.
- **Stopped passing Tailscale fields** to `CloudInitConfig` in `pkg/daemon/tasks.go`.
- **Removed `--ephemeral` flag** from `preregister.go` and `stockyard-init.sh` — this flag doesn't exist in modern Tailscale and was causing pre-registration to fail.

### Dead code removal

- Deleted `vm-image/cloud-init/user-data.template` and `meta-data.template` (unreferenced Go templates using legacy `vscode` user)
- Removed `GenerateCloudInitScript` from `pkg/tailscale/tailscale.go` (defined but never called)

## What We Learned

- `--ephemeral` is not a `tailscale up` CLI flag in modern Tailscale. Ephemeral behavior is controlled by the auth key type.
- The `--no-tailscale` flow is unaffected — it gates on `tailscaleAuthKey` being empty, which skips pre-registration, MMDS auth key, and all init script Tailscale paths.
- `RemoveDevice` in `tailscale.go` is a no-op that relies on ephemeral key expiration — which now actually works with the correct key type.

## Verified

E2E tested on stockyard-eval instance: VM registered as ephemeral in the Tailscale admin console.

# stockyard

> Coding-agent VM orchestrator running agents in isolated VMs: Firecracker micro-VMs on Linux (ZFS audit snapshots) and Apple container on macOS.

**Family:** brooks · **Type:** service · **Lifecycle:** experimental · **Owner:** obra

## What it does
Stockyard is a coding-agent VM orchestrator. It runs coding agents in isolated VMs — Firecracker micro-VMs on Linux (with ZFS-based audit-trail snapshots) and Apple's `container` tool on macOS. It provides a daemon (`stockyardd`) and CLI to create, attach to, and list agent VMs, injecting env files and SSH keys, with a gRPC API for local and remote control.

## How it fits
- Depends on: —
- Used by: — (toil borrows its web-UI style but does not import it)
- External: Firecracker, ZFS, Apple `container`, Tailscale, 1Password (auth-key lookup)

## Runtime & data
- Runs: Go daemon (`stockyardd`) + CLI; manages VMs
- Data in: task specs, env files, SSH keys
- Data out: running agent VMs, ZFS snapshots, gRPC API

<!-- Maintained by the maintaining-project-map skill. Do not hand-edit; regenerated. -->

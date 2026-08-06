# stockyard

> Coding-agent VM orchestrator running agents in isolated VMs: Firecracker micro-VMs on Linux (ZFS audit snapshots) and Apple container on macOS.

**Family:** brooks · **Type:** service · **Lifecycle:** experimental · **Owner:** obra

## What it does
Stockyard is a coding-agent VM orchestrator. It runs coding agents in isolated VMs — Firecracker micro-VMs on Linux (with ZFS-based audit-trail snapshots) and Apple's `container` tool on macOS. It provides a daemon (`stockyardd`) and CLI to create, attach to, list, snapshot, and destroy agent VMs (named-task destruction is fail-closed and confirmation-gated), injecting env files and SSH keys, with a gRPC API for local and remote control. A per-instance image registry lets tasks pick their VM image (`stockyard.local/<name>` qualification).

## How it fits
- Depends on: [llm-proxy](https://github.com/prime-radiant-inc/llm-proxy) — the VM image downloads the llm-proxy release binary and runs it as an init service inside every agent VM, logging the agents' LLM API traffic.
- Used by: — (toil borrows its web-UI style but does not import it)
- External: Firecracker, ZFS, Apple `container`, Tailscale, 1Password (auth-key lookup)

## Runtime & data
- Runs: Go daemon (`stockyardd`) + CLI; manages VMs
- Data in: task specs, env files, SSH keys
- Data out: running agent VMs, ZFS snapshots, gRPC API

<!-- Maintained by the maintaining-project-map skill. Do not hand-edit; regenerated. -->

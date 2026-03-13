# Remove Git Repo Coupling from CLI/API

**Status: IMPLEMENTED** (2026-03-12)

## Overview

Stockyard's primary purpose is to safely and efficiently run ephemeral agents. The CLI currently accepts `--repo` and `--ref` flags and the API includes `repo` and `ref` fields, but these are metadata-only — no cloning actually happens. Git repository management should not be baked into stockyard's CLI or API. It belongs in the agent's prompt, a setup script, or an orchestrator.

## Motivation

- `--repo` and `--ref` suggest stockyard handles git operations, but it doesn't
- Repository setup is a concern of the workload, not the VM lifecycle tool
- Removing these fields simplifies the interface and avoids misleading users
- Future work (script injection, snapshot chaining) provides better mechanisms for workspace preparation

## Changes

### Proto (`api/stockyard.proto`)

Remove `repo` and `ref` from `CreateTaskRequest`:

```protobuf
message CreateTaskRequest {
    // Remove: string repo = 1;
    // Remove: string ref = 2;
    string name = 3;
    repeated string command = 4;
    map<string, string> env = 5;
    int32 cpus = 6;
    string memory = 7;
    bool no_tailscale = 8;
    string tailscale_auth_key = 9;
    repeated string ssh_authorized_keys = 10;
}
```

Remove `repo` and `ref` from `Task`:

```protobuf
message Task {
    string id = 1;
    string name = 2;
    // Remove: string repo = 3;
    // Remove: string ref = 4;
    string status = 5;
    string tailscale_hostname = 6;
    string created_at = 7;
    string stopped_at = 8;
}
```

### CLI (`cmd/stockyard/run.go`)

- Remove `--repo` flag (currently required — remove the requirement too)
- Remove `--ref` flag
- Remove any validation that depends on repo being set

### Daemon (`pkg/daemon/tasks.go`)

- Remove `Repo` and `Ref` from the internal task struct
- Remove the command-string construction that references repo
- Clean up any repo/ref references in task creation

### gRPC handler (`pkg/daemon/grpc.go`)

- Stop reading `req.Repo` and `req.Ref` from `CreateTaskRequest`

### State/DB (`pkg/daemon/state.go`)

- Remove `repo` and `ref` columns from the tasks table
- Migration: drop columns (or ignore them — SQLite makes column removal awkward, so marking them unused is acceptable)

### Cloud-init (`pkg/firecracker/cloudinit.go`)

- Remove any repo/ref references from `MMDSMetadata` if present

### Documentation

- Update any docs that reference `--repo` or `--ref`
- Update `CLAUDE.md` if it mentions repo flags
- Update `docs/INITIAL_PROMPT.md` if relevant

## What This Does NOT Change

- The `command` field stays — it's the workload
- Environment variable injection stays (`GITHUB_TOKEN` is still useful for agents that clone repos themselves)
- Tailscale, SSH, snapshots, and all other functionality unchanged

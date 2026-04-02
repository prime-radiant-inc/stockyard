# VMBackend Interface Cleanup Backlog

**Date:** 2026-04-01
**Source:** Code review by Knuth, Dijkstra, Ritchie (Bob reviewers)
**Context:** These are design improvements deferred from the initial implementation. Best tackled by a fresh team once the dual-backend architecture is stable.

## 1. Magic metadata keys in VMConfig → FirecrackerBackend

**Problem:** The Firecracker adapter extracts backend-specific fields from `VMConfig.Env` and `VMConfig.Metadata` using underscore-prefixed magic keys:
```go
cfg.Env["_tailscale_auth_key"]
cfg.Metadata["_tailscale_state"]
cfg.Metadata["_network_ip"]
```

The caller (tasks.go) has to know which backend it's talking to and smuggle data through generic maps.

**Knuth's suggestion:** Add a `BackendOptions any` field to `VMConfig` that each backend can type-assert to its own options struct. Or promote Tailscale/network fields to first-class VMConfig fields since they're not truly backend-specific — they're network topology concerns.

**Scope:** Changes VMConfig, FirecrackerBackend.CreateVM, daemon CreateTask, and tests.

## 2. VMInfo leaks Firecracker-specific fields

**Problem:** `VMInfo` has `CID uint32` and `VsockPath string` with comments "(Firecracker-specific, 0 if unused)". These are always zero for the vfkit backend.

**Knuth's suggestion:** Move backend-specific return data to a `BackendInfo any` field or `map[string]string` escape hatch. Keep VMInfo clean with only universal fields (ID, PID, IP, State, CreatedAt).

**Scope:** Changes VMInfo, both backends, daemon state (CID/VsockPath storage), snapshot service (CID-based resolution), dashboard (shell on vsock).

**Note:** This pairs with #1 — both are about cleaning the interface boundary between generic and backend-specific concerns. Tackle together.

# macOS Apple `container` Backend — Design

- **Date:** 2026-05-21
- **Status:** Approved (design); reviewed by Riker; ready to plan.
- **Author:** Pham@6e1fb16f (Opus 4.7)
- **Revisions:**
  - 2026-05-21 — backend renamed `AppleContainerBackend`; CLI access via
    `stockyard attach`.
  - 2026-05-21 — incorporated peer review (Riker): backend/`vm_id` threaded
    through the proto; explicit rootfs-provisioner guard; `container logs`
    follower for log capture; CLI-driven daemon-restart reconciliation;
    `--name`-based container identity; per-task metrics declared a non-goal.

## Context

On macOS, stockyard runs Linux workloads through the **vfkit** backend
(`pkg/vmbackend/vfkit.go`): it boots a Kata kernel plus a hand-built Alpine
ext4 rootfs (`vm-image/macos/`). That path carries its own image-and-kernel
build pipeline, separate from the Firecracker/Linux image
(`vm-image/Dockerfile`, Ubuntu amd64). It is also the thinner path — `stockyard
exec`, ZFS snapshots, and Tailscale are all Firecracker-only.

Apple's `container` tool (open-sourced June 2025; ~0.12.x as of this writing)
runs each container in its own lightweight VM via Virtualization.framework,
with `vminitd` as PID 1 and an Apple-supplied kernel. It pulls and runs
standard OCI images directly.

This design adds Apple's `container` as a **third VM backend** on macOS. The
motivating win is image simplification: macOS stops building its own image and
kernel and instead consumes an OCI image produced by the same Docker build that
feeds Firecracker.

## Decisions

1. **Additive, not a replacement.** Apple's `container` is added as a new
   `config.backend` value, `"apple-container"`. vfkit and Firecracker are
   untouched. Whether to later delete vfkit and `vm-image/macos/` is deferred.
2. **Shared multi-arch image.** `vm-image/Dockerfile` is restructured into a
   multi-stage build with a shared `base` stage and thin `firecracker` and
   `container` targets. The `base` stage becomes arch-clean so the `container`
   target builds native `arm64`.
3. **Access is container-native.** The daemon and dashboard reach a container
   via `container exec` and `container cp` — no `sshd`, no SSH key injection,
   no IP-readiness race. The legacy in-guest `stockyard-shell` vsock agent is
   not built or shipped on this path; `container exec` replaces it.
4. **Opt-in Tailscale.** The container entrypoint starts `tailscaled` in
   userspace-networking mode and `tailscale up --ssh` **only** when a Tailscale
   auth key is configured. Off by default.
5. **macOS 26+ assumed.** Pre-Tahoe macOS support is a non-goal.
6. **Go drives the CLI.** Apple exposes no stable API for non-Swift clients.
   The backend shells out to the `container` CLI with `--format json`.
7. **Named for Apple's tool specifically.** The backend type is
   `AppleContainerBackend`, config value `"apple-container"` — not a generic
   `Container*` name — so a future, unrelated container runtime can be added
   without a collision.
8. **Container identity is `--name`.** Each container is created with
   `--name stockyard-<vmID>`, giving a deterministic, directly-addressable name
   for `exec`/`cp`/`inspect`. No ID-mapping file is needed.

## Scope

**In scope**

- An `AppleContainerBackend` implementing `vmbackend.Backend`.
- Config and daemon wiring for `config.backend == "apple-container"`, including
  the rootfs-provisioner guard and CLI-driven restart reconciliation.
- Surfacing the task's `backend` and `vm_id` through the gRPC API.
- A multi-stage, multi-arch `vm-image/Dockerfile` with a `container` target.
- A container entrypoint: `llm-proxy` plus opt-in Tailscale.
- The dashboard web terminal and `stockyard attach` on the new path.

**Non-goals**

- No changes to the vfkit or Firecracker backends (beyond `stockyard attach`
  and the dashboard terminal dispatching by backend).
- No ZFS snapshots on this path.
- **No per-task VM metrics on the apple-container path.** The daemon's
  `metricsPoller` is Firecracker-specific (a metrics FIFO). For apple-container
  it stays `nil`; the dashboard shows no per-VM CPU/mem. Host metrics still
  work. `container stats` is a possible future source.
- No `stockyard exec` queue work — experimental, out of scope.
- No `stockyard-shell` / `stockyard-snapshot` in the container image.
- Pre-Tahoe macOS support.

## Architecture

### 1. `AppleContainerBackend` — `pkg/vmbackend/apple_container.go`

Implements the existing `vmbackend.Backend` interface
(`pkg/vmbackend/backend.go`) by shelling out to the `container` CLI.

| Interface method | `container` invocation |
|---|---|
| `CreateVM` | `container run -d --name stockyard-<id>` — image, `--cpus`, `--memory`, `--env`, `--label task-id=<id>` |
| `StartVM`  | `container start stockyard-<id>` |
| `StopVM`   | `container stop stockyard-<id>` |
| `DeleteVM` | `container stop` (if running) then `container rm stockyard-<id>` |
| `GetVM`    | `container inspect stockyard-<id>` → status |
| `ListVMs`  | `container ls --all --format json` |
| `Close`    | stops log followers; otherwise no-op |

Details:

- **Identity.** The container name is `stockyard-<VMConfig.ID>` (the 8-char VM
  ID). All subsequent commands address the container by that name. A per-VM
  state directory `{StateDir}/{cfg.ID}/` is still created — only to hold the
  captured log files (below).
- **`VMInfo`.** `IP` is read from `container inspect`. `CID` is `0` and
  `VsockPath` is `""` (Firecracker-specific, unused). `PID` is best-effort
  (`0` is acceptable — `container` runs the workload under its own
  service-managed helper, so there is no meaningful hypervisor PID).
- **Unused `VMConfig` fields.** `KernelPath`, `RootfsPath`, `CloudInitData`,
  `SSHAuthorizedKeys` are unused; `container` owns kernel and rootfs.
- **Memory.** `VMConfig.MemoryMB` is rendered as `fmt.Sprintf("%dM",
  MemoryMB)` — `container --memory` accepts a `K/M/G/T/P` suffix. (Settled;
  not a verify-by-test item.)
- **Log capture.** `container run -d` does **not** write `stdout.log` /
  `stderr.log` files; container output lives in `container logs`. So on
  `CreateVM`/`StartVM` the backend spawns a `container logs -f stockyard-<id>`
  follower whose stdout/stderr are redirected into
  `{StateDir}/{cfg.ID}/stdout.log` and `stderr.log`. This makes the daemon's
  existing per-task `logTailer` (which tails exactly those paths) work
  unchanged. The follower is tracked and killed on `StopVM`/`DeleteVM`/`Close`.
- **Fail-fast probe.** `createAppleContainerBackend` runs a cheap
  `container system status` (or `container ls`) probe at construction; if the
  `container` service is not running it returns a clear, actionable error
  rather than failing opaquely on first `CreateVM`.
- **Test seam.** The backend takes an injectable `commandRunner` (a function
  type wrapping `exec.CommandContext`) so unit tests exercise argument
  construction and JSON parsing without a real `container` daemon.

### 2. Config and daemon wiring

- **`pkg/config`.** Add `AppleContainerConfig{ ContainerBin string; Image
  string }` in a new `pkg/config/apple_container.go` (must compile on all
  platforms) and an `AppleContainer AppleContainerConfig` field on `Config`.
  `ContainerBin` defaults to `"container"`.
- **Backend switch** (`pkg/daemon/daemon.go`, ~lines 104–129). Add
  `case "apple-container":` calling `createAppleContainerBackend(cfg)`.
- **Build-tagged constructors.** `createAppleContainerBackend` is added to
  `backend_darwin.go` (real backend) and `backend_other.go` (returns
  "available only on macOS"), matching the `createVfkitBackend` split.
- **No ZFS / DHCP / IP pool.** Already `nil` for non-Firecracker backends; the
  existing guards in `daemon.go` cover this.
- **Rootfs provisioner guard.** `createRootfsProvisioner`
  (`pkg/daemon/rootfs_darwin.go`) currently returns an APFS provisioner
  whenever `cfg.Rootfs.BaseImage != ""` — it does **not** branch on backend.
  Add an explicit `if cfg.Backend == "apple-container" { return nil }` at the
  top so a stray `rootfs.base_image` in config cannot produce an unused APFS
  clone. `CreateTask` (`pkg/daemon/tasks.go`) already guards every
  `provisioner`/`zfs`/`ipPool` call with a nil check, so it needs **no
  change** — a nil provisioner yields an empty `RootfsPath`, which the
  apple-container backend ignores.
- **Restart reconciliation.** `reconcileRunningVMs` (`daemon.go`) currently
  detects liveness via `firecracker.pid` / `vfkit.pid` files. An
  apple-container task has neither. For the apple-container backend,
  reconciliation must instead determine each task's status from the backend
  itself (`backend.GetVM` → `container inspect`, or one `backend.ListVMs`
  call), so a daemon restart does not wrongly mark live containers `stopped`.

### 3. Task data model — surfacing `backend` and `vm_id`

The dashboard terminal and `stockyard attach` must know a task's backend and
its container name. Today neither is reachable: the gRPC `Task` message
(`api/stockyard.proto`), `dashboard.Task`, and `client`-side task structs carry
no backend field, and `pkg/dashboard/terminal_handler.go` unconditionally calls
`GetVsockPath()` and returns 503 if it is empty — which it always is on the
apple-container path.

Changes:

- **`api/stockyard.proto`** — add `string backend` and `string vm_id` to the
  `Task` message; regenerate Go bindings.
- **Population.** The daemon is single-backend, so `backend` is filled from
  `cfg.Backend` for every task; `vm_id` is filled from the existing
  `state.Task.VMID`. No new per-task storage.
- **`dashboard.DaemonAPI`** — expose the backend type and per-task VM ID
  (e.g. `GetBackend() string`, and `vm_id` via the existing task lookup).
- This single change unblocks both the dashboard terminal (§6) and
  `stockyard attach` (§7). It corrects the earlier "no new daemon RPC surface"
  assumption — the proto **is** the surface, and it changes once, here.

### 4. Unified image — `vm-image/Dockerfile`

Restructure the single-stage `Dockerfile` into a multi-stage build:

- **`base` stage.** Everything architecture-independent: base packages,
  languages (Python, Node, Go, Rust), developer tools, coding agents,
  `llm-proxy`, Tailscale, the VM user. The hardcoded-amd64 download URLs
  (`yq`, the Go tarball, AWS CLI, gcloud) are changed to honor `TARGETARCH`
  so the stage builds for both `amd64` and `arm64`.
- **`firecracker` target.** `FROM base`, plus systemd config, cloud-init,
  network config, the in-Docker kernel build, `CMD ["/sbin/init"]`. Must
  produce **functionally identical** output to today's image for the amd64
  Firecracker path.
- **`container` target.** `FROM base`, plus the entrypoint script (§5) and an
  `ENTRYPOINT`. No systemd, no kernel build, no cloud-init. Built `arm64`.

Build plumbing: `vm-image/build.sh` / `Makefile` gain a target that builds the
`container` target. `vm-image/macos/` (the Alpine vfkit image) is left
untouched.

### 5. Container entrypoint — `vm-image/init/stockyard-container-init.sh`

A small shell script baked into the `container` target as its `ENTRYPOINT`:

1. If a Tailscale auth-key env var is set: start `tailscaled
   --tun=userspace-networking`, then `tailscale up --ssh --authkey=<key>`.
   Userspace mode needs no TUN device and no privileges; Tailscale SSH works
   in it. If unset, skip — Tailscale is opt-in.
2. Start `llm-proxy` in the background.
3. `exec sleep infinity` — keep the container alive; access is via
   `container exec`.

The auth key travels in `VMConfig.Env`; `AppleContainerBackend.CreateVM`
forwards `Env` as `--env` flags. On `StartVM` the container's writable layer
(and thus `tailscaled`'s persisted node state) survives, so a restart rejoins
the tailnet from saved state without needing a fresh auth key.

### 6. Dashboard web terminal

`pkg/dashboard/terminal_handler.go` gains a second session type beside
`VsockSession`:

- **`ContainerExecSession`** — runs `container exec -t -i stockyard-<vmID>
  <shell>` under a host PTY (`creack/pty`, already a dependency), bridging the
  PTY to the websocket.
- The websocket-side `shell.Msg*` framing (dashboard JS ↔ Go handler) is
  unchanged; only the session's backing transport differs. Resize sets the
  host PTY window size.
- `ServeHTTP` branches on the task's backend (from §3). The current
  unconditional `GetVsockPath()`-empty → 503 guard moves **inside** the
  Firecracker/vfkit branch.

### 7. CLI access — `stockyard attach`

`stockyard attach <task>` already exists (`cmd/stockyard/attach.go`) and
currently `exec`s `ssh` to the task's Tailscale hostname or IP. It is
**modified** to dispatch on the task's backend (now available via §3):

- **`apple-container`** — `exec`s `container exec -t -i stockyard-<vmID>
  <shell>`.
- **vfkit / Firecracker** — unchanged (existing SSH path).

Ad-hoc commands and file copy use `container exec` / `container cp` directly
against the `stockyard-<vmID>` name.

## Implementation phases

Ordered so the verifiable core lands first and the slow/risky image work is
isolated:

1. **Backend core** — `AppleContainerBackend`, `AppleContainerConfig`, daemon
   wiring (backend case, constructors, provisioner guard, system-status probe,
   reconciliation), log follower. Compiles and unit-tests with no `container`
   installed.
2. **Task data model + CLI** — proto `backend`/`vm_id`, regeneration,
   population, `dashboard.DaemonAPI`, `stockyard attach` dispatch.
3. **Dashboard terminal** — `ContainerExecSession` and the `ServeHTTP` branch.
4. **Unified image** — multi-stage multi-arch `Dockerfile`, entrypoint, build
   plumbing.

Phases 1–3 are pure Go and verifiable overnight. Phase 4 depends on an image
builder; full `firecracker`-target rebuild verification may need CI or a Linux
builder and is reported honestly if it cannot be completed locally.

## Testing

- **`AppleContainerBackend` unit tests** via the `commandRunner` seam — arg
  construction, JSON parsing, log-follower lifecycle. No real daemon.
- **Reconciliation test** — a restart with a (faked) running container is
  reconciled to `running`, not `stopped`.
- **`stockyard attach` test** — backend dispatch picks `container exec` for
  apple-container, SSH otherwise (extends `attach_test.go`).
- **Integration test** — gated behind an env flag (set only where `container`
  is installed on macOS 26): real `create` → `exec` → teardown.
- **Image build** — CI builds both targets; asserts the `firecracker` target
  is functionally unchanged versus pre-refactor.

## Risks and verify-by-test items

- **`container` is pre-1.0** (~0.12.x). Pin a known-good version; parse
  `--format json`, never human output.
- **Writable-layer persistence across stop/start.** `StartVM` and
  Tailscale-state survival both depend on a stopped-not-deleted container
  retaining its writable filesystem. Expected Docker-like semantic; confirm by
  test.
- **`llm-proxy` arm64 build** must exist as a release artifact for the
  arm64 `container` image. Verify before relying on it.
- **Native-TUN Tailscale is not pursued.** Userspace-networking mode is the
  committed path.

## Prerequisites

- macOS 26 (Tahoe) or later, Apple Silicon.
- Apple `container` installed and its service running: `brew install container`
  then `container system start`. (`container` ships as a signed `.pkg`
  installing a launchd service, so it is not a clean fit for `mise` — `brew` or
  the `.pkg` directly is the install path.) Documented in
  `vm-image/macos/README.md`.

## Deferred / future work

- Deleting the vfkit backend and `vm-image/macos/` once the apple-container
  path is proven.
- Per-task metrics via `container stats`.
- Workspace snapshots on macOS, if ever wanted — host-side on an APFS volume
  bind-mounted into the container, independent of the VM backend.
- Native-TUN Tailscale, only if bulk tailnet throughput matters.

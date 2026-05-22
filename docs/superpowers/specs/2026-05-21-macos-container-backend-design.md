# macOS Apple `container` Backend — Design

- **Date:** 2026-05-21
- **Status:** Approved (design); implementation plan pending
- **Author:** Pham@6e1fb16f (Opus 4.7)
- **Revisions:** 2026-05-21 — backend renamed `AppleContainerBackend`; CLI
  access via `stockyard attach`.

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

These were settled during brainstorming and are recorded so the rationale
survives:

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
   auth key is configured. This makes a container addressable on the tailnet
   (MagicDNS + Tailscale SSH) for direct user/IDE access. Off by default.
5. **macOS 26+ assumed.** Per-container networking on `container` is a
   first-class experience only on macOS 26 (Tahoe). Pre-Tahoe support is a
   non-goal.
6. **Go drives the CLI.** Apple exposes no stable API for non-Swift clients.
   The backend shells out to the `container` CLI with `--format json`, the same
   pattern the vfkit backend uses for `vfkit`.
7. **Named for Apple's tool specifically.** The backend type is
   `AppleContainerBackend` and the config value is `"apple-container"` — not a
   generic `Container*` name — so a future, unrelated container runtime can be
   added without a naming collision.

## Scope

**In scope**

- An `AppleContainerBackend` implementing `vmbackend.Backend`.
- Config and daemon wiring for `config.backend == "apple-container"`.
- A multi-stage, multi-arch `vm-image/Dockerfile` with a `container` target.
- A container entrypoint: `llm-proxy` plus opt-in Tailscale.
- The dashboard web terminal and `stockyard attach` on the new path.

**Non-goals**

- No changes to the vfkit or Firecracker backends (beyond `stockyard attach`
  dispatching by backend).
- No ZFS snapshots on this path (`container` has no copy-on-write fork; macOS
  never had snapshots anyway).
- No `stockyard exec` queue work — that mechanism is experimental and out of
  scope.
- No `stockyard-shell` / `stockyard-snapshot` in the container image.
- Pre-Tahoe macOS support.

## Architecture

### 1. `AppleContainerBackend` — `pkg/vmbackend/apple_container.go`

Implements the existing `vmbackend.Backend` interface (`pkg/vmbackend/backend.go`)
by shelling out to the `container` CLI.

| Interface method | `container` invocation |
|---|---|
| `CreateVM` | `container run -d` — image, `--cpus`, `--memory`, `--env`, `--label task-id=<id>` |
| `StartVM`  | `container start <container-id>` |
| `StopVM`   | `container stop <container-id>` |
| `DeleteVM` | `container stop` (if running) then `container rm <container-id>` |
| `GetVM`    | `container inspect <container-id>` → status |
| `ListVMs`  | `container ls --all --format json` |
| `Close`    | no-op |

Details:

- **ID mapping.** Each VM gets a state directory `{StateDir}/{cfg.ID}/`
  containing a `container-id` file mapping stockyard's 8-char `VMConfig.ID` to
  the `container`-assigned ID. This mirrors the vfkit backend's per-VM-directory
  pattern (`pkg/vmbackend/vfkit.go`) and avoids depending on a `--name` flag.
  `--label task-id=<id>` is set additionally as a convenience for `container ls`.
- **`VMInfo`.** `IP` is read from `container inspect`. `CID` is `0` and
  `VsockPath` is `""` — these are Firecracker-specific and unused here. `PID`
  is best-effort (the per-container runtime helper PID if available, else `0`).
- **Unused `VMConfig` fields.** `KernelPath`, `RootfsPath`, `CloudInitData`,
  and `SSHAuthorizedKeys` are not used on this path. `container` owns the
  kernel and the rootfs; access is via `container exec`.
- **Memory.** `VMConfig.MemoryMB` is rendered for `--memory` (likely a size
  string such as `2048m`). Exact units are a verify-by-test item.
- **Test seam.** The backend takes an injectable `commandRunner` (a function
  type wrapping `exec.CommandContext`) so unit tests exercise argument
  construction and JSON parsing without a running `container` daemon.

### 2. Config and daemon wiring

- **`pkg/config`.** Add `AppleContainerConfig{ ContainerBin string; Image
  string }` (`pkg/config/apple_container.go`, alongside `vfkit.go`) and an
  `AppleContainer AppleContainerConfig` field on `Config`
  (`pkg/config/config.go`). `ContainerBin` defaults to `"container"`.
- **`pkg/daemon/daemon.go`.** Add `case "apple-container":` to the backend
  switch (currently handling `""`/`firecracker` and `vfkit`, ~lines 104–129)
  calling `createAppleContainerBackend(cfg)`.
- **Build-tagged constructors.** `createAppleContainerBackend` is added to
  `backend_darwin.go` (real backend) and `backend_other.go` (returns an error:
  the Apple `container` backend is only available on macOS), matching the
  existing `createVfkitBackend` split.
- **No ZFS / DHCP / IP pool.** These stay `nil` for `apple-container`, exactly
  as for `vfkit` — the relevant guards in `daemon.go` already key on
  `firecracker`/empty backend.
- **No rootfs provisioner.** `container` owns image layers and the per-container
  writable layer, so `createRootfsProvisioner` (`pkg/daemon/rootfs_darwin.go`)
  returns `nil` for this backend. `CreateTask` (`pkg/daemon/tasks.go`) skips the
  `provisioner.Clone()` step when the provisioner is `nil`. This is the one
  daemon-orchestration change beyond the backend switch.

### 3. Unified image — `vm-image/Dockerfile`

Restructure the single-stage `Dockerfile` into a multi-stage build:

- **`base` stage.** Everything architecture-independent: base packages,
  languages (Python, Node, Go, Rust), developer tools, coding agents,
  `llm-proxy`, Tailscale, and the VM user. The handful of hardcoded-amd64
  download URLs (`yq`, the Go tarball, AWS CLI, gcloud) are changed to honor
  `TARGETARCH` so the stage builds for both `amd64` and `arm64`.
- **`firecracker` target.** `FROM base`, plus systemd configuration,
  cloud-init, network config, the in-Docker kernel build, and
  `CMD ["/sbin/init"]`. This target must produce **functionally identical**
  output to today's image — the Firecracker/Linux path carries no behavior
  change. CI verifies this (see Testing).
- **`container` target.** `FROM base`, plus the entrypoint script (§4) and an
  `ENTRYPOINT`. No systemd, no kernel build, no cloud-init. Built `arm64`.

Build plumbing:

- `vm-image/build.sh` / `vm-image/Makefile` gain a target that builds the
  `container` target via `container build --target container` (or `docker
  buildx` followed by `container` import — the implementation plan chooses).
- `vm-image/macos/` (the Alpine vfkit image) is left untouched; vfkit still
  uses it. It is not part of the `container` path.

### 4. Container entrypoint — `vm-image/init/stockyard-container-init.sh`

A small shell script baked into the `container` target as its `ENTRYPOINT`.
On start it:

1. If a Tailscale auth-key environment variable is set: start
   `tailscaled --tun=userspace-networking`, then `tailscale up --ssh
   --authkey=<key>`. Userspace mode needs no TUN device and no elevated
   privileges, and Tailscale SSH (the server built into `tailscaled`) works in
   that mode. If the variable is unset, skip — Tailscale is opt-in.
2. Start `llm-proxy` in the background, so coding-agent API traffic is logged,
   matching the Firecracker image's behavior.
3. `exec sleep infinity` — keep the container alive. All host-driven access is
   via `container exec`.

The Tailscale auth key travels in `VMConfig.Env`, which `CreateTask` already
populates for the Firecracker path; `AppleContainerBackend.CreateVM` forwards
`Env` entries as `--env` flags to `container run`.

### 5. Dashboard web terminal

`pkg/dashboard/terminal_handler.go` currently bridges the dashboard websocket
to the in-guest shell over vsock (`VsockSession`, `createVsockSession`). Add a
second session type for the `apple-container` path:

- **`ContainerExecSession`.** Runs `container exec -t -i <container-id>
  <shell>` under a host PTY (`creack/pty`), bridging the PTY to the websocket.
- The websocket-side framing (`shell.Msg*` between the dashboard JS and the Go
  handler) is unchanged — only the session's backing transport differs.
- Terminal resize sets the host PTY's window size.
- The handler selects the session type based on the active backend.

### 6. CLI access — `stockyard attach`

`stockyard attach <task>` provides interactive shell access, dispatching on the
task's backend:

- **`apple-container`** — wraps `container exec -t -i <container-id> <shell>`.
- **vfkit / Firecracker** — uses the existing access path (SSH / vsock).

If `stockyard attach` does not already exist in `cmd/stockyard/`, it is added,
following existing command patterns; if it exists, the `apple-container` branch
is added to its backend dispatch. The daemon surfaces each task's `container`
ID in task status so that ad-hoc commands and file copy can use `container
exec` and `container cp` directly — no new daemon RPC surface.

## Testing

- **`AppleContainerBackend` unit tests.** Exercise argument construction and
  JSON parsing through the injected `commandRunner`; no real `container` daemon
  required.
- **Integration test.** Gated behind an environment flag (set only where
  `container` is installed on macOS 26): real `create` → `exec` → teardown.
- **Image build.** CI builds both the `firecracker` and `container` targets.
  CI asserts the `firecracker` target's output is unchanged versus pre-refactor
  (functional comparison of the produced rootfs).

## Risks and verify-by-test items

- **`container` is pre-1.0** (~0.12.x). Pin a known-good version; parse
  `--format json`, never human-readable output.
- **Writable-layer persistence across stop/start.** `StartVM` depends on a
  stopped-but-not-deleted container retaining its writable filesystem. This is
  the expected Docker-like semantic but is lightly documented for `container`;
  confirm by test.
- **`container run --memory` units** — confirm size-string vs. MB.
- **`llm-proxy` arm64 build.** The `base` stage installs `llm-proxy` by
  `TARGETARCH`; an `llm-proxy-linux-arm64` release artifact must exist for the
  `container` (arm64) image. Verify before relying on it.
- **Native TUN Tailscale is not pursued.** Userspace-networking mode is the
  committed path; native mode would risk reintroducing a custom-kernel
  dependency, which this design exists to avoid.

## Prerequisites

- macOS 26 (Tahoe) or later, Apple Silicon.
- Apple `container` installed and its service running: `brew install container`
  then `container system start`. (`container` ships as a signed `.pkg` that
  installs a launchd service, so it is not a clean fit for `mise`'s
  binary-fetching backends — `brew` or the `.pkg` directly is the install
  path.) This is documented in `vm-image/macos/README.md`.

## Deferred / future work

- Deleting the vfkit backend and `vm-image/macos/` once the `apple-container`
  path is proven.
- Workspace snapshots on macOS, if ever wanted — would be built host-side on an
  APFS volume bind-mounted into the container, independent of the VM backend,
  not via ZFS.
- Native-TUN Tailscale, only if bulk tailnet throughput ever matters.

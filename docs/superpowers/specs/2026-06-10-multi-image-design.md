# Multi-Image Support — Design

- **Ticket:** [PRI-2150](https://linear.app/prime-radiant/issue/PRI-2150/stockyard-multi-image-support-per-task-image-selection)
- **Date:** 2026-06-10
- **Status:** Approved design, pre-implementation

## Problem

Stockyard offers exactly one image, fixed at daemon startup. On macOS, `apple_container.image` is mandatory daemon config and `buildRunArgs` appends it unconditionally (`pkg/vmbackend/apple_container.go:131`). On Linux, every VM clones the single hardcoded ZFS snapshot `tank/stockyard/images/rootfs@base` (`pkg/firecracker/client.go:152`, `pkg/daemon/daemon.go:553`). `stockyard run` has no `--image` flag and `CreateTaskRequest` has no image field.

The first real consumer is Prudence ([PRI-2063](https://linear.app/prime-radiant/issue/PRI-2063/prudence-stockyard-remote-sandbox-backend-scp-transport)): it wants cells running `prudence-vm`, which differs from the default `stockyard-vm`. Today that requires repointing the whole daemon.

## Decisions

Design review settled these:

1. **Both backends, phased.** apple-container ships first; Firecracker follows soon after. One spec covers both.
2. **OCI refs name images everywhere.** `--image prudence-vm:1.2` is the same string on both platforms. An OCI ref names the *userland*, not the VM: the kernel and boot configuration remain stockyard's concern.
3. **Resolution is strictly on-host.** Stockyard never contacts a registry. macOS resolves refs against `container`'s image store. Linux resolves refs against stockyard's own registered-image store — Docker is a build-time tool, possibly on a different machine, and may not exist on the stockyard host at all.
4. **Entrypoint behavior is a documented contract, not an API.** No `--command` override; `CreateTaskRequest` field 2 stays reserved. Images must ship a self-sustaining init (see Image Contract).
5. **One shared kernel for now.** Registered-image metadata reserves an optional kernel reference so per-image kernels need no schema change, but pairing semantics are a phase 2 design point.

## API Surface

Applies to both backends from phase 1.

- **Proto:** `CreateTaskRequest.image = 10` (string). Empty means the daemon-configured default — current behavior, unchanged. `Task.image = 10` reports each task's image in `GetTask`/`ListTasks`.
- **CLI:** `stockyard run --image <ref>`. No other run-path commands change.
- **Config:** existing fields keep their meaning as *the default image*: `apple_container.image` on macOS; `firecracker.rootfs_path`/`kernel_path` on Linux. No config migration.
- **Persistence:** the task record (SQLite) stores the *resolved* image ref — the ref the task actually runs (the daemon default when the request was empty), never the empty string — so listings, restarts, and daemon-restart reconciliation report it.

## Phase 1 — apple-container

- `vmbackend.VMConfig` gains `Image string`. The daemon threads `CreateTaskRequest.image` through; `buildRunArgs` uses `cfg.Image` and falls back to `b.cfg.Image` when empty.
- **Validate before creating task state.** `CreateTask` checks the ref with `container image inspect <ref>`. On a miss, the error lists the local store's contents (`container image ls`): `image "x" not found on host; available: stockyard-vm:latest, prudence-vm:1.2`. Validation is a backend seam (an optional interface the daemon consults), testable through the existing fake `commandRunner`.
- **Restart path is unchanged.** `container start` reuses the existing container; its image was baked at `container run` time.
- **Firecracker in phase 1:** any non-empty `image` fails with `firecracker backend does not support per-task images yet (PRI-2150 phase 2)`. No silent fallback. (Phase 1 Firecracker has no image names to match against — config holds a rootfs path, not a ref.)

## Phase 1.5 — image CLI surface

Lands the complete `stockyard image` interface before the Linux registry exists, so phase 2 becomes purely backend work — no proto, CLI, or workflow churn at the moment the registry lands. Exercising the interface during the Mac period validates the workflow Linux inherits.

- **Proto:** `ListImages`, `ImportImage`, and `RemoveImage` RPCs land now, fixing the whole surface. `ImageInfo {reference, digest, size, created_at}` — `size` is a human-readable string (the `container` CLI reports `"4 MB"`-style strings; phase 2 formats bytes the same way); `created_at` is best-effort (only present when the image carries an OCI created annotation). `ImportImageRequest {name, rootfs_path, kernel_path}` carries host-side paths; `RemoveImageRequest {name}`.
- **vmbackend:** `ImageLister` optional interface — `ListImages(ctx) ([]ImageInfo, error)` — same pattern as `ImageValidator`. apple-container implements it via `container image ls --format json` (schema verified against `container` 0.12.x: array of `{fullSize, descriptor{digest, annotations}, reference}`).
- **Daemon:** `ListImages` consults the backend's `ImageLister`, or returns Unimplemented naming the backend. `ImportImage`/`RemoveImage` return per-backend guidance: apple-container redirects to `container image ...` — its store is authoritative, and stockyard does not mutate a store it does not own (this stays true after phase 2); Firecracker answers with the phase-2 message.
- **CLI:** `stockyard image` command group: `ls` (REFERENCE / DIGEST / SIZE table, answered by the *daemon* so it is truthful for remote daemons), `import`, and `rm` (which surface the daemon's errors verbatim).

## Phase 2 — Firecracker image registry

Stockyard owns a registry of named, prepared rootfses. The existing `vm-image` Docker pipeline flattens the OCI image to a rootfs out-of-band; the artifact lands on the host; registration ingests it.

- **A registered image is:** a ZFS dataset `tank/stockyard/images/<name>` with an `@base` snapshot, plus a metadata row in the daemon's SQLite store: name (the OCI-style ref), rootfs size, registered-at, optional kernel reference (empty = shared `vmlinux.bin`).
- **The default image joins the registry.** At startup the daemon seeds a registered image named `default` from the configured `rootfs_path`; `ensureBaseImage()` generalizes to ensure every registered image has its base. `stockyard run` without `--image` is equivalent to `--image default`.
- **Registration CLI:** the `stockyard image import <name> --rootfs <path-on-host> [--kernel <path>]` / `ls` / `rm <name>` surface already exists from phase 1.5; phase 2 implements the Firecracker side of those RPCs — import ingests, `rm` destroys, `ls` reads the registry. macOS behavior is unchanged (`ls` proxies the `container` store; `import`/`rm` keep redirecting to the `container` CLI).
- **Re-import replaces, scoped scorched-earth.** Importing over an existing name destroys that name's base and its clones — tasks on that image die. This is today's deploy semantics (`zfs destroy -R`), scoped to one name instead of the world. Generation-based replacement (old base drains as its clones exit) is a future refinement, not v1.
- **Run-time resolution:** name → metadata row → clone `images/<name>@base`. An unknown name produces the same error shape as the macOS miss, listing registered images.
- **`vm-image` pipeline:** build scripts gain an image-name parameter. The `deploy` target shrinks to *build → copy artifact to host → `stockyard image import`*. The hand-rolled ZFS surgery leaves the Makefile; the daemon owns its store.

## Image Contract

New doc: `docs/image-contract.md`. The two platforms boot differently, and the existing `stockyard-vm` build already reflects this — the Dockerfile's `firecracker` stage ships systemd (`CMD ["/sbin/init"]`); the `container` stage sets `ENTRYPOINT ["stockyard-container-init.sh"]`. The contract names that practice:

- **macOS:** the image's ENTRYPOINT/CMD is PID 1 and must be self-sustaining (the `stockyard-container-init.sh` pattern). A bare language-runtime CMD (e.g. `node`) exits immediately.
- **Linux:** the rootfs must contain a bootable `/sbin/init` — systemd in `stockyard-vm`. Firecracker boot args pass no `init=` (`pkg/firecracker/client.go:264`), so the rootfs decides.
- **Integrated tier:** images that want `stockyard attach`, ssh, or scp transports also ship tailscale + sshd (+ `stockyard-shell`).
- **Image families:** one OCI name on both platforms means the same *family* — typically per-target stages from a shared Docker base — not identical bytes.

`prudence-vm` needs the integrated tier regardless — hence no command override.

## Errors

One legible shape across backends for the common failure: `image "<ref>" not found on host; available images:` followed by the host's image listing (the `container image ls` table on macOS; one registered ref per line on Linux). Stockyard does not parse the `container` CLI's table output to reformat it — the raw listing is the contract. Phase 1's Firecracker rejection names the ticket and phase. Import failures (unreadable rootfs file, insufficient pool space) report the underlying cause directly.

## Testing

- **apple-container:** unit tests through the fake `commandRunner` — the ref lands in `container run` args; validation hits `image inspect`; miss errors carry the available list.
- **Daemon:** CreateTask validation tests (default fallback, unknown ref, phase-1 Firecracker rejection); proto/CLI plumbing tests.
- **Firecracker (phase 2):** registry operations behind the existing integration-test pattern (real ZFS required).
- **End-to-end:** macOS smoke before push — scratch instance config, daemon up, `stockyard run --image` against a second loaded image, verify `ls` shows it. Linux smoke when phase 2 lands.

## Phase 2 design points (deferred, tracked)

- **Kernel pairing.** Which kernel boots which image, and whether per-image boot args ride along (boot args are hardcoded today at `pkg/firecracker/client.go:264`). The metadata hook exists from day one; the semantics need their own design pass.
- **Generation-based replacement** of registered images, if scoped scorched-earth proves too blunt.

## Non-Goals

- Registry pulls, auth, or any off-host image movement. Something else delivers artifacts to the host.
- A `--command`/entrypoint override.
- Automatic garbage collection of unused bases.
- Changing how the daemon binary itself deploys; this repo has no real-world deploy machinery, and this design adds none.

# Stockyard Image Contract

What an OCI image must provide to run as a stockyard task. Stockyard resolves
images strictly on-host — macOS against `container`'s image store, Linux
against stockyard's registered-image store (phase 2, PRI-2150) — and never
pulls from a registry.

## Minimal tier: the task runs

- **macOS (apple-container):** the image's ENTRYPOINT/CMD is PID 1 and must
  be self-sustaining — an init or equivalent long-running process (see
  `vm-image/init/stockyard-container-init.sh`). A bare language-runtime CMD
  (for example `node`, inherited from `node:*-slim` bases) exits immediately
  and the task dies at birth.
- **Linux (Firecracker):** the rootfs must contain a bootable `/sbin/init` —
  `stockyard-vm` ships systemd. Firecracker boot args pass no `init=`
  (`pkg/firecracker/client.go`), so the rootfs alone decides what runs.

There is deliberately no command override (`CreateTaskRequest` field 2 is
reserved — it was a command once, and it stays dead). If your image needs a
different entrypoint, build it into the image.

## Integrated tier: stockyard features work

Images that want `stockyard attach`, ssh access, or scp-based file transfer
additionally ship:

- **tailscaled** + `tailscale up` wiring (see the env contract below)
- **sshd**, with keys from `ssh_authorized_keys` honored
- **stockyard-shell** (built by `make build-guest`) for `stockyard attach`

`stockyard-vm` is the reference implementation of this tier on both platforms.

## Image families

One OCI name used on both platforms means the same *family*, not identical
bytes. `stockyard-vm` builds per-target stages from a shared Docker base
(`vm-image/Dockerfile`): the `firecracker` stage ships systemd, the
`container` stage ships the container init. Follow that pattern.

## Environment

The daemon injects configuration as environment variables (`container run
--env` on macOS; cloud-init on Linux), including `TAILSCALE_AUTH_KEY` when
tailscale is enabled. Integrated-tier inits must consume these; see
`vm-image/init/stockyard-container-init.sh` for the reference flow.

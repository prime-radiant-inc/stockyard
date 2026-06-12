# Config/CLI Cleanup — Design

- **Ticket:** [PRI-2177](https://linear.app/prime-radiant/issue/PRI-2177/configcli-cleanup-pass-dead-config-fields-stale-initconfigure-stale)
- **Date:** 2026-06-11
- **Status:** Draft, awaiting review

## Problem

`stockyard init` and `stockyard configure` are twins from the project's day-one scaffold (init: e065f7a, Jan 16 23:59; configure: ab818aa, two hours later) and neither has changed since. The daemon around them grew two more backends, fallback secrets, and an image registry. The drift, traced field-by-field in the 2026-06-11 audit:

- `init` and `configure` overlap (both set `instance_id` + `secrets.prefix`); configure additionally prompts for a secrets provider the daemon never reads — offering "aws", which was never implemented.
- `init`'s next-steps present 1Password vault setup as required. The daemon has used `FallbackProvider` (1Password → files in `secrets.dir`, default `/etc/stockyard/secrets`) for months (`cmd/stockyardd/main.go:39-46`), and a macOS `--no-tailscale` setup needs no secrets at all.
- `init` knows nothing about backends: macOS users must hand-edit `backend` and `apple_container.image`.
- Two config fields are dead: `secrets.provider` (never read) and `firecracker.vm_subnet` (never read; the real subnet is *derived* — `NewIPPoolFromGateway(cfg.Firecracker.VMGateway, 24)` hardcodes the /24, and `pkg/dashboard/server.go:480` separately hardcodes the display string `"10.0.100.0/24"`).
- CLAUDE.md documents root-Makefile deploy targets that no longer exist.

## Decisions

1. **One setup command.** `stockyard init` absorbs configure's job; `configure` is deleted. Flag-driven beats interactive for an internal tool, and `config.json` remains the escape hatch for everything else (socket path, DHCP ranges, …). Deleting configure also drops the `survey` dependency — configure.go is its only consumer.
2. **Dead fields removed, display derived.** `secrets.provider` and `firecracker.vm_subnet` leave the config struct and defaults. The dashboard derives its subnet display from `vm_gateway` + the same /24 the IP pool actually uses — one source of truth. (Per Matt: remove rather than honor; nobody has needed a non-/24.)
3. **Docs tell the real deploy story.** CLAUDE.md's phantom targets are replaced with what exists; vm-image READMEs are audited for the same drift; the two PRI-2150 semantic shifts are written down.

## The new `stockyard init`

```
stockyard init --instance <name> [--backend firecracker|apple-container] [--image <ref>]
```

- `--instance` (required, unchanged): sets `instance_id` and `secrets.prefix`. Keeps the existing overwrite warning.
- `--backend` (new): sets `backend` explicitly. Default is platform-aware — `apple-container` on darwin, `firecracker` on linux — and is always written to the config so the file says what the daemon will do.
- `--image` (new, apple-container only): seeds `apple_container.image`, the daemon's default image. Rejected with a clear error when combined with `--backend firecracker` (the Firecracker default image comes from `rootfs_path` seeding the registry's `default`, not from a ref).
- Next-steps output becomes truthful and per-backend:
  - **Secrets (both backends):** optional, not step one. Name the actual mechanism: the daemon tries 1Password (`op://Stockyard/<instance>/…`) and falls back to files in `/etc/stockyard/secrets` (`secrets.dir`); list the three secret names (`anthropic-api-key`, `github-token`, `tailscale-auth-key`) and say tasks run without them.
  - **apple-container:** load or pull an image with the `container` CLI, set/verify the default via `--image` or `stockyard image ls`, start `stockyardd`.
  - **firecracker:** build and register an image (`make -C vm-image deploy [REGISTRY_IMAGE=<name>]` → `stockyard image import`), start `stockyardd` (systemd unit on Linux hosts).
- `configure.go` and its tests are deleted; `survey` leaves go.mod.

The daemon's hard requirement is unchanged: it still refuses to start without `instance_id` and still points at `stockyard init`.

## Dead fields

- `SecretsConfig.Provider` — removed. `Vault`, `Prefix`, `Dir` stay (all are read by the providers).
- `FirecrackerConfig.VMSubnet` — removed, including the `DefaultConfig()` seed. `VMGateway`, `DHCPRange*`, `BridgeName` stay (all read at `pkg/daemon/daemon.go:145-161`).
- **No migration needed:** Go's JSON unmarshal ignores unknown keys, so existing config.json files with the dead fields keep loading; the keys disappear on the next `cfg.Save()`.
- **Dashboard:** replace the `"10.0.100.0/24"` literal with a value derived from the configured gateway masked to /24 (mirroring `NewIPPoolFromGateway`'s prefix). The displayed subnet then tracks reality if the gateway ever changes.

## Docs

- **CLAUDE.md:** drop `make deploy-daemon`/`deploy-image`/`deploy` (the root Makefile has neither); document the real flows — `make build`, `make test`, and image deployment via `make -C vm-image deploy [REGISTRY_IMAGE=<name>]`, which registers through `stockyard image import` with no daemon restart.
- **vm-image/README.md and vm-image/macos/README.md:** audit for the same drift (pre-registry deploy semantics, stale target names) and correct.
- **Semantic notes to capture** (in README/config docs, wherever config fields are documented): `firecracker.rootfs_path` now means "seed for the registry's `default` image" and still gates FC-backend construction; `apple_container.image` now means "default image, overridable per task" (PRI-2150).

## Testing

- `init`: update `init_test.go` for the new flags — platform default selection, `--image` seeding, the firecracker+`--image` rejection, and config contents after save.
- Config: update `config_test.go` (defaults no longer include removed fields; loading a config.json that still contains them succeeds).
- Dashboard: unit test the gateway→subnet derivation against the existing fake (`server_test.go` already fakes a gateway of `10.0.100.1`).
- Smoke before push: scratch-instance recipe — run the new `init` for both backend values, inspect config.json, start the daemon on the scratch socket, verify it comes up and `stockyard image ls` answers.

## Non-Goals

- CLI command grouping (PRI-2176) and image-name qualification (PRI-2178) — separate tickets.
- Any change to secrets *behavior* — the FallbackProvider order and error-swallowing stay as they are; this pass only makes the words match the code.
- New config surface. No flags beyond `--backend`/`--image`; everything else stays a config.json edit.

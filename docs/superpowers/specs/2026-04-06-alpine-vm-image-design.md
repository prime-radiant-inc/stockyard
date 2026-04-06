# Alpine VM Image for Faster Boot Times

**Date:** 2026-04-06
**Author:** Banzai (Bob 4357972c / Opus 4.6)
**Status:** Design

## Problem

The Stockyard VM lifecycle (Create -> SSH echo -> Destroy) takes ~6.5 seconds. Benchmarking shows:

| Phase | Time | Bottleneck |
|-------|------|-----------|
| Tailscale pre-registration | ~1,600ms | External API (cannot optimize) |
| Daemon-side (ZFS, TAP, Firecracker) | ~140ms | Already fast |
| Kernel + systemd to init script | ~300ms | Systemd overhead |
| Init script (non-Tailscale) | ~390ms | MMDS fetch, SSH keys, hooks |
| Tailscale reconnect (guest) | ~1,760ms | External API (cannot optimize) |
| ldconfig + remaining systemd | ~400ms | Systemd + glibc overhead |
| Tailscale DNS/routing convergence | ~1,900ms | Network convergence |

The two Tailscale round-trips (~3.4s) are external API latency we cannot reduce. The remaining ~3.1s includes ~1.1s of systemd and glibc overhead that Alpine eliminates.

## Solution

Replace the Ubuntu 24.04 VM image with Alpine Linux 3.21. Alpine uses musl libc (no ldconfig), OpenRC (lighter than systemd), and has a much smaller base footprint. The `gcompat` package provides glibc compatibility for binaries that need it (AWS CLI, Azure CLI, gcloud).

## Design Decisions

**Separate targets, not shared config.** The Ubuntu Dockerfile stays untouched. Alpine gets its own Dockerfile, init scripts, and OpenRC service files. They share the kernel config and the build/convert scripts.

**OpenRC as init system.** Alpine's default, well-supported, handles service dependencies. Faster than systemd for our workload. We don't need a custom PID 1 or exotic init like s6.

**gcompat for glibc binaries.** AWS CLI v2, Azure CLI, and gcloud ship glibc-linked binaries. Rather than pip-installing everything (slow, fragile), we install `gcompat` and use the official binary distributions. If a specific tool breaks, we fix it individually.

**Kernel unchanged.** The kernel is passed to Firecracker as a host-side file, not loaded from the rootfs. The existing custom 6.1.155 build with TUN + NF_TABLES continues to work regardless of the userspace distro.

**Init script ported to POSIX sh.** Alpine's default shell is ash (busybox). The stockyard-init script gets ported from bash to POSIX sh, avoiding bashisms. The logic stays the same.

## File Layout

```
vm-image/
  Dockerfile                    # Ubuntu (existing, unchanged)
  Dockerfile.alpine             # Alpine (new)
  build.sh                      # Add VARIANT=alpine support
  convert-to-rootfs.sh          # Unchanged (just exports a container)
  kernel.config                 # Shared
  init/
    stockyard-init.sh           # Existing (bash/systemd, unchanged)
    stockyard-init-alpine.sh    # New (POSIX sh, OpenRC)
    stockyard-shell.service     # Existing systemd unit (unchanged)
    stockyard-shell.initd       # New OpenRC init script
    llm-proxy.service           # Existing systemd unit (unchanged)
    llm-proxy.initd             # New OpenRC init script
    stockyard-init.service      # Existing (unchanged)
    hosts                       # Shared
```

## Makefile Targets

```makefile
build-image:          # Ubuntu (existing, unchanged)
build-image-alpine:   # Alpine (new)
deploy-image:         # Ubuntu (existing, unchanged)
deploy-image-alpine:  # Alpine (new)
```

## Dockerfile.alpine

### Section 1: Base System

```dockerfile
FROM alpine:3.21

ARG VM_USER=mooby

RUN apk add --no-cache \
    bash curl wget git jq unzip gnupg ca-certificates \
    openssh iproute2 iptables bind-tools iputils \
    sudo vim less tmux bc rsync ripgrep \
    build-base pkgconf cmake clang clang-extra-tools \
    gcompat \
    openrc
```

Key differences from Ubuntu:
- `gcompat` for glibc compatibility
- `openrc` explicit (though it's in the base)
- `openssh` (not `openssh-server`) includes both client and server
- `build-base` replaces `build-essential`
- No `cloud-init` (already masked in Ubuntu, stockyard-init handles everything)
- No `software-properties-common` or `apt-transport-https` (apt-specific)

### Section 2: Programming Languages

- **Python:** `apk add python3 py3-pip python3-dev`
- **uv:** Same `COPY --from=ghcr.io/astral-sh/uv:latest` pattern
- **Node.js 24:** Use official Linux binary tarball (Alpine `nodejs` package may lag). The official tarball includes musl builds.
- **Go 1.26:** Same tarball install (Go is statically linked, works everywhere)
- **Rust:** Same rustup install. Rustup detects musl and installs the `x86_64-unknown-linux-musl` target.

### Section 3: Developer Tools

- **GitHub CLI:** `apk add github-cli` from community repo
- **AWS CLI v2:** Official bundled installer + gcompat. Falls back to `pip install awscli` if the bundled installer fails.
- **Azure CLI:** `pip install azure-cli` (the official script doesn't support Alpine natively)
- **gcloud CLI:** Tarball + python3 (same approach as Ubuntu)
- **Tailscale:** `apk add tailscale` from community, or static binary from releases
- **dotenvx:** Same curl install
- **llm-proxy:** Same binary install (static Go binary)
- **yq:** `apk add yq` from community repo
- **golangci-lint, ruff, eslint, prettier, typescript:** Same install methods

### Section 4: User Setup

```dockerfile
RUN adduser -D -s /bin/bash -G wheel ${VM_USER} \
    && echo "${VM_USER} ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers.d/${VM_USER} \
    && chmod 0440 /etc/sudoers.d/${VM_USER}
```

Rust user install, Claude Code, Codex CLI: same as Ubuntu.

### Section 5: SSH Configuration

Same sshd_config changes. No `ssh.socket` (that's systemd socket activation). SSH runs as a regular OpenRC service started at boot.

### Section 6: OpenRC Services

Three init scripts replace systemd units:

**stockyard-shell** (`/etc/init.d/stockyard-shell`):
```sh
#!/sbin/openrc-run
description="Stockyard Shell Service (vsock terminal access)"
command="/usr/local/bin/stockyard-shell"
command_background=true
pidfile="/run/stockyard-shell.pid"
depend() {
    need net
}
```

**llm-proxy** (`/etc/init.d/llm-proxy`):
```sh
#!/sbin/openrc-run
description="LLM API Logging Proxy"
command="/usr/local/bin/llm-proxy"
command_background=true
pidfile="/run/llm-proxy.pid"
```

**stockyard-init** (`/etc/init.d/stockyard-init`):
```sh
#!/sbin/openrc-run
description="Stockyard VM Initialization"
command="/usr/local/bin/stockyard-init-alpine.sh"
command_background=false
depend() {
    need net
    before sshd
}
```

All three added to the default runlevel: `rc-update add <service> default`

### Section 7: Networking

No systemd-networkd. The kernel boot args provide the static IP (`ip=...`). The init script handles:
- DNS: writes `/etc/resolv.conf` directly
- MMDS route: `ip route add 169.254.169.254/32 dev eth0 scope link`

Alpine's networking stack uses `/etc/network/interfaces` but we don't need it — the kernel handles the IP and the init script handles DNS/routes.

### Section 8: Disable Unnecessary Services

Alpine starts very little by default. Disable:
- `crond` (not needed for ephemeral VMs)
- `acpid` (not needed)

No ldconfig (musl doesn't use it). No modprobe services. No systemd-journald, systemd-logind, systemd-tmpfiles, etc.

### Section 9: Kernel Build

Same as Ubuntu, with Alpine build dependencies:

```dockerfile
RUN apk add --no-cache \
    ncurses-dev flex bison openssl-dev elfutils-dev bc perl linux-headers
```

Same `make olddefconfig && make -j$(nproc) vmlinux` flow.

## stockyard-init-alpine.sh

Port of `stockyard-init.sh` from bash to POSIX sh. Changes:

| Bash | POSIX sh |
|------|----------|
| `#!/bin/bash` | `#!/bin/sh` |
| `{1..30}` | `seq 1 30` |
| `$((RANDOM))` | not used (remove or use /dev/urandom) |
| `date +%s.%N` | `date +%s` (Alpine date lacks nanoseconds without coreutils) |
| `set -e` | `set -e` (same) |

Logic is identical: check network, fetch MMDS, install SSH keys, start Tailscale with pre-registered state (or auth key fallback), set up workspace, configure Claude hooks.

For timing instrumentation, install `coreutils` to get nanosecond-precision `date`, or accept second-level granularity.

## Build Pipeline

### build.sh changes

Add a `VARIANT` parameter:

```bash
VARIANT=${VARIANT:-ubuntu}  # "ubuntu" or "alpine"

if [ "$VARIANT" = "alpine" ]; then
    DOCKERFILE="Dockerfile.alpine"
    TAG="stockyard-vm:alpine"
else
    DOCKERFILE="Dockerfile"
    TAG="stockyard-vm:latest"
fi

docker build -f "$DOCKERFILE" -t "$TAG" .
```

### convert-to-rootfs.sh

Should work unchanged. It creates a container from the image, exports the filesystem, writes it to an ext4 file, and extracts the kernel. The container's contents are different (Alpine vs Ubuntu) but the export process is the same.

### vm-image/Makefile

Add Alpine targets alongside existing ones:

```makefile
build-alpine:
    VARIANT=alpine ./build.sh

deploy-alpine: build-alpine
    ./convert-to-rootfs.sh stockyard-vm:alpine
```

## Expected Performance

### Boot timeline (estimated)

```
0ms     Kernel starts
~50ms   OpenRC starts (vs ~130ms for systemd)
~100ms  Networking ready (kernel-configured)
~120ms  sshd started
~150ms  stockyard-init starts
~200ms  MMDS fetched, SSH keys installed
~1,950ms Tailscale reconnected
~2,000ms Init complete
```

### Lifecycle comparison (estimated)

| Phase | Ubuntu | Alpine (est.) | Savings |
|-------|--------|--------------|---------|
| Create (API) | 1,715ms | 1,715ms | 0ms |
| Kernel to init | 300ms | 100ms | 200ms |
| Init (non-TS) | 390ms | 200ms | 190ms |
| Tailscale reconnect | 1,760ms | 1,760ms | 0ms |
| Post-init systemd | 400ms | 50ms | 350ms |
| TS convergence | 1,900ms | 1,900ms | 0ms |
| **Total** | **~6,500ms** | **~5,750ms** | **~750ms** |

Conservative estimate: ~750ms improvement. The main wins are eliminating ldconfig (131ms), systemd overhead (200ms+), and modprobe services.

### Image size

Ubuntu rootfs: ~10GB. Alpine rootfs: estimated ~3-4GB (same dev tools but smaller base and no glibc/systemd bloat). Faster ZFS import on daemon restart.

## Testing

1. Build the Alpine image
2. Deploy to stockyard-ip-10-50-1-107
3. Run the same benchmark script (3 runs)
4. Compare lifecycle times
5. Verify: SSH works, Tailscale works, stockyard-shell works, agent tools (Claude Code, Go, Node, Python, Rust) work
6. Verify: cloud CLIs (aws, az, gcloud) work with gcompat

## Risks

- **gcompat edge cases:** Some glibc binaries may not work with gcompat. Mitigation: test each tool, fall back to pip installs for tools that break.
- **musl DNS quirks:** musl's DNS resolver behaves slightly differently from glibc (no `/etc/nsswitch.conf`, different search order). Mitigation: we control `/etc/resolv.conf` directly.
- **Alpine package freshness:** Some packages in Alpine repos may lag behind Ubuntu. Mitigation: we install most tools from upstream tarballs/scripts anyway.
- **Node.js native addons:** npm packages with native code compile against musl. Most work fine but some assume glibc. Mitigation: `gcompat` + test Claude Code and Codex.

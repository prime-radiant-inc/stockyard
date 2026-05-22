# Stockyard on macOS

Run stockyard VMs on macOS (Apple Silicon). Two backends are available, both
built on Apple's Virtualization.framework:

- **vfkit** — boots a full Linux VM (Alpine + a Kata kernel) via
  [vfkit](https://github.com/crc-org/vfkit). Covered by the sections below.
- **Apple `container`** — runs each task as an Apple
  [`container`](https://github.com/apple/container) from a normal OCI image,
  with no separate kernel or rootfs to build. Requires macOS 26+. See
  [Apple `container` backend](#apple-container-backend) at the end of this file.

## Requirements

- An **Apple Silicon** Mac (arm64).
- `brew install vfkit e2fsprogs` — `e2fsprogs` is keg-only; the build scripts
  locate `mkfs.ext4` via `brew --prefix`, so it does not need to be on `PATH`.
- **Docker** — [OrbStack](https://orbstack.dev/) or Docker Desktop. Build-time
  only, used to assemble the Alpine rootfs.
- **Go** — to build stockyard itself.

## 1. Build stockyard

From the repo root:

```bash
make build
```

This produces the host binaries `bin/stockyard` and `bin/stockyardd` (plus the
Linux guest binaries). Put `bin/` on your `PATH`, or invoke the binaries
directly — the examples below assume `stockyard` and `stockyardd` are on `PATH`.

## 2. Build the VM image

```bash
cd vm-image/macos
./setup.sh
```

`setup.sh` downloads the Kata Containers arm64 kernel and builds the Alpine
rootfs. Output lands in `vm-image/macos/output/`:

- `vmlinux` — Kata arm64 kernel (~13 MB, virtio built-in, direct boot)
- `alpine-rootfs.raw` — Alpine ext4 rootfs (~380 MB)

`setup.sh` is idempotent — it skips any step whose output already exists. After
editing the image (`Dockerfile.alpine` or the `overlay/`), rebuild just the
rootfs with `./build-rootfs.sh`.

## 3. Configure

stockyard reads `~/.config/stockyard/config.json`. Create that file with the
vfkit configuration below.

> **Do not run `stockyard init` on macOS.** It writes Firecracker/ZFS/1Password
> defaults with `/var/...` paths that require root. Write the config directly.

> **All paths must be absolute.** The daemon does not expand `~`.

First create the data directory (the file secrets provider does not create its
own directory):

```bash
mkdir -p ~/.local/share/stockyard/secrets
```

Then write `~/.config/stockyard/config.json` — replace `/Users/you` and the
repo path with your real absolute paths:

```json
{
  "instance_id": "my-mac",
  "backend": "vfkit",
  "vm": { "user": "stockyard" },
  "vfkit": {
    "vfkit_bin": "/opt/homebrew/bin/vfkit",
    "kernel_path": "/Users/you/code/stockyard/vm-image/macos/output/vmlinux"
  },
  "rootfs": {
    "provider": "apfs",
    "base_image": "/Users/you/code/stockyard/vm-image/macos/output/alpine-rootfs.raw",
    "vms_dir": "/Users/you/.local/share/stockyard/vms"
  },
  "secrets": {
    "provider": "file",
    "dir": "/Users/you/.local/share/stockyard/secrets"
  },
  "daemon": {
    "socket_path": "/Users/you/.local/share/stockyard/stockyard.sock",
    "data_dir": "/Users/you/.local/share/stockyard"
  }
}
```

Field notes:

- **`backend: vfkit`** — required. It selects the macOS hypervisor path; an
  empty or `firecracker` value makes the daemon try ZFS and a DHCP server,
  which do not exist on macOS.
- **`vm.user: stockyard`** — the only account in the Alpine image, and what you
  SSH in as. It has passwordless `sudo`.
- **`vfkit.kernel_path` / `rootfs.base_image`** — point at the two files
  produced in step 2.
- **`vfkit.vfkit_bin`** — optional; omit it to use `vfkit` from `PATH`.
- **`daemon.socket_path` / `data_dir`** — keep these under your home directory.
  The built-in defaults (`/var/run/stockyard`, `/var/lib/stockyard`) require
  root.
- **`secrets.dir`** — where the file secrets provider looks. Drop plaintext
  secret files here (e.g. a file named `anthropic-api-key`) if your VM
  workloads need them. Not required just to boot a VM.

## 4. Run

```bash
stockyardd &                               # start the daemon
stockyard run --no-tailscale --name test   # create a VM — prints a task id
stockyard attach <task-id>                 # open an interactive shell in it
stockyard list                             # list VMs
stockyard destroy -f <task-id>             # tear it down
```

- `stockyardd` reads `~/.config/stockyard/config.json` on startup.
- Pass **`--no-tailscale`** on macOS — the vfkit path connects over direct VM
  IPs, not Tailscale.
- `stockyard attach` opens an SSH session as the `stockyard` user. To connect
  by hand, `ssh stockyard@<vm-ip>` with a key from your `~/.ssh/*.pub`
  (`stockyard run` injects all of them into the VM).

## Known limitations

- **`stockyard exec` / the command queue does not work on vfkit.** The
  in-guest agent runs, but the daemon-side executor speaks Firecracker's
  vsock-proxy protocol, which vfkit does not use. Run commands over SSH
  instead. (Tracked internally as PRI-1682.)

## Architecture

```
macOS host
  └── stockyardd (daemon)
        └── vfkit (one process per VM)
              └── Virtualization.framework
                    └── Alpine Linux arm64 VM
                          ├── Kata kernel (virtio built-in, ip=dhcp)
                          ├── OpenRC (sysinit + boot + default runlevels)
                          ├── sshd (pre-baked host keys)
                          └── VirtioFS mount (/mnt/stockyard → host shared dir)
```

### How it works

1. **Create:** APFS `clonefile()` copies the base rootfs image (instant,
   copy-on-write).
2. **Boot:** vfkit spawns with the Kata kernel via direct boot (no firmware).
3. **Network:** the kernel gets an IP via DHCP at ~0.2s (built-in DHCP client,
   vmnet NAT).
4. **SSH keys:** injected via a VirtioFS shared directory — the host writes
   `authorized_keys`, the guest mounts it and sshd reads keys from there.
5. **SSH ready:** ~1.2s after vfkit launch.
6. **Destroy:** SIGKILL the vfkit process and remove the rootfs clone (~30ms).

### Performance

| Metric | Time |
|--------|------|
| `stockyard run` (create task) | ~0.7s |
| SSH ready (from vfkit launch) | ~1.2s |
| **Total to SSH Hello World** | **~2.0s** |
| Destroy | ~0.03s |
| Full lifecycle (create + command + destroy) | ~3s |

### Differences from Linux (Firecracker)

| | Linux (Firecracker) | macOS (vfkit) |
|--|---------------------|---------------|
| Hypervisor | Firecracker (KVM) | vfkit (Virtualization.framework) |
| Rootfs provisioning | ZFS clone | APFS clonefile |
| Networking | TAP + bridge + dnsmasq | vmnet NAT (built-in DHCP) |
| SSH access | Tailscale hostname | Direct IP |
| Metadata delivery | MMDS (cloud-init) | VirtioFS shared directory |
| Guest OS | Ubuntu (custom image) | Alpine Linux |
| Kernel | Custom x86_64 | Kata arm64 |

## Customizing the image

Edit `Dockerfile.alpine` (or the `overlay/` tree) to add packages or change
configuration, then rebuild the rootfs:

```bash
./build-rootfs.sh
```

The rootfs is built by:

1. Docker builds an Alpine arm64 image with all packages.
2. `docker export` extracts the filesystem.
3. `.dockerenv` is removed so OpenRC does not run in container-degraded mode.
4. `mkfs.ext4 -d` creates an ext4 image from the directory.

Because `mkfs.ext4 -d` runs unprivileged on macOS, it cannot preserve root
ownership or setuid bits. The image compensates at build and boot time — see
the notes below.

### Key files in the image

- `overlay/etc/ssh/sshd_config.d/stockyard.conf` — sshd reads `authorized_keys`
  from the VirtioFS mount.
- `overlay/etc/init.d/stockyard-mount` — OpenRC service (runlevel `boot`):
  mounts the VirtioFS share and repairs ownership/permissions that
  `mkfs.ext4 -d` cannot preserve (sshd host keys, `sudo`).
- `overlay/etc/init.d/stockyard-shell` — OpenRC service (runlevel `default`):
  the in-guest vsock command agent.
- `Dockerfile.alpine` — also populates the OpenRC `sysinit` runlevel
  (`devfs`, `sysfs`); a docker-exported rootfs starts with empty runlevels,
  so without this `/dev/pts`, `/dev/shm` and `/sys` would be unmounted.

# Apple `container` backend

The `apple-container` backend runs each task as an Apple
[`container`](https://github.com/apple/container) — one lightweight VM per
container — instead of a vfkit VM. It consumes a normal OCI image, so there is
no separate kernel or rootfs to build. This backend is **additive**: it does not
affect the vfkit or Firecracker paths.

## Requirements

- **macOS 26 (Tahoe) or later**, Apple Silicon. Earlier macOS is not supported.
- Apple's `container` tool, installed and running:
  ```bash
  brew install container
  container system start
  container system status     # should report: status running
  ```
  `container` ships as a signed `.pkg` that installs a launchd service, so it is
  not a clean fit for `mise` — use `brew` (or the `.pkg` directly).
- **Docker** (OrbStack or Docker Desktop) — to build the image (see the note
  under [Build the image](#build-the-image)).
- **Go** — to build stockyard.

## 1. Build stockyard

```bash
make build
```

Same as the vfkit path — produces `bin/stockyard` and `bin/stockyardd`.

## 2. Build the image

The `apple-container` backend runs an OCI image that must live in `container`'s
own image store. Build it with Docker, then load it:

```bash
cd vm-image
make container-image                              # docker build of the container target (arm64)
docker save stockyard-vm:container -o /tmp/sy.tar
container image load -i /tmp/sy.tar
rm /tmp/sy.tar
container image ls                                # stockyard-vm:container should be listed
```

> **Why the Docker round-trip?** `container build` *should* build the image
> directly into `container`'s store, but on `container` 0.12.x it fails on this
> multi-stage Dockerfile (`"Stream unexpectedly closed"`). Until that is fixed,
> build with Docker and `container image load` the result. Tracked in PRI-1755.

## 3. Configure

stockyard reads `~/.config/stockyard/config.json`. Create the secrets directory
and write the config — replace `/Users/you` with your real absolute path:

```bash
mkdir -p ~/.local/share/stockyard/secrets
```

```json
{
  "instance_id": "my-mac",
  "backend": "apple-container",
  "vm": { "user": "mooby" },
  "apple_container": {
    "image": "stockyard-vm:container"
  },
  "secrets": {
    "provider": "file",
    "dir": "/Users/you/.local/share/stockyard/secrets"
  },
  "daemon": {
    "socket_path": "/Users/you/.local/share/stockyard/stockyard.sock",
    "data_dir": "/Users/you/.local/share/stockyard"
  }
}
```

Field notes:

- **`backend: apple-container`** — selects this backend.
- **`apple_container.image`** — the image loaded in step 2. **Required** — the
  daemon refuses to start without it.
- **`vm.user: mooby`** — the account baked into the image; `stockyard attach`
  opens the shell as this user (`container exec -u mooby`). It must exist in
  the image.
- No `vfkit` or `rootfs` block is needed — `container` owns the kernel and
  rootfs.
- **All paths absolute** — the daemon does not expand `~`. Keep `socket_path` /
  `data_dir` under your home directory; the `/var/...` defaults require root.
- Do **not** run `stockyard init` — it writes Firecracker/ZFS defaults. Write
  the config directly, as above.

## 4. Run

```bash
stockyardd &                                # start the daemon
stockyard run --no-tailscale --name dev     # create a task — prints a task id
stockyard attach <task-id>                  # interactive shell (via container exec)
stockyard list
stockyard destroy -f <task-id>              # tear it down
```

- **Restart `stockyardd` after any config change.** The daemon reads the config
  — including `apple_container.image` — once at startup and never reloads it.
  Change the image and you must restart the daemon, or tasks keep using the old
  one.
- Pass **`--no-tailscale`** unless you have dropped a `tailscale-auth-key` file
  in the secrets directory. With a key, the container joins your tailnet
  (userspace mode, Tailscale SSH) and is reachable by its MagicDNS name.
- `stockyard attach` runs `container exec -t -i -u <vm.user> … /bin/bash` — the
  image must have that user and `/bin/bash`. The project image has both; a bare
  image (e.g. plain `alpine`) does not, and `attach` will fail.

## Notes and limitations

- **macOS 26+ only.**
- No ZFS snapshots and no per-task VM metrics on this backend (as with vfkit).
- `stockyard exec` / the command queue is not wired up (as with vfkit) — use
  `stockyard attach`, or `container exec` directly against the container named
  `stockyard-<vmID>`.
- vfkit and Firecracker are unaffected by this backend — `backend` in the
  config chooses which one runs.

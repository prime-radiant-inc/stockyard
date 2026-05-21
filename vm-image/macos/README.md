# Stockyard on macOS

Run stockyard VMs on macOS (Apple Silicon) using Apple's Virtualization.framework
via [vfkit](https://github.com/crc-org/vfkit).

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

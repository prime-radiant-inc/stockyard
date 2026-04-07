# Fast Boot Alpine Rootfs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sub-second TTFB (time to first SSH byte) for stockyard VMs on macOS by switching from Ubuntu + cloud-init + DHCP to Alpine + Kata kernel + static IP + VirtioFS key injection.

**Architecture:** Build a minimal Alpine arm64 rootfs image via Docker, boot it with the Kata kernel (virtio built-in, no initrd), assign static IPs via kernel cmdline, inject SSH authorized_keys via VirtioFS shared directory. No cloud-init, no DHCP, no initrd.

**Tech Stack:** Alpine Linux, Docker (for image build), Kata kernel, vfkit, e2fsprogs (Homebrew)

---

## File Structure

### New files
- `vm-image/macos/Dockerfile.alpine` — Multi-stage Docker build for Alpine rootfs
- `vm-image/macos/build-rootfs.sh` — Script to build rootfs from Docker image
- `vm-image/macos/overlay/etc/init.d/stockyard-mount` — OpenRC init script to mount VirtioFS
- `vm-image/macos/overlay/etc/ssh/sshd_config.d/stockyard.conf` — sshd config for VirtioFS keys

### Modified files
- `pkg/vmbackend/vfkit.go` — Static IP, VirtioFS shared dir, Kata kernel, no cloud-init, no initrd
- `pkg/vmbackend/vfkit_test.go` — Update tests
- `pkg/config/vfkit.go` — Remove InitrdPath, add defaults
- `pkg/daemon/backend_darwin.go` — Update config mapping
- `vm-image/macos/setup.sh` — Download Kata kernel instead of Ubuntu kernel+initrd, run build-rootfs.sh

---

### Task 1: Build Alpine rootfs image

**Files:**
- Create: `vm-image/macos/Dockerfile.alpine`
- Create: `vm-image/macos/build-rootfs.sh`
- Create: `vm-image/macos/overlay/etc/init.d/stockyard-mount`
- Create: `vm-image/macos/overlay/etc/ssh/sshd_config.d/stockyard.conf`

- [ ] **Step 1: Create the sshd config overlay**

```
# vm-image/macos/overlay/etc/ssh/sshd_config.d/stockyard.conf
AuthorizedKeysFile /mnt/stockyard/authorized_keys .ssh/authorized_keys
PermitRootLogin no
PasswordAuthentication no
```

- [ ] **Step 2: Create the OpenRC init script for VirtioFS mount**

```sh
#!/sbin/openrc-run
# vm-image/macos/overlay/etc/init.d/stockyard-mount

description="Mount stockyard VirtioFS share"

depend() {
    before sshd
}

start() {
    ebegin "Mounting stockyard VirtioFS"
    mkdir -p /mnt/stockyard
    mount -t virtiofs stockyard /mnt/stockyard 2>/dev/null || true
    eend 0
}

stop() {
    ebegin "Unmounting stockyard VirtioFS"
    umount /mnt/stockyard 2>/dev/null || true
    eend 0
}
```

- [ ] **Step 3: Create the Dockerfile**

```dockerfile
# vm-image/macos/Dockerfile.alpine
FROM --platform=linux/arm64 alpine:3.21

# Install essentials
RUN apk add --no-cache \
    openssh-server \
    rsync \
    ripgrep \
    curl \
    git \
    bash \
    sudo

# Install development tools
RUN apk add --no-cache \
    nodejs \
    npm \
    go

# Create stockyard user with sudo
RUN adduser -D -s /bin/bash stockyard && \
    echo 'stockyard ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/stockyard

# Generate SSH host keys (baked into image — instant sshd start)
RUN ssh-keygen -A

# Configure sshd for VirtioFS key injection
COPY overlay/etc/ssh/sshd_config.d/stockyard.conf /etc/ssh/sshd_config.d/

# Install VirtioFS mount init script
COPY overlay/etc/init.d/stockyard-mount /etc/init.d/stockyard-mount
RUN chmod +x /etc/init.d/stockyard-mount

# Enable services at boot
RUN rc-update add sshd default && \
    rc-update add stockyard-mount default

# Configure networking for static IP (will be overridden by kernel cmdline)
RUN echo 'auto eth0' > /etc/network/interfaces && \
    echo 'iface eth0 inet manual' >> /etc/network/interfaces

# Set hostname template
RUN echo 'stockyard' > /etc/hostname

# Ensure /sbin/init exists (OpenRC)
RUN ln -sf /sbin/openrc-init /sbin/init || true
```

- [ ] **Step 4: Create the build script**

```bash
#!/bin/bash
# vm-image/macos/build-rootfs.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT_DIR="${1:-$SCRIPT_DIR/output}"
IMAGE_NAME="stockyard-alpine-vm"
ROOTFS_SIZE="${ROOTFS_SIZE:-4G}"

GREEN='\033[0;32m'
NC='\033[0m'
info() { echo -e "${GREEN}==>${NC} $*"; }

# Check prereqs
command -v docker >/dev/null 2>&1 || { echo "docker required"; exit 1; }
brew list e2fsprogs >/dev/null 2>&1 || { echo "e2fsprogs required: brew install e2fsprogs"; exit 1; }

mkdir -p "$OUTPUT_DIR"

# Build Docker image
info "Building Alpine VM image..."
docker build --platform linux/arm64 -t "$IMAGE_NAME" -f "$SCRIPT_DIR/Dockerfile.alpine" "$SCRIPT_DIR"

# Export filesystem
info "Exporting filesystem..."
CONTAINER_ID=$(docker create --platform linux/arm64 "$IMAGE_NAME")
docker export "$CONTAINER_ID" > "$OUTPUT_DIR/rootfs.tar"
docker rm "$CONTAINER_ID" >/dev/null

# Create ext4 image from tarball
info "Creating ext4 image (${ROOTFS_SIZE})..."
ROOTFS_PATH="$OUTPUT_DIR/alpine-rootfs.raw"

# Create empty image
truncate -s "$ROOTFS_SIZE" "$ROOTFS_PATH"

# Format as ext4 and populate from tarball
# Extract tar first, then use mkfs.ext4 -d
TMPDIR=$(mktemp -d)
tar xf "$OUTPUT_DIR/rootfs.tar" -C "$TMPDIR"

# Ensure critical directories exist
mkdir -p "$TMPDIR"/{dev,proc,sys,run,tmp,mnt/stockyard}
chmod 1777 "$TMPDIR/tmp"

# Use Homebrew's mkfs.ext4 (keg-only, need full path)
MKFS="$(brew --prefix e2fsprogs)/sbin/mkfs.ext4"
"$MKFS" -d "$TMPDIR" -L alpine-root "$ROOTFS_PATH" "${ROOTFS_SIZE}"

# Cleanup
rm -rf "$TMPDIR" "$OUTPUT_DIR/rootfs.tar"

info "Rootfs: $ROOTFS_PATH ($(du -h "$ROOTFS_PATH" | cut -f1))"
```

- [ ] **Step 5: Install e2fsprogs if needed and run the build**

Run: `brew install e2fsprogs` (if not installed)
Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && chmod +x vm-image/macos/build-rootfs.sh && ./vm-image/macos/build-rootfs.sh`

Verify: `file vm-image/macos/output/alpine-rootfs.raw` should show ext4 filesystem.

- [ ] **Step 6: Commit**

```bash
git add vm-image/macos/Dockerfile.alpine vm-image/macos/build-rootfs.sh vm-image/macos/overlay/
git commit -m "feat: add Alpine arm64 rootfs build for fast-boot macOS VMs"
```

---

### Task 2: Update vfkit backend for fast boot

**Files:**
- Modify: `pkg/vmbackend/vfkit.go`
- Modify: `pkg/vmbackend/vfkit_test.go`
- Modify: `pkg/config/vfkit.go`
- Modify: `pkg/daemon/backend_darwin.go`

Changes to the vfkit backend:

1. **Remove initrd** — Kata kernel has virtio built-in, Alpine rootfs has init
2. **Add VirtioFS** — shared directory for SSH authorized_keys
3. **Static IP** — via kernel cmdline, simple counter-based allocation
4. **Remove cloud-init** — no more writing user-data/meta-data files
5. **Remove DHCP lease polling** — IP is known at create time (we assigned it)

- [ ] **Step 1: Update VfkitConfig**

In `pkg/config/vfkit.go`:

```go
type VfkitConfig struct {
    VfkitBin   string `json:"vfkit_bin"`
    KernelPath string `json:"kernel_path"`
    RootfsPath string `json:"rootfs_path"`
}
```

Remove `InitrdPath` — no longer needed.

- [ ] **Step 2: Update backend_darwin.go**

Remove `InitrdPath` from the config mapping.

- [ ] **Step 3: Rewrite vfkit.go buildArgs and CreateVM**

The new `buildArgs` builds a command like:
```
vfkit --cpus 2 --memory 2048 \
  --kernel /path/vmlinux \
  --kernel-cmdline "console=hvc0 root=/dev/vda rw ip=192.168.64.X::192.168.64.1:255.255.255.0::eth0:off" \
  --device virtio-blk,path=/path/rootfs.raw \
  --device virtio-net,nat,mac=02:xx:xx:xx:xx:xx \
  --device virtio-rng \
  --device virtio-serial,logFilePath=/path/console.log \
  --device virtio-fs,sharedDir=/path/shared,mountTag=stockyard \
  --restful-uri unix:///path/rest.sock
```

Note: no `--initrd`, no `--cloud-init`. IP is in the kernel cmdline. VirtioFS mounts the shared dir.

The new `CreateVM`:
1. Clone rootfs (already done by caller via provisioner)
2. Create shared directory, write authorized_keys into it
3. Allocate static IP (simple counter: 192.168.64.2, .3, .4, ...)
4. Build args, spawn vfkit
5. Return immediately with the known IP (no polling needed!)

The IP allocator is a simple atomic counter starting at 2 (gateway is .1). When a VM is deleted, its IP is released. For now, a simple incrementing counter is fine — we won't run 252 VMs.

- [ ] **Step 4: Remove waitForIP, writeCloudInit, DHCP lease dependency**

Delete the `waitForIP` and `writeCloudInit` functions. The `FindIPByMAC`/`FindIPByName` functions stay (useful for fallback/debugging) but are no longer in the hot path.

- [ ] **Step 5: Update tests**

Update `TestVfkitBackend_BuildArgs` to check for:
- `--kernel` present, `--initrd` absent
- `virtio-fs` present with `mountTag=stockyard`
- `--kernel-cmdline` contains `ip=192.168.64.`
- No `--cloud-init`

Update `TestWriteCloudInit` → replace with `TestWriteAuthorizedKeys` that verifies the shared directory gets the keys file.

- [ ] **Step 6: Run tests and verify**

Run: `go test ./pkg/vmbackend/ -v`
Run: `go test ./pkg/... -count=1`
Run: `make build`

- [ ] **Step 7: Commit**

```bash
git add pkg/vmbackend/vfkit.go pkg/vmbackend/vfkit_test.go pkg/config/vfkit.go pkg/daemon/backend_darwin.go
git commit -m "feat: fast-boot vfkit backend — static IP, VirtioFS keys, no initrd/cloud-init"
```

---

### Task 3: Update setup.sh and end-to-end test

**Files:**
- Modify: `vm-image/macos/setup.sh`

- [ ] **Step 1: Update setup.sh**

The setup script should now:
1. Download the Kata kernel (not Ubuntu kernel + initrd)
2. Run build-rootfs.sh to build the Alpine image
3. Print updated config

Remove the Ubuntu kernel/initrd download. Add the Kata kernel download (already implemented in the original setup.sh). Add a call to build-rootfs.sh.

- [ ] **Step 2: Run setup.sh to rebuild everything**

Run: `cd /Users/mw/Code/prime/stockyard/.worktrees/vm-backend && ./vm-image/macos/setup.sh`

- [ ] **Step 3: Update test config and time it**

Update `/tmp/stockyard-test/config.json` to point at the new kernel + rootfs. Start daemon. Run timing test:

```bash
# Time: create → SSH "Hello World" → destroy
time stockyard run --no-tailscale --name test
# then SSH and time to first response
```

Target: <1s from `stockyard run` to SSH ready.

- [ ] **Step 4: Commit**

```bash
git add vm-image/macos/setup.sh
git commit -m "feat: setup.sh uses Kata kernel + Alpine rootfs for fast boot"
```

---

### Task 4: Final timing and verification

- [ ] **Step 1: Full test suite**

Run: `go test ./pkg/... -count=1` — all pass

- [ ] **Step 2: Build all binaries**

Run: `make build` — success

- [ ] **Step 3: Timing test**

Run the full lifecycle with timing. Target numbers:

| Step | Target |
|------|--------|
| CREATE (clone + spawn vfkit) | <500ms |
| SSH READY | <500ms after create |
| TOTAL to Hello World | <1s |
| DESTROY | <500ms |

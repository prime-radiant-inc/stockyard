# stockyard.local Image Qualification + `image ls` De-noising Implementation Plan (PRI-2178)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stockyard-built OCI images carry real provenance as `stockyard.local/<name>:<tag>` (e.g. `stockyard.local/stockyard-vm:container`), and `stockyard image ls` stops displaying containerd's `docker.io/library/` normalization noise.

**Architecture:** Two independent, small changes. (1) Build-time: the `container`-target branch of `vm-image/build.sh` tags as `stockyard.local/${IMAGE_NAME}:container` — the Firecracker/rootfs pipeline is deliberately untouched (its Docker tag is consumed locally by `convert-to-rootfs.sh`, and the Linux registry names images at `stockyard image import` time). (2) Display-time: a pure helper `displayRef` in `cmd/stockyard/image.go`, next to the existing `shortDigest`, strips a leading `docker.io/library/` (and a bare leading `docker.io/`) in the `image ls` table only — matching what Docker's CLI and Apple's own `container image ls` table do. Stored refs stay canonical; `--image` accepts short or qualified forms unchanged.

**Tech Stack:** Go 1.25 (cobra CLI, stdlib `strings`/`testing`), bash build script, Apple `container` CLI (macOS smoke).

**Scope guardrails (decisions already made — do not relitigate):**
- macOS/apple-container only. No proto, daemon, resolution, or Linux-registry changes.
- `vm-image/convert-to-rootfs.sh` and the `firecracker` build branch are NOT qualified.
- prudence-vm (built in the Prudence repo) adopting the qualification is OUT of scope — tracked as a PRI-2063 follow-up.
- De-noising happens in `cmd/stockyard/image.go` at display time only. The dashboard and daemon error strings are out of scope.

**Where things live today (verified):**
- Tag application: `vm-image/build.sh:54` — `-t "${IMAGE_NAME}:container"` inside the `TARGET=container` branch. Invoked by root `Makefile` `container-image` → `vm-image/Makefile` `container-image: @TARGET=container ./build.sh`. There is no automated `container image load` step in the repo; loading into Apple's store is a manual `docker save` + `container image load` step.
- `IMAGE_NAME` (default `stockyard-vm`): `vm-image/build.sh:19`, consumed also by `vm-image/convert-to-rootfs.sh:18` (Linux pipeline — leave alone).
- Display: `cmd/stockyard/image.go:46` prints `img.Reference` raw; `shortDigest` helper at `cmd/stockyard/image.go:87`.
- CLI test pattern: plain `package main` `*_test.go` files in `cmd/stockyard/` (see `gc_test.go`) — stdlib `testing`, no test framework. No in-repo example config carries `apple_container.image`; docs that mention image names: `docs/image-contract.md`, `vm-image/Makefile` help text, `README.md` backend paragraph (line ~101).

---

### Task 1: `displayRef` helper in `cmd/stockyard/image.go` (TDD)

**Files:**
- Create: `cmd/stockyard/image_test.go`
- Modify: `cmd/stockyard/image.go` (helper next to `shortDigest` at line 87; wire-in at line 46)

- [ ] **Step 1: Write the failing test**

Create `cmd/stockyard/image_test.go` with exactly:

```go
// cmd/stockyard/image_test.go
package main

import "testing"

func TestDisplayRef(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"hub library ref trimmed", "docker.io/library/alpine:3.21", "alpine:3.21"},
		{"hub library legacy stockyard ref trimmed", "docker.io/library/stockyard-vm:container", "stockyard-vm:container"},
		{"hub org ref drops bare docker.io", "docker.io/obra/foo:1", "obra/foo:1"},
		{"stockyard.local ref unchanged", "stockyard.local/stockyard-vm:container", "stockyard.local/stockyard-vm:container"},
		{"other registry unchanged", "ghcr.io/obra/stockyard-vm:latest", "ghcr.io/obra/stockyard-vm:latest"},
		{"already short ref unchanged", "alpine:3.21", "alpine:3.21"},
		{"hub library digest ref trimmed", "docker.io/library/alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"bare digest ref unchanged", "alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "alpine@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"empty ref unchanged", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayRef(tc.in); got != tc.want {
				t.Errorf("displayRef(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/stockyard/ -run TestDisplayRef -v`
Expected: FAIL to build with `undefined: displayRef`

- [ ] **Step 3: Write minimal implementation**

In `cmd/stockyard/image.go`, add immediately below the `shortDigest` function (after line 96):

```go
// displayRef strips containerd-style reference normalization noise for table
// display — "docker.io/library/" on official-namespace refs, bare "docker.io/"
// otherwise — matching Docker's CLI and Apple's `container image ls` table.
// Display only: stored refs stay canonical, and --image accepts short or
// qualified forms unchanged.
func displayRef(ref string) string {
	if rest, ok := strings.CutPrefix(ref, "docker.io/library/"); ok {
		return rest
	}
	return strings.TrimPrefix(ref, "docker.io/")
}
```

(`strings` is already imported in this file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/stockyard/ -run TestDisplayRef -v`
Expected: PASS (7 subtests)

- [ ] **Step 5: Wire the helper into the `image ls` table**

In `cmd/stockyard/image.go`, change the row print (currently line 45-46):

```go
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				img.Reference, shortDigest(img.Digest), img.Size, created)
```

to:

```go
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				displayRef(img.Reference), shortDigest(img.Digest), img.Size, created)
```

- [ ] **Step 6: Run the full package tests**

Run: `go test ./cmd/stockyard/`
Expected: `ok  	github.com/obra/stockyard/cmd/stockyard`

- [ ] **Step 7: Commit**

```bash
git add cmd/stockyard/image_test.go cmd/stockyard/image.go
git commit -m "feat(PRI-2178): de-noise docker.io/library/ prefixes in stockyard image ls"
```

---

### Task 2: Qualify the `container`-target tag in `vm-image/build.sh`

**Files:**
- Modify: `vm-image/build.sh` (usage header lines 8-11; new ref variable after line 26; header echo line 42; tag line 54; completion echoes lines 69-74)

No Go code here — verification is `bash -n` plus output inspection of the echo paths (the full Docker build is NOT required; see Task 4 note).

- [ ] **Step 1: Add the qualified ref variable and usage line**

In `vm-image/build.sh`, extend the usage header (currently lines 8-11):

```bash
# Usage:
#   ./build.sh                    # Build with default settings
#   IMAGE_NAME=my-image ./build.sh   # Custom image name
#   IMAGE_TAG=v1.0.0 ./build.sh      # Custom tag
#   TARGET=container ./build.sh      # Apple `container` target → stockyard.local/<name>:container
```

After the `PLATFORM` default (line 26), add:

```bash
# Qualified OCI ref for the Apple `container` target (PRI-2178).
# Unqualified names are normalized to docker.io/library/<name> by
# containerd-style stores (false Docker Hub provenance) and are squattable
# on Docker Hub — anything that PULLS an unqualified ref asks Hub for it.
# stockyard.local declares "built here, never pulled".
# The Firecracker/rootfs pipeline is deliberately NOT qualified: its Docker
# tag is consumed locally by convert-to-rootfs.sh, and the Linux registry
# names images at `stockyard image import` time.
CONTAINER_IMAGE_REF="stockyard.local/${IMAGE_NAME}:container"
```

- [ ] **Step 2: Use the qualified ref in the header echo, tag, and next-steps**

Change the header echo (currently line 42, which prints `${IMAGE_NAME}:${IMAGE_TAG}` even for the container target — a pre-existing inaccuracy):

```bash
echo "=== Building Stockyard VM Image ==="
echo "Variant: ${VARIANT}"
echo "Target:  ${TARGET}"
if [ "$TARGET" = "container" ]; then
    echo "Image: ${CONTAINER_IMAGE_REF}"
else
    echo "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
fi
echo "VM User: ${VM_USER}"
echo "Dockerfile: ${DOCKERFILE}"
echo ""
```

Change the container-branch build tag (currently line 54) from `-t "${IMAGE_NAME}:container"` to:

```bash
        -t "${CONTAINER_IMAGE_REF}" \
```

Change the container-branch completion echoes (currently lines 69-74) to:

```bash
if [ "$TARGET" = "container" ]; then
    echo "Image: ${CONTAINER_IMAGE_REF}"
    echo ""
    echo "Next steps:"
    echo "  - Load into Apple's container store:"
    echo "      docker save ${CONTAINER_IMAGE_REF} -o /tmp/stockyard-vm-oci.tar"
    echo "      container image load --input /tmp/stockyard-vm-oci.tar"
    echo "  - Run container: container run -d ${CONTAINER_IMAGE_REF}"
```

The `else` (firecracker) branch stays byte-for-byte unchanged.

- [ ] **Step 3: Verify the script**

Run: `bash -n vm-image/build.sh`
Expected: exit 0, no output.

Run: `grep -n 'IMAGE_NAME}:container' vm-image/build.sh` (exit code 1 is the expected no-match result)
Expected: no matches (the only `:container` tag is via `CONTAINER_IMAGE_REF`).

Dry-run the container branch with a fake `docker` on PATH (no real build):

```bash
mkdir -p /tmp/fake-docker
cat > /tmp/fake-docker/docker <<'EOF'
#!/bin/sh
echo "docker $@"
EOF
chmod +x /tmp/fake-docker/docker
TARGET=container PATH="/tmp/fake-docker:$PATH" ./vm-image/build.sh | grep stockyard.local
rm -rf /tmp/fake-docker
```

Expected: output includes `Image: stockyard.local/stockyard-vm:container` (twice — header and completion echo) and the fake-docker line shows `-t stockyard.local/stockyard-vm:container`.

- [ ] **Step 4: Commit**

```bash
git add vm-image/build.sh
git commit -m "feat(PRI-2178): qualify Apple container image as stockyard.local/<name>:container"
```

---

### Task 3: Update docs and help text that reference the old short ref

**Files:**
- Modify: `vm-image/Makefile` (comment line 72-73; help lines 95 and 109)
- Modify: `docs/image-contract.md` (Image families section, lines 41-46)
- Modify: `README.md` (apple-container backend paragraph, line 101)

- [ ] **Step 1: vm-image/Makefile — comment and help text**

Change the `container-image` comment (currently line 72) from:

```makefile
# Build the Apple `container` OCI image (arm64). Requires Docker + BuildKit.
```

to:

```makefile
# Build the Apple `container` OCI image (arm64), tagged
# stockyard.local/$(IMAGE_NAME or stockyard-vm):container. Requires Docker + BuildKit.
```

Change the help line (currently line 95) from:

```makefile
	@echo "  make container-image            Build the Apple container OCI image (arm64)"
```

to:

```makefile
	@echo "  make container-image            Build the Apple container OCI image (arm64, tagged stockyard.local/stockyard-vm:container)"
```

Change the `IMAGE_NAME` help line (currently line 109) from:

```makefile
	@echo "  IMAGE_NAME       Docker image name (default: stockyard-vm) — owned by build.sh"
```

to:

```makefile
	@echo "  IMAGE_NAME       Docker image name (default: stockyard-vm) — owned by build.sh; the container target tags it stockyard.local/<IMAGE_NAME>:container"
```

- [ ] **Step 2: docs/image-contract.md — qualified naming + transition note**

Replace the "Image families" section (currently lines 41-46):

```markdown
## Image families

One OCI name used on both platforms means the same *family*, not identical
bytes. `stockyard-vm` builds per-target stages from a shared Docker base
(`vm-image/Dockerfile`): the `firecracker` stage ships systemd, the
`container` stage ships the container init. Follow that pattern.
```

with:

```markdown
## Image families

One OCI name used on both platforms means the same *family*, not identical
bytes. `stockyard-vm` builds per-target stages from a shared Docker base
(`vm-image/Dockerfile`): the `firecracker` stage ships systemd, the
`container` stage ships the container init. Follow that pattern.

Stockyard-built OCI images are qualified as `stockyard.local/<name>:<tag>`;
the macOS container image is `stockyard.local/stockyard-vm:container`
(built by `make container-image`). Unqualified names display as
`docker.io/library/<name>` under containerd-style reference normalization —
false Hub provenance — and are squattable on Docker Hub. `stockyard.local`
states provenance without implying a real registry; stockyard never pulls.
Linux Firecracker registry names are unaffected: images there are named at
`stockyard image import` time.

**Transition:** existing unqualified local tags keep working. Alias them with
`container image tag stockyard-vm:container stockyard.local/stockyard-vm:container`.
```

- [ ] **Step 3: README.md — name the qualified image in the backend paragraph**

Change the paragraph at line 101 from:

```markdown
The top-level `backend` key selects the VM backend. Valid values are `"firecracker"` (default, Linux) and `"apple-container"` (macOS). The apple-container backend skips the Firecracker-only setup steps — no ZFS, no kernel/rootfs install — and uses Apple's `container` CLI to manage VMs.
```

to:

```markdown
The top-level `backend` key selects the VM backend. Valid values are `"firecracker"` (default, Linux) and `"apple-container"` (macOS). The apple-container backend skips the Firecracker-only setup steps — no ZFS, no kernel/rootfs install — and uses Apple's `container` CLI to manage VMs. Its task image is set via `apple_container.image` (e.g. `"stockyard.local/stockyard-vm:container"`, built by `make container-image`).
```

- [ ] **Step 4: Commit**

```bash
git add vm-image/Makefile docs/image-contract.md README.md
git commit -m "docs(PRI-2178): document stockyard.local image qualification and transition"
```

---

### Task 4: Verification + live macOS smoke

**Files:** none created or modified (scratch artifacts under `/tmp/stockyard-smoke-2178/`, removed at the end).

The full vm-image Docker build is NOT required — the smoke aliases an existing local image to a `stockyard.local/...` name with `container image tag`.

**Isolation rule (non-negotiable):** never touch the real daemon socket or data dir. Every `stockyardd` AND every `stockyard` CLI invocation below carries `STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178`. (`STOCKYARD_DATA_DIR` alone does NOT isolate.)

- [ ] **Step 1: Build and unit-test**

Run: `make build`
Expected: exit 0; binaries in `bin/` (`bin/stockyard`, `bin/stockyardd`).

Run: `make test`
Expected: all `./pkg/...` packages `ok`. Note: `make test` does NOT cover `cmd/` (it runs `go test ./pkg/...` only — known gap, tracked as PRI-2180), so additionally:

Run: `go test ./cmd/stockyard/ -v`
Expected: all tests `ok`, including `TestDisplayRef`. Do not widen the Makefile here — that's PRI-2180's scope.

- [ ] **Step 2: Pre-flight the Apple container CLI**

Run: `container system status`
Expected: reports the apiserver running. If not: `container system start`.

Run: `container image ls`
Expected: a non-empty local store. Note an existing stockyard-family ref — typically `docker.io/library/stockyard-vm:container` (displayed by Apple's CLI as `stockyard-vm  container`). If `stockyard-vm:container` is absent, any bootable local image (e.g. a `prudence-vm` ref) works for the tag+run steps; substitute it below.

- [ ] **Step 3: Alias an existing image to the qualified name**

Run: `container image tag stockyard-vm:container stockyard.local/stockyard-vm:container`
Expected: exit 0. `container image ls` now also shows `stockyard.local/stockyard-vm` with tag `container`.

- [ ] **Step 4: Create the scratch instance config**

```bash
mkdir -p /tmp/stockyard-smoke-2178/secrets /tmp/stockyard-smoke-2178/data
cat > /tmp/stockyard-smoke-2178/config.json <<'EOF'
{
  "instance_id": "smoke2178",
  "backend": "apple-container",
  "secrets": {"provider": "file", "dir": "/tmp/stockyard-smoke-2178/secrets"},
  "daemon": {"socket_path": "/tmp/stockyard-smoke-2178/stockyardd.sock", "data_dir": "/tmp/stockyard-smoke-2178/data"},
  "http": {"enabled": false},
  "apple_container": {"image": "stockyard.local/stockyard-vm:container"}
}
EOF
```

Known gotcha (harmless): the daemon ignores `secrets.provider` and always tries 1Password first with file fallback, errors swallowed. We pass `--no-tailscale` on runs so no secret is needed.

- [ ] **Step 5: Start the scratch daemon**

```bash
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178 ./bin/stockyardd > /tmp/stockyard-smoke-2178/daemon.log 2>&1 &
echo $! > /tmp/stockyard-smoke-2178/daemon.pid
```

Wait for the socket: `ls /tmp/stockyard-smoke-2178/stockyardd.sock` succeeds within a few seconds. On failure, read `/tmp/stockyard-smoke-2178/daemon.log`.

- [ ] **Step 6: Verify de-noised `image ls` display**

Run: `STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178 ./bin/stockyard image ls`

Expected, all three:
1. Output contains `stockyard.local/stockyard-vm:container` (qualified ref shown as-is).
2. Output does NOT contain the substring `docker.io/library/` anywhere.
3. A Hub image displays short — e.g. the row that Apple's store holds as `docker.io/library/stockyard-vm:container` displays as `stockyard-vm:container` (and e.g. `alpine:3.21` if present).

Check: `STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178 ./bin/stockyard image ls | grep -c 'docker.io/library/'`
Expected: `0` (grep exits 1).

- [ ] **Step 7: Boot a task from the qualified ref**

```bash
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178 ./bin/stockyard run --image stockyard.local/stockyard-vm:container --no-tailscale --name smoke2178
```

Expected: exit 0, prints a task ID. Then:

Run: `STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178 ./bin/stockyard list`
Expected: the `smoke2178` task present and running.

- [ ] **Step 8: Destroy the task**

```bash
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178 ./bin/stockyard destroy <task-id-from-step-7> --force
```

Expected: exit 0. (Without `--force`, destroy is a dry run — it must be passed.)

Run: `STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke-2178 ./bin/stockyard list`
Expected: task gone (or not running).

- [ ] **Step 9: Tear down the scratch instance**

```bash
kill "$(cat /tmp/stockyard-smoke-2178/daemon.pid)"
container image rm stockyard.local/stockyard-vm:container   # use `container image delete ...` if `rm` is unrecognized
rm -rf /tmp/stockyard-smoke-2178
```

Expected: daemon exits; the alias tag is removed (the underlying image and its original tag remain); scratch dir gone. Verify with `container image ls` (no `stockyard.local/...` row) and `ls /tmp/stockyard-smoke-2178` (no such file).

- [ ] **Step 10: Final check of the branch**

Run: `git status --short`
Expected: clean tree (all work committed in Tasks 1-3; the smoke created no repo files).

Run: `git log --oneline main..HEAD`
Expected: the plan commit plus the three implementation commits from Tasks 1-3.

---

## Out of scope (tracked elsewhere)

- prudence-vm adopting `stockyard.local/` qualification — different repo, PRI-2063 follow-up.
- Linux Firecracker registry naming and `convert-to-rootfs.sh` — the registry names images at `stockyard image import` time.
- Dashboard or daemon-error-string ref display.
- Any `--image` resolution changes — short and qualified forms both pass through to `container image inspect` exactly as typed.

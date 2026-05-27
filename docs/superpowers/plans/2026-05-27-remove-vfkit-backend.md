# Remove vfkit Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Linear:** PRI-1855

**Goal:** Collapse the stockyard supported-backend matrix to Linux+Firecracker and macOS+apple-container, removing vfkit and everything that exists solely to support it.

**Architecture:** This is a pure-deletion change. No new features, no new tests. Each phase removes a self-contained slice (a backend, a machinery layer, a build subsystem, docs) and verifies the build + existing tests still pass before committing. macOS-version floor moves to whatever apple-container requires (Tahoe / macOS 26) — no fallback for older macOS, by Matt's call ("upgrade or die").

**Tech Stack:** Go 1.x, Make, Docker (for non-vfkit image build), shell.

**Prerequisite verification — before Phase 1:**
- [ ] Confirm current branch is the new `remove-vfkit-backend` branch (created off `origin/main`).
- [ ] Confirm baseline build is green:
  ```bash
  make build && make test
  GOOS=linux go build ./...
  GOOS=darwin go build ./...
  ```
  Expected: all four commands exit 0. If any fails on `main`, stop and ask — that's a pre-existing breakage, not the plan's problem to solve.

---

## Phase 1: Delete the vfkit backend

Removes the vfkit `VMBackend` implementation and every direct reference to it. After this phase, `backend: vfkit` in config produces "unknown backend" at daemon startup. The rootfs provisioner machinery still exists (Phase 2 handles that).

**Files:**
- Delete: `pkg/vmbackend/vfkit.go`
- Delete: `pkg/vmbackend/vfkit_test.go`
- Delete: `pkg/config/vfkit.go`
- Modify: `pkg/config/config.go` (drop `Vfkit` field + backend doc comment)
- Modify: `pkg/daemon/daemon.go` (drop vfkit switch case)
- Modify: `pkg/daemon/backend_darwin.go` (drop `createVfkitBackend`)
- Modify: `cmd/stockyard/attach.go` (drop vfkit mention)
- Modify: `cmd/stockyard/attach_test.go` (drop vfkit test case)

- [ ] **Step 1.1: Delete vfkit backend source + test**

```bash
rm pkg/vmbackend/vfkit.go pkg/vmbackend/vfkit_test.go pkg/config/vfkit.go
```

- [ ] **Step 1.2: Drop the `Vfkit` field from `Config`**

Open `pkg/config/config.go`. Find the `Config` struct. Remove the `Vfkit` field declaration. Also update the `Backend` field's doc comment (above the field) so it no longer lists vfkit as a valid value. Acceptable values are now exactly `""` (default → firecracker), `"firecracker"`, `"apple-container"`.

- [ ] **Step 1.3: Drop the vfkit switch case in daemon backend selection**

Open `pkg/daemon/daemon.go`. Locate the backend selection switch (around lines 103–134). Delete the vfkit case (the block that calls `createVfkitBackend`). The default error path on unknown backend stays.

- [ ] **Step 1.4: Drop `createVfkitBackend` from the darwin backend helpers**

Open `pkg/daemon/backend_darwin.go`. Delete the `createVfkitBackend` function and its imports if they become unused. If the file becomes empty (only the package declaration remains), leave it as-is — Phase 2 may rewrite it; do not delete the file here.

- [ ] **Step 1.5: Drop vfkit mention from `stockyard attach`**

Open `cmd/stockyard/attach.go`. Find the comment + long-description string that names vfkit (around lines 22 and 61 per the audit). Reword so it describes only the apple-container vs SSH dispatch, with SSH being the fallback for Firecracker (the only remaining SSH backend).

- [ ] **Step 1.6: Drop the vfkit attach test**

Open `cmd/stockyard/attach_test.go`. Delete the `TestBuildAttachCommand_SSHForVfkit` function entirely. Keep any other SSH-path test that exercises Firecracker/empty-backend (those still apply).

- [ ] **Step 1.7: Verify build on both platforms**

```bash
GOOS=linux go build ./...
GOOS=darwin go build ./...
```
Expected: both exit 0. If darwin fails with `undefined: createVfkitBackend` or similar, fix the dangling reference and re-run.

- [ ] **Step 1.8: Run tests**

```bash
make test
```
Expected: PASS. The `TestCreateRootfsProvisioner_VfkitStillProvisions` test in `pkg/daemon/rootfs_darwin_test.go` still exists at this point — it may now compile but reference a vfkit config that's gone. If it fails to compile, fix by either marking it skipped temporarily or just letting Phase 2 delete the whole file (preferred — easier to commit Phase 1 cleanly by skipping the test, then deleting in Phase 2). If you skip, use `t.Skip("removed in Phase 2 of vfkit removal")` so the skip carries an explanation.

- [ ] **Step 1.9: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(PRI-1855): remove vfkit VM backend

Drops the vfkit backend implementation, its config type, the daemon
switch case, the createVfkitBackend helper, and the vfkit mention in
stockyard attach. The rootfs provisioner machinery (APFS, copy, the
interface) is removed in Phase 2.

After this commit, backend: vfkit in stockyardd config produces an
"unknown backend" error at startup.

Co-Authored-By: Tiffany@3758e0fe (Opus 4.7)
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2: Delete the rootfs.Provisioner machinery

Removes `pkg/rootfs/` entirely (APFS + copy + ZFS provisioners + the interface), the daemon's platform-specific factory files, the daemon field/accessor, and the nil-checked call sites in `tasks.go`. Verified by prior audit: ZFS provisioner has zero callers (Firecracker builds rootfs paths directly), APFS was vfkit-only, copy provisioner becomes unreachable after Phase 1.

**Files:**
- Delete: `pkg/rootfs/` (entire directory: `apfs.go`, `apfs_test.go`, `copy.go`, `copy_test.go`, `zfs.go`, `zfs_test.go`, `provisioner.go`)
- Delete: `pkg/daemon/rootfs_darwin.go`
- Delete: `pkg/daemon/rootfs_other.go`
- Delete: `pkg/daemon/rootfs_darwin_test.go`
- Modify: `pkg/daemon/daemon.go` (drop `rootfsProvisioner` field, accessor, init line, `rootfs` import)
- Modify: `pkg/daemon/tasks.go` (drop nil-checked `RootfsProvisioner()` call sites)
- Modify: `pkg/config/config.go` (drop `Rootfs` field + `RootfsConfig` type definition)

- [ ] **Step 2.1: Delete the rootfs package and daemon platform files**

```bash
rm -r pkg/rootfs/
rm pkg/daemon/rootfs_darwin.go pkg/daemon/rootfs_other.go pkg/daemon/rootfs_darwin_test.go
```

- [ ] **Step 2.2: Drop the `rootfsProvisioner` field, accessor, and initialization from the daemon**

Open `pkg/daemon/daemon.go`. Remove:
1. The `rootfs` import (line ~22).
2. The `rootfsProvisioner rootfs.Provisioner` field (line ~40).
3. The `d.rootfsProvisioner = createRootfsProvisioner(cfg)` initialization (line ~136).
4. The `RootfsProvisioner()` accessor method (lines ~530–533).

- [ ] **Step 2.3: Drop nil-checked `RootfsProvisioner()` call sites in tasks.go**

Open `pkg/daemon/tasks.go`. There are four call sites (around lines 164, 199, 241, 567 per the audit). Each is guarded by `if tm.daemon.RootfsProvisioner() != nil`. Since the provisioner is now always nil, delete each guarded block in its entirety. After this, `rootfsPath` (a local variable assigned from `Clone(...)` at line ~166) may also become unused — if so, delete that variable declaration and any downstream use of it that this plan didn't already cover.

Read the file carefully — the four call sites are: rootfs clone on task creation, rootfs destroy on creation-failure rollback (×2), and rootfs destroy on task deletion. All four go.

- [ ] **Step 2.4: Drop the `Rootfs` field and `RootfsConfig` type from Config**

Open `pkg/config/config.go`. Remove:
1. The `Rootfs RootfsConfig` field on the `Config` struct (line ~23).
2. The `RootfsConfig` type definition (line ~27 onwards). Leave the top-level `RootfsPath` field alone — it's the Firecracker rootfs path and is still used by `pkg/vmbackend/firecracker.go`.

- [ ] **Step 2.5: Verify build on both platforms**

```bash
GOOS=linux go build ./...
GOOS=darwin go build ./...
```
Expected: both exit 0. If a build fails with `undefined: rootfs.Provisioner` or similar, you missed a reference — grep for `rootfs.` and `RootfsProvisioner` across the daemon package.

- [ ] **Step 2.6: Run tests**

```bash
make test
```
Expected: PASS. No test should reference `pkg/rootfs` after the file deletions.

- [ ] **Step 2.7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor(PRI-1855): remove rootfs.Provisioner machinery (dead code)

Firecracker manages its own rootfs.ext4 directly via pkg/firecracker
(constructs paths from ZFS mountpoint or vmDir); apple-container owns
its own rootfs. With vfkit gone (Phase 1), no backend uses the
rootfs.Provisioner interface, so the entire pkg/rootfs package
(APFS + copy + ZFS implementations + interface), both daemon platform
factories, the daemon field/accessor, the nil-checked call sites in
tasks.go, and the RootfsConfig in config are all dead code. Removed.

Co-Authored-By: Tiffany@3758e0fe (Opus 4.7)
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3: Delete the macOS Alpine VM image build

The entire `vm-image/macos/` subtree (Alpine rootfs + Kata kernel download + setup script) exists to feed vfkit. Container backend uses a separate OCI image built from `vm-image/Dockerfile` (the multi-stage one merged in PR #1).

**Files:**
- Delete: `vm-image/macos/` (entire directory)
- Modify: `vm-image/Makefile` (drop alpine variant targets)

- [ ] **Step 3.1: Delete the macOS image build directory**

```bash
rm -r vm-image/macos/
```

- [ ] **Step 3.2: Drop alpine targets from vm-image/Makefile**

Open `vm-image/Makefile`. Remove:
1. The `docker-alpine`, `rootfs-alpine`, `deploy-alpine`, `docker-image-alpine` targets (whatever set exists — read the file).
2. Their entries from the `.PHONY` declaration (line ~11).
3. Any help-text lines that reference the alpine variant.

Leave `docker`, `rootfs`, `deploy`, `container-image`, `clean`, `help` and any other Firecracker/container-image-only targets in place.

- [ ] **Step 3.3: Confirm no other reference to vm-image/macos/ remains**

```bash
grep -rn 'vm-image/macos' --include='Makefile' --include='*.md' --include='*.sh' --include='*.go' .
```
Expected: only matches inside `docs/` files that Phase 4 will delete or update. If anything else matches (e.g. the top-level `Makefile`), fix it.

- [ ] **Step 3.4: Verify top-level build still works**

```bash
make build && make test
```
Expected: PASS. (No code changes in this phase — purely build-infra.)

- [ ] **Step 3.5: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
build(PRI-1855): remove macOS Alpine VM image build

vm-image/macos/ produced an ext4 rootfs and downloaded a Kata kernel
for vfkit; with vfkit gone, none of it is reachable. The
apple-container backend consumes an OCI image built from
vm-image/Dockerfile (multi-stage, shared with Firecracker).

Co-Authored-By: Tiffany@3758e0fe (Opus 4.7)
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 4: Documentation cleanup

Removes vfkit/APFS/Alpine references from user-facing docs and deletes superseded design docs outright (per Matt: "Just delete them. Don't care.").

**Files:**
- Modify: `CLAUDE.md` (drop brew install line + macOS Setup section)
- Modify: `README.md` (drop vfkit/macOS-setup paragraph)
- Delete: `docs/superpowers/plans/2026-04-01-vfkit-backend.md`
- Delete: `docs/superpowers/plans/2026-04-01-fast-boot-alpine.md`
- Delete: `docs/research/macos-backend-sketch.md`
- Modify: `docs/superpowers/plans/2026-04-01-vm-backend-interface.md` (drop vfkit case studies if present, else delete the doc if it's wholly vfkit-historical)

- [ ] **Step 4.1: Edit CLAUDE.md**

Open `CLAUDE.md`. Remove the entire "## macOS Setup" section (the one with `brew install vfkit e2fsprogs` and the call to `vm-image/macos/setup.sh`). Also remove the link to `vm-image/macos/README.md`.

If after removal the surrounding sections look stranded (e.g. a "## Testing" header immediately after a deleted "## macOS Setup"), tidy the spacing but don't invent new content.

- [ ] **Step 4.2: Edit README.md**

Open `README.md`. Find the paragraph or bullet that links to the macOS vfkit setup (around lines 5–6 per the audit). Delete it. Don't replace it with an apple-container setup link unless one exists in the repo — flag in the PR description if you think one should be written separately.

- [ ] **Step 4.3: Delete superseded design docs**

```bash
rm docs/superpowers/plans/2026-04-01-vfkit-backend.md
rm docs/superpowers/plans/2026-04-01-fast-boot-alpine.md
rm docs/research/macos-backend-sketch.md
```

- [ ] **Step 4.4: Review vm-backend-interface design doc**

Open `docs/superpowers/plans/2026-04-01-vm-backend-interface.md`. Skim it.
- If it's a generic backend-interface design that still applies (just happens to use vfkit as a case study), edit out the vfkit-specific examples and keep the rest.
- If it's primarily a vfkit-implementation plan in disguise, delete it (`rm docs/superpowers/plans/2026-04-01-vm-backend-interface.md`).

Use judgment. The audit flagged it as "edit to remove vfkit case studies" — verify that's the right call before doing either.

- [ ] **Step 4.5: Final grep for stragglers**

```bash
grep -rin 'vfkit\|apfs\|kata' --include='*.go' --include='*.md' --include='Makefile' --include='*.sh' . | grep -v '^./docs/research/' | grep -v 'vendor/'
```
Expected: empty, or only references in historical files you've consciously kept (e.g. older research notes that are clearly dated). Anything in `*.go`, `Makefile`, `*.sh`, or user-facing markdown is a bug — fix it.

- [ ] **Step 4.6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
docs(PRI-1855): remove vfkit/APFS/macOS-Alpine references

CLAUDE.md and README.md no longer mention vfkit setup. Three
superseded design docs deleted: the original vfkit-backend plan, the
Alpine fast-boot tuning plan, and the macos-backend-sketch research
note. apple-container is the macOS backend going forward.

Co-Authored-By: Tiffany@3758e0fe (Opus 4.7)
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5: Final verification + push

- [ ] **Step 5.1: Full clean build + test pass on both platforms**

```bash
make clean 2>/dev/null || true
make build
make test
GOOS=linux go build ./...
GOOS=darwin go build ./...
```
Expected: all green. Any failure here is a regression introduced by this branch — investigate before pushing.

- [ ] **Step 5.2: Verify no vfkit/APFS references in code or active docs**

```bash
grep -rin 'vfkit' --include='*.go' --include='Makefile' --include='*.sh' .
grep -rin 'APFSProvisioner\|NewAPFSProvisioner' --include='*.go' .
grep -rin 'rootfs\.Provisioner\|RootfsConfig\b' --include='*.go' .
```
Expected: all three commands return empty (no matches).

- [ ] **Step 5.3: Push branch and open PR against `main`**

```bash
git push -u origin HEAD
gh pr create --title "Remove vfkit backend (PRI-1855)" --body "$(cat <<'EOF'
## Summary

- Removes the vfkit VM backend and everything that existed solely to support it on macOS.
- Supported backends are now: **Linux+Firecracker**, **macOS+apple-container**. macOS-version floor moves to whatever apple-container requires (Tahoe / macOS 26).
- The `rootfs.Provisioner` interface and all three implementations (APFS, copy, ZFS) were dead code post-vfkit and have been deleted.
- Removes the `vm-image/macos/` Alpine + Kata kernel build subtree.
- Cleans up CLAUDE.md, README.md, and three superseded design docs.

## Breaking change

Users with `backend: vfkit` in their stockyardd config will see an "unknown backend" error at startup. Intended. No migration shim.

## Test plan

- [x] `make build` clean
- [x] `make test` clean
- [x] `GOOS=linux go build ./...` clean
- [x] `GOOS=darwin go build ./...` clean
- [ ] Manual: `stockyardd` starts on macOS with `backend: apple-container`, creates and destroys a task end-to-end
- [ ] Manual (Linux): `stockyardd` still starts and serves Firecracker tasks normally

Linear: PRI-1855

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5.4: Move Linear ticket to In Review and post reflective comment**

Per the primeradiant-ops:linear-ticket-lifecycle skill: transition PRI-1855 to "In Review" and post a reflective implementation comment covering what went smoothly, what was tricky, subjective confidence level, and any risks for the reviewer to watch.

---

## Self-review notes

**Spec coverage:** Every removal-surface item from the PRI-1855 ticket description maps to a step above:
- vfkit backend code → Phase 1 steps 1.1–1.8
- VfkitConfig → Phase 1 step 1.1
- APFS provisioner → Phase 2 step 2.1 (folded into pkg/rootfs deletion)
- copy provisioner → Phase 2 step 2.1
- ZFS provisioner (newly discovered dead code) → Phase 2 step 2.1
- rootfs.Provisioner machinery → Phase 2 steps 2.1–2.4
- macOS Alpine VM image → Phase 3
- CLI/daemon edits → Phase 1 steps 1.3–1.6
- Documentation → Phase 4

**Risks for the executor:**
1. **Test compile failure between phases.** `pkg/daemon/rootfs_darwin_test.go` references a `Backend == "vfkit"` config in `TestCreateRootfsProvisioner_VfkitStillProvisions`. If you delete `VfkitConfig` in Phase 1 step 1.2 but leave the test file in place (Phase 2 deletes it), the test may still compile (the string `"vfkit"` is just a string), but its assertion ("APFS provisioner returned") will fail at runtime once the backend selection rejects vfkit. Step 1.8 calls this out; resolution is to `t.Skip` it for the Phase-1 commit. Phase 2 step 2.1 removes the file outright.

2. **The `attach.go` long-description and `tasks.go` nil-checks both have multiple sites** — re-grep before assuming the audit's line numbers are still current after each commit.

3. **The vm-backend-interface design doc** (step 4.4) requires judgment — read it before deciding edit-vs-delete. The plan does not force one path.

4. **No new tests are added.** That's intentional. This is dead-code removal; the verification is "existing tests pass + the package compiles." If a reviewer wants a regression test that `backend: vfkit` errors at startup, that's a one-line addition in `daemon_test.go` — flag if asked but don't write it speculatively.

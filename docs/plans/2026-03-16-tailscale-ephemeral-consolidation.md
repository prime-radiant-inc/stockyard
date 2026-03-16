# Tailscale Ephemeral Node Consolidation — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix PRI-710 — make Stockyard VMs register as ephemeral Tailscale nodes and consolidate redundant `tailscale up` paths into a single authority (the VM init script).

**Architecture:** Pre-registration on the host adds `--ephemeral`. Cloud-init stops generating `tailscale up` runcmds — the init script is the sole owner of Tailscale inside the VM. Dead code paths are removed.

**Tech Stack:** Go, shell (bash)

**Spec:** `docs/specs/2026-03-16-tailscale-ephemeral-consolidation.md`

---

## Task 1: Add `--ephemeral` to pre-registration

This is the core bug fix. The host-side pre-registration registers nodes without `--ephemeral`, so they persist after VM destruction.

**Files:**
- Modify: `pkg/tailscale/preregister.go:84-93`

- [ ] **Step 1: Add `--ephemeral` flag to `tailscale up` args**

In `pkg/tailscale/preregister.go`, the `up` command (line 84-93) is missing `--ephemeral`. Add it:

```go
	up := exec.CommandContext(ctx,
		"tailscale",
		"--socket="+socketPath,
		"up",
		"--authkey=file:"+authKeyPath,
		"--hostname="+hostname,
		"--accept-routes",
		"--ssh",
		"--ephemeral",
		"--timeout=30s",
	)
```

- [ ] **Step 2: Run existing tests**

Run: `cd /Users/matt/Code/prime/stockyard && go build ./pkg/tailscale/...`
Expected: compiles cleanly (the integration test requires `TAILSCALE_AUTH_KEY` so just verify it builds)

- [ ] **Step 3: Commit**

```bash
git add pkg/tailscale/preregister.go
git commit -m "fix: add --ephemeral to Tailscale pre-registration (PRI-710)

The pre-registration path runs tailscale up on the host to create a node
identity before the VM boots. Missing --ephemeral meant nodes persisted
in the tailnet after VM destruction."
```

---

## Task 2: Remove `tailscale up` from cloud-init generation (firecracker)

The init script handles Tailscale setup. Cloud-init shouldn't also race to run `tailscale up`.

**Files:**
- Modify: `pkg/firecracker/cloudinit.go:99-107`
- Modify: `pkg/daemon/tasks.go:189-195`

- [ ] **Step 1: Remove Tailscale runcmd from `CloudInitConfig.Generate()`**

In `pkg/firecracker/cloudinit.go`, remove the Tailscale block from the runcmd section (lines 100-107):

```go
	// Build runcmd section
	var cmds []string

	// Create stockyard directory
	cmds = append(cmds, "mkdir -p /etc/stockyard")

	// Run post-create script if provided
	if c.PostCreateScript != "" {
		cmds = append(cmds, "/etc/stockyard/post-create.sh")
	}
```

- [ ] **Step 2: Remove `TailscaleAuthKey` and `TailscaleHostname` fields from firecracker `CloudInitConfig`**

In `pkg/firecracker/cloudinit.go`, remove the two fields from the struct (lines 18-19):

```go
type CloudInitConfig struct {
	Hostname          string
	Environment       map[string]string
	SSHAuthorizedKeys []string
	WorkspacePath     string
	PostCreateScript  string
}
```

- [ ] **Step 3: Stop passing Tailscale fields to `CloudInitConfig` in `tasks.go`**

In `pkg/daemon/tasks.go` (lines 189-195), remove the two Tailscale fields:

```go
	cloudInitCfg := &firecracker.CloudInitConfig{
		Hostname:      hostname,
		Environment:   env,
		WorkspacePath: workspacePath,
	}
```

- [ ] **Step 4: Verify it builds**

Run: `cd /Users/matt/Code/prime/stockyard && go build ./...`
Expected: compiles cleanly

- [ ] **Step 5: Commit**

```bash
git add pkg/firecracker/cloudinit.go pkg/daemon/tasks.go
git commit -m "refactor: remove tailscale up from firecracker cloud-init

The VM init script is the single authority for Tailscale setup inside the
VM. The auth key reaches the VM via MMDS metadata, not cloud-init runcmd."
```

---

## Task 3: Remove `tailscale up` from cloud-init generation (flintlock)

Same change as Task 2 but for the flintlock provider. Tests need updating.

**Files:**
- Modify: `pkg/flintlock/cloudinit.go:12-21, 99-107`
- Modify: `pkg/flintlock/cloudinit_test.go`

- [ ] **Step 1: Remove Tailscale runcmd from flintlock `CloudInitConfig.Generate()`**

In `pkg/flintlock/cloudinit.go`, remove the Tailscale block (lines 99-107) and the two struct fields (lines 17-18). Same pattern as Task 2:

Struct becomes:
```go
type CloudInitConfig struct {
	Hostname          string
	Environment       map[string]string
	SSHAuthorizedKeys []string
	WorkspacePath     string
	PostCreateScript  string
}
```

Runcmd section becomes:
```go
	// Build runcmd section
	var cmds []string

	// Create stockyard directory
	cmds = append(cmds, "mkdir -p /etc/stockyard")

	// Run post-create script if provided
	if c.PostCreateScript != "" {
		cmds = append(cmds, "/etc/stockyard/post-create.sh")
	}
```

- [ ] **Step 2: Update tests — remove Tailscale fields from test configs**

In `pkg/flintlock/cloudinit_test.go`:

- `TestCloudInitConfig_Generate` (line 10): Remove `TailscaleAuthKey` field (line 19). Change the tailscale assertion (line 47-49) to verify tailscale is NOT in output:

```go
	// Should NOT contain tailscale up command (init script handles this)
	if strings.Contains(content, "tailscale up") {
		t.Error("should not contain tailscale up command — init script owns Tailscale setup")
	}
```

- `TestCloudInitConfig_Generate_NoTailscaleWithoutKey` (line 119): Remove `TailscaleAuthKey` field. The test still makes sense — verify no tailscale command appears regardless.

- `TestCloudInitConfig_Generate_TailscaleHostname` (line 143): **Delete this entire test function.** It tests Tailscale hostname behavior in cloud-init runcmd, which no longer exists.

- [ ] **Step 3: Run tests**

Run: `cd /Users/matt/Code/prime/stockyard && go test ./pkg/flintlock/...`
Expected: all tests pass

- [ ] **Step 4: Commit**

```bash
git add pkg/flintlock/cloudinit.go pkg/flintlock/cloudinit_test.go
git commit -m "refactor: remove tailscale up from flintlock cloud-init

Same consolidation as firecracker provider. Init script is the single
authority for Tailscale setup inside the VM."
```

---

## Task 4: Delete dead code

Remove unused code paths that are no longer referenced.

**Files:**
- Delete: `vm-image/cloud-init/user-data.template`
- Modify: `pkg/tailscale/tailscale.go:47-50`

- [ ] **Step 1: Delete the cloud-init template**

```bash
rm vm-image/cloud-init/user-data.template
```

If `vm-image/cloud-init/` is now empty, delete the directory too.

- [ ] **Step 2: Remove `GenerateCloudInitScript` from `tailscale.go`**

In `pkg/tailscale/tailscale.go`, delete lines 47-50:

```go
// GenerateCloudInitScript generates the cloud-init script for Tailscale setup
func (m *Manager) GenerateCloudInitScript(hostname string) string {
	return fmt.Sprintf(`tailscale up --authkey=%s --hostname=%s --accept-routes --ssh`, m.authKey, hostname)
}
```

Also remove the `"fmt"` import if it's no longer used (check — it's still used by other functions like `ValidateAuthKey` and `BuildHostname`). It is still used, so leave imports alone.

- [ ] **Step 3: Verify it builds**

Run: `cd /Users/matt/Code/prime/stockyard && go build ./...`
Expected: compiles cleanly

- [ ] **Step 4: Commit**

```bash
git add -A vm-image/cloud-init/ pkg/tailscale/tailscale.go
git commit -m "cleanup: remove dead tailscale up code paths

- Delete vm-image/cloud-init/user-data.template (unused Go template,
  references legacy 'vscode' user)
- Remove GenerateCloudInitScript (defined but never called)"
```

---

## Task 5: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/matt/Code/prime/stockyard && go test ./...`
Expected: all tests pass

- [ ] **Step 2: Verify no remaining non-ephemeral `tailscale up` in codebase**

Run: `grep -r "tailscale up" --include="*.go" --include="*.sh" --include="*.template" .`
Expected: only `stockyard-init.sh:210` which already has `--ephemeral`

- [ ] **Step 3: Commit any fixups if needed, then done**

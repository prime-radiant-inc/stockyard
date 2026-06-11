# Snapshot Command Group Implementation Plan (PRI-2176)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the three flat top-level snapshot commands (`stockyard snapshot <task-id>`, `stockyard snapshots`, `stockyard restore`) with a single cobra command group: `stockyard snapshot create|ls|restore`.

**Architecture:** Pure CLI-layer regroup modeled exactly on the existing `image` group (`cmd/stockyard/image.go`, single-file group + subcommands, introduced by PRI-2150). The three existing `RunE` bodies move verbatim under the new group; gRPC, `pkg/client`, and the daemon are untouched. Clean break: old top-level forms are removed outright — no hidden aliases, no deprecation wrappers (zero script usage was verified before this plan).

**Tech Stack:** Go, spf13/cobra v1.10.2.

---

## Decisions Already Made (do not relitigate)

- `snapshot create <task-id> [label]`, `snapshot ls <task-id>` (alias `list`), `snapshot restore <task-id> <snapshot-name>`.
- Old forms removed outright. After this change, `stockyard snapshot <task-id>` (bare arg, no subcommand) produces cobra's standard behavior for a non-runnable group: it prints the group help and exits 0. This was verified against cobra v1.10.2 with the existing group (`stockyard image foo` prints the image group help, exit 0). `stockyard snapshots ...` and `stockyard restore ...` produce `unknown command "snapshots"/"restore" for "stockyard"` and exit 1.
- One file `cmd/stockyard/snapshot.go` holds the group plus all three subcommands, like `image.go`. Delete `snapshots.go` and `restore.go`.
- Out of scope: grouping task verbs (run/list/stop/...), any gRPC/client/daemon change, hidden compatibility shims.

## Codebase Facts (verified during planning)

- `getClient()` lives in `cmd/stockyard/client.go`; subcommand `RunE` bodies already use it. Client methods used (unchanged, `pkg/client/client.go`): `CreateSnapshot(ctx, taskID, label) (string, error)`, `ListSnapshots(ctx, taskID) ([]*pb.Snapshot, error)`, `RestoreSnapshot(ctx, taskID, snapshotName) error`, `GetTask(ctx, taskID) (*pb.Task, error)`.
- `make test` runs `go test ./pkg/... -v` only — it does NOT run `cmd/stockyard` tests, and CI (`.github/workflows/ci.yml`) runs `make test`. CLI tests must be run explicitly with `go test ./cmd/stockyard/`.
- The whole repo was grepped for the old command forms: the only references are the three CLI source files themselves. `README.md` and live docs mention "snapshot" only for the in-guest `stockyard-snapshot` vsock service and ZFS internals — those are NOT CLI invocations and must not be edited.
- On the macOS apple-container backend the daemon's `ListSnapshots` handler (`pkg/daemon/grpc.go:155`) returns `codes.Unavailable, "snapshots require ZFS (not available on this backend)"` because `daemon.zfs == nil`. The smoke test uses this deterministic error to prove CLI → gRPC → daemon wiring through the new group.

## File Structure

- Modify: `cmd/stockyard/snapshot.go` — full rewrite: `snapshotCmd` becomes the group; `snapshotCreateCmd`, `snapshotLsCmd`, `snapshotRestoreCmd` carry the three verbatim `RunE` bodies.
- Create: `cmd/stockyard/snapshot_test.go` — structural tests for the group (style follows `restart_test.go`: assert on command metadata, no daemon needed).
- Delete: `cmd/stockyard/snapshots.go`, `cmd/stockyard/restore.go`.
- No other files change. (Docs sweep in Task 2 confirms.)

---

### Task 1: Snapshot command group with create/ls/restore subcommands

**Files:**
- Create: `cmd/stockyard/snapshot_test.go`
- Modify: `cmd/stockyard/snapshot.go` (full rewrite)
- Delete: `cmd/stockyard/snapshots.go`, `cmd/stockyard/restore.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/stockyard/snapshot_test.go` with exactly this content:

```go
// cmd/stockyard/snapshot_test.go
package main

import "testing"

func TestSnapshotGroup_Structure(t *testing.T) {
	if snapshotCmd.Use != "snapshot" {
		t.Errorf("expected Use 'snapshot', got %q", snapshotCmd.Use)
	}
	if snapshotCmd.Short == "" {
		t.Error("expected non-empty Short description")
	}
	if snapshotCmd.Runnable() {
		t.Error("snapshot group must not be runnable itself (no Run/RunE)")
	}
	for _, name := range []string{"create", "ls", "restore"} {
		found := false
		for _, sub := range snapshotCmd.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected subcommand %q under snapshot group", name)
		}
	}
}

func TestSnapshotCreate_UsageAndArgs(t *testing.T) {
	if snapshotCreateCmd.Use != "create <task-id> [label]" {
		t.Errorf("expected Use 'create <task-id> [label]', got %q", snapshotCreateCmd.Use)
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{}); err == nil {
		t.Error("expected arg-validation error for zero args")
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{"task-1"}); err != nil {
		t.Errorf("expected one arg to be accepted, got %v", err)
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{"task-1", "label"}); err != nil {
		t.Errorf("expected two args to be accepted, got %v", err)
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{"task-1", "label", "extra"}); err == nil {
		t.Error("expected arg-validation error for three args")
	}
}

func TestSnapshotLs_AliasAndArgs(t *testing.T) {
	if snapshotLsCmd.Use != "ls <task-id>" {
		t.Errorf("expected Use 'ls <task-id>', got %q", snapshotLsCmd.Use)
	}
	if len(snapshotLsCmd.Aliases) != 1 || snapshotLsCmd.Aliases[0] != "list" {
		t.Errorf("expected alias 'list', got %v", snapshotLsCmd.Aliases)
	}
	if err := snapshotLsCmd.Args(snapshotLsCmd, []string{}); err == nil {
		t.Error("expected arg-validation error for zero args")
	}
	if err := snapshotLsCmd.Args(snapshotLsCmd, []string{"task-1"}); err != nil {
		t.Errorf("expected one arg to be accepted, got %v", err)
	}
}

func TestSnapshotRestore_ArgsAndForceFlag(t *testing.T) {
	if snapshotRestoreCmd.Use != "restore <task-id> <snapshot-name>" {
		t.Errorf("expected Use 'restore <task-id> <snapshot-name>', got %q", snapshotRestoreCmd.Use)
	}
	if err := snapshotRestoreCmd.Args(snapshotRestoreCmd, []string{"task-1"}); err == nil {
		t.Error("expected arg-validation error for one arg")
	}
	if err := snapshotRestoreCmd.Args(snapshotRestoreCmd, []string{"task-1", "snap-1"}); err != nil {
		t.Errorf("expected two args to be accepted, got %v", err)
	}
	flag := snapshotRestoreCmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag on snapshot restore")
	}
	if flag.Shorthand != "f" {
		t.Errorf("expected -f shorthand for --force, got %q", flag.Shorthand)
	}
}

func TestOldTopLevelCommandsRemoved(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		switch c.Name() {
		case "snapshots", "restore":
			t.Errorf("old top-level command %q must be removed", c.Name())
		case "snapshot":
			if c.Runnable() {
				t.Error("top-level 'snapshot' must be a group, not the old runnable create command")
			}
		}
	}
}
```

These tests are structural (command metadata, `Args` validators, flag registration) — they never execute `RunE`, so no daemon is needed. This matches the existing CLI test style (`cmd/stockyard/restart_test.go`).

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
CGO_ENABLED=0 go test ./cmd/stockyard/ -v 2>&1 | head -20
```

Expected: BUILD FAILURE — `undefined: snapshotCreateCmd`, `undefined: snapshotLsCmd`, `undefined: snapshotRestoreCmd` (the old `snapshot.go` defines only `snapshotCmd` as the create command). A compile error is the red step here; the whole package fails to build, which is expected.

- [ ] **Step 3: Rewrite `cmd/stockyard/snapshot.go` as the group**

Replace the entire contents of `cmd/stockyard/snapshot.go` with exactly this. The three `RunE` bodies are verbatim moves from the old `snapshot.go`, `snapshots.go`, and `restore.go` — only the surrounding command variables, `Use` strings, and the flag variable name (`restoreForce` → `snapshotRestoreForce`) change:

```go
// cmd/stockyard/snapshot.go
package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage task snapshots",
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create <task-id> [label]",
	Short: "Create a manual snapshot",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		label := "manual"
		if len(args) > 1 {
			label = args[1]
		}

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		fmt.Printf("Creating snapshot for %s: %s\n", taskID, label)

		snapName, err := c.CreateSnapshot(context.Background(), taskID, label)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		fmt.Printf("Snapshot created: %s\n", snapName)
		return nil
	},
}

var snapshotLsCmd = &cobra.Command{
	Use:     "ls <task-id>",
	Aliases: []string{"list"},
	Short:   "List snapshots for a task",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		snapshots, err := c.ListSnapshots(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to list snapshots: %w", err)
		}

		if len(snapshots) == 0 {
			fmt.Println("No snapshots found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCREATED")
		for _, s := range snapshots {
			fmt.Fprintf(w, "%s\t%s\n", s.Name, s.CreatedAt)
		}
		w.Flush()

		return nil
	},
}

var snapshotRestoreForce bool

var snapshotRestoreCmd = &cobra.Command{
	Use:   "restore <task-id> <snapshot-name>",
	Short: "Restore a task to a snapshot",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		snapshotName := args[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		task, err := c.GetTask(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}

		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		if !snapshotRestoreForce {
			fmt.Printf("About to restore task %s to snapshot %s\n", taskID, snapshotName)
			if task.Status == "running" {
				fmt.Printf("Warning: Task is running. Restore will stop the VM.\n")
			}
			fmt.Printf("This will roll back all changes since the snapshot.\n")
			fmt.Printf("Run with --force to confirm.\n")
			return nil
		}

		fmt.Printf("Restoring task %s to %s...\n", taskID, snapshotName)

		if err := c.RestoreSnapshot(context.Background(), taskID, snapshotName); err != nil {
			return fmt.Errorf("failed to restore: %w", err)
		}

		fmt.Println("Restored successfully.")
		return nil
	},
}

func init() {
	snapshotRestoreCmd.Flags().BoolVarP(&snapshotRestoreForce, "force", "f", false, "Force restore")
	snapshotCmd.AddCommand(snapshotCreateCmd, snapshotLsCmd, snapshotRestoreCmd)
	rootCmd.AddCommand(snapshotCmd)
}
```

- [ ] **Step 4: Delete the old command files**

Run:

```bash
git rm cmd/stockyard/snapshots.go cmd/stockyard/restore.go
```

Expected: both files removed. This must happen in the same task as Step 3 — the old files register the top-level `snapshots`/`restore` commands that `TestOldTopLevelCommandsRemoved` asserts are gone, and `restore.go`'s old `restoreForce`/`restoreCmd` variables would otherwise linger as dead code.

- [ ] **Step 5: Run the tests to verify they pass**

Run:

```bash
CGO_ENABLED=0 go test ./cmd/stockyard/ -v
```

Expected: PASS, including all five new `TestSnapshot*`/`TestOldTopLevelCommandsRemoved` tests AND every pre-existing test in the package (`TestRestartCommand_*`, `TestGarbageCollector_*`, etc. — package was green at baseline).

- [ ] **Step 6: Verify the binary builds and gofmt is clean**

Run:

```bash
CGO_ENABLED=0 go build -o bin/stockyard ./cmd/stockyard && gofmt -l cmd/stockyard/snapshot.go cmd/stockyard/snapshot_test.go
```

Expected: build succeeds; `gofmt -l` prints nothing. (Deliberately scoped to the two files this task touches — `cmd/stockyard/resources.go` is unformatted on main today; leave it alone.)

- [ ] **Step 7: Commit**

```bash
git add cmd/stockyard/snapshot.go cmd/stockyard/snapshot_test.go
git commit -m "refactor(PRI-2176): consolidate snapshot commands into 'stockyard snapshot' group

stockyard snapshot create|ls|restore replaces the flat top-level
snapshot/snapshots/restore commands. Clean break: old forms removed,
no aliases. CLI-layer only; gRPC, pkg/client, and daemon unchanged.

Co-Authored-By: <YourBobName>@<first8-of-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

(The `git rm` from Step 4 is already staged; `git add` picks up the rest.)

---

### Task 2: Docs sweep for old command forms

**Files:**
- Possibly modify: `README.md` (expected: no change needed)
- Never modify: `docs/plans/`, `docs/superpowers/plans/`, `docs/superpowers/specs/`, `docs/INITIAL_PROMPT.md` — immutable archives.

- [ ] **Step 1: Grep live docs for the old CLI forms**

Run:

```bash
grep -rn -E 'stockyard (snapshots|restore)\b|stockyard snapshot [a-z0-9<]' \
  README.md ABOUT.md CLAUDE.md docs/ \
  | grep -v 'docs/plans/' | grep -v 'docs/superpowers/' | grep -v 'docs/INITIAL_PROMPT.md'
```

Expected: **no output** (grep exiting 1 is the good outcome here — judge by output, not exit code). This was verified during planning — no live doc invokes the old CLI forms. README/docs mentions of "snapshot" refer to the in-guest `stockyard-snapshot` vsock service, ZFS internals, or research notes; those are not CLI invocations. **Do not edit them.**

- [ ] **Step 2: Handle any unexpected matches**

If (and only if) Step 1 produces hits outside the immutable archives, rewrite each to the new form (`stockyard snapshot create ...`, `stockyard snapshot ls ...`, `stockyard snapshot restore ...`) and commit:

```bash
git add README.md docs/
git commit -m "docs(PRI-2176): update snapshot command forms to new group syntax

Co-Authored-By: <YourBobName>@<first8-of-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

If there are no hits (the expected case), make no commit and move on.

---

### Task 3: Verification + live CLI smoke

No file changes. This task proves the build, the test suites, and the real CLI → daemon wiring through the new group. The smoke runs against an **isolated scratch daemon** — never the real daemon socket.

- [ ] **Step 1: Full build**

Run:

```bash
make build
```

Expected: exits 0; produces `bin/stockyard`, `bin/stockyardd`, and the guest binaries (`bin/stockyard-shell*`, `bin/stockyard-snapshot*`). Note `bin/stockyard-snapshot` is the unrelated in-guest ZFS coordinator — unchanged by this work.

- [ ] **Step 2: Run the test suites**

Run:

```bash
make test && CGO_ENABLED=0 go test ./cmd/... -v
```

Expected: both pass. (`make test` covers only `./pkg/...`; the explicit `./cmd/...` invocation is required because neither the Makefile nor CI runs CLI-package tests.)

- [ ] **Step 3: Stand up an isolated scratch daemon (verified recipe)**

This is the verified scratch-instance recipe (STOCKYARD_CONFIG_DIR isolation; `config.ConfigDir()` in `pkg/config/config.go` checks it first). Known gotchas: the daemon ignores `secrets.provider` and always tries 1Password first with file fallback (errors are swallowed — harmless); never point the CLI at the real daemon socket.

**State does not survive between separate shell invocations.** Every block in Steps 3–5 therefore re-derives its state from the fixed path `/tmp/stockyard-pri2176-smoke` and a pidfile — an empty `$SCRATCH` would make `STOCKYARD_CONFIG_DIR=""` silently fall through to the *real* config (`pkg/config/config.go:159`), and the real daemon returns the same ZFS error, falsely passing the checks. The Step 4 guard exists to catch exactly that.

First pick an existing image reference: run `container image ls` (if empty: `container system start`, then re-check) and substitute it for `IMAGE_REF` below. The smoke never boots a VM, but the daemon config wants a real ref.

```bash
SCRATCH=/tmp/stockyard-pri2176-smoke
rm -rf "$SCRATCH"
mkdir -p "$SCRATCH/secrets" "$SCRATCH/data"
IMAGE_REF="docker.io/library/stockyard-vm:container"   # <- replace with a real REFERENCE from 'container image ls'

cat > "$SCRATCH/config.json" <<EOF
{
  "instance_id": "smoke",
  "backend": "apple-container",
  "secrets": {"provider": "file", "dir": "$SCRATCH/secrets"},
  "daemon": {"socket_path": "$SCRATCH/stockyardd.sock", "data_dir": "$SCRATCH/data"},
  "http": {"enabled": false},
  "apple_container": {"image": "$IMAGE_REF"}
}
EOF

STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyardd > "$SCRATCH/daemon.log" 2>&1 &
echo $! > "$SCRATCH/daemon.pid"

# Wait for the socket (up to ~10s)
for i in $(seq 1 20); do [ -S "$SCRATCH/stockyardd.sock" ] && break; sleep 0.5; done
[ -S "$SCRATCH/stockyardd.sock" ] && echo "daemon up" || { echo "daemon failed"; cat "$SCRATCH/daemon.log"; }
```

Expected: `daemon up`.

- [ ] **Step 4: Smoke the new group (wiring + error paths, not snapshot semantics)**

ZFS snapshots are not supported on the macOS backend, so this smoke verifies command wiring and error paths only. Open the block with the guard — it stops the smoke if the scratch daemon isn't up or if `STOCKYARD_URL` is exported (env beats config in `pkg/client/resolve.go:21`, so a set `STOCKYARD_URL` would silently bypass the scratch socket — possibly to a remote ZFS daemon where snapshot commands are live):

```bash
SCRATCH=/tmp/stockyard-pri2176-smoke
[ -S "$SCRATCH/stockyardd.sock" ] || { echo "scratch daemon not up — STOP, redo Step 3"; exit 1; }
[ -z "${STOCKYARD_URL:-}" ] || { echo "STOCKYARD_URL is set and would bypass the scratch socket — unset it first"; exit 1; }

# 1. Group help lists the three subcommands
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard snapshot --help
# Expected: exit 0; "Available Commands:" shows create, ls, restore

# 2. Daemon error path through the new group (proves CLI -> gRPC -> daemon wiring)
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard snapshot ls nonexistent-task
# Expected: exit 1; error containing:
#   failed to list snapshots: rpc error: code = Unavailable
#   desc = snapshots require ZFS (not available on this backend)
# Reaching this exact daemon-side message means the request traveled the full path.

# 3. Alias works identically
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard snapshot list nonexistent-task
# Expected: identical to check 2

# 4. Arg validation on a subcommand
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard snapshot ls
# Expected: exit 1; "accepts 1 arg(s), received 0"

# 5. Old bare-create form no longer creates anything
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard snapshot some-task-id
# Expected: exit 0; prints the snapshot GROUP HELP (cobra v1.10.2 default for a
# non-runnable group given a non-subcommand arg — same behavior as
# 'stockyard image foo' today). No snapshot is created, no daemon call is made.

# 6. Old top-level 'snapshots' is gone
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard snapshots foo
# Expected: exit 1; output CONTAINS 'unknown command "snapshots" for "stockyard"'
# (cobra appends a 'Did you mean this?  snapshot' suggestion — that's fine)

# 7. Old top-level 'restore' is gone
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard restore foo bar
# Expected: exit 1; output CONTAINS 'unknown command "restore" for "stockyard"'
```

- [ ] **Step 5: Tear down the scratch instance**

```bash
SCRATCH=/tmp/stockyard-pri2176-smoke
kill "$(cat "$SCRATCH/daemon.pid")"
rm -rf "$SCRATCH"
```

Expected: daemon stops; scratch directory removed. Nothing touched the real daemon, socket, or data dir.

- [ ] **Step 6: Confirm working tree is clean and history is right**

```bash
git status --short && git log --oneline main..HEAD
```

Expected: empty status; commits from Task 1 (and Task 2 only if docs needed edits) on branch `matt/pri-2176-cli-consolidate-snapshot-commands-into-a-stockyard-snapshot`. Do not push; do not open a PR — hand back for review.

---

## Notes for the Implementer

- On a Linux/ZFS host the same smoke would exercise real snapshot semantics, but that is not required for this ticket; the macOS wiring smoke above is sufficient.
- If check 2 instead returns a connection error (`failed to connect`/`no such file`), the CLI is not finding the scratch config — confirm `STOCKYARD_CONFIG_DIR` is set on the CLI invocation, not just the daemon.
- `task.Status` in the restore body is a plain string field on `*pb.Task` — the verbatim move compiles as-is.

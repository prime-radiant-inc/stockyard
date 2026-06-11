# Multi-Image Support Phase 1 (apple-container) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-task image selection — `stockyard run --image <ref>` — on the apple-container backend, with legible rejection on Firecracker until phase 2.

**Architecture:** A new `image` string flows CLI → proto → daemon → `vmbackend.VMConfig`. The daemon resolves it before allocating any resources: empty → daemon default; non-empty → backend must implement the new optional `vmbackend.ImageValidator` interface (apple-container does; Firecracker doesn't, so it rejects). The *resolved* ref — never empty — is stored in SQLite and reported via `Task.image`.

**Tech Stack:** Go, protobuf/gRPC (`make proto`, generated code committed), cobra CLI, SQLite (modernc), Apple `container` CLI behind the existing `commandRunner` test seam.

**Spec:** `docs/superpowers/specs/2026-06-10-multi-image-design.md` (PRI-2150). Phase 2 (Firecracker registry) is a separate plan.

**Conventions:**
- Work on branch `matt/pri-2150-stockyard-multi-image-support-per-task-image-selection`.
- All tests in `pkg/vmbackend` for apple-container are darwin-only (build tag); run them on the Mac host.
- Sign commits with your Bob handle: `Co-Authored-By: <YourName@8hex> (<model>)`.
- Non-goals (do not build): dashboard exposure of image, `--command` override, registry pulls, RestartTask changes (the container's image is baked at `container run` time).

---

### Task 1: Proto `image` fields

**Files:**
- Modify: `api/stockyard.proto:22-32` (CreateTaskRequest), `api/stockyard.proto:108-118` (Task)
- Regenerate: `pkg/api/v1/stockyard.pb.go`, `pkg/api/v1/stockyard_grpc.pb.go` (committed generated code)

- [ ] **Step 1: Add `image` to CreateTaskRequest**

In `api/stockyard.proto`, change:

```proto
    repeated string ssh_authorized_keys = 8;  // SSH public keys for VM access
    bytes dotenv = 9;  // Raw .env file bytes for VM
}
```

to:

```proto
    repeated string ssh_authorized_keys = 8;  // SSH public keys for VM access
    bytes dotenv = 9;  // Raw .env file bytes for VM
    string image = 10;  // OCI image ref; empty = daemon default (PRI-2150)
}
```

- [ ] **Step 2: Add `image` to Task**

In the same file, change:

```proto
    string backend = 8;
    string vm_id = 9;
}
```

to:

```proto
    string backend = 8;
    string vm_id = 9;
    string image = 10;  // Resolved OCI image ref the task runs (PRI-2150)
}
```

- [ ] **Step 3: Regenerate and build**

Run: `cd /Users/mw/Code/prime/stockyard && make proto && CGO_ENABLED=0 go build ./...`
Expected: protoc succeeds; build succeeds; `git diff --stat` shows `api/stockyard.proto` and `pkg/api/v1/stockyard.pb.go` (message-only change — `stockyard_grpc.pb.go` only diffs if protoc versions drifted; either way is fine).

- [ ] **Step 4: Commit**

```bash
git add api/stockyard.proto pkg/api/v1/stockyard.pb.go pkg/api/v1/stockyard_grpc.pb.go
git commit -m "feat(PRI-2150): add image field to CreateTaskRequest and Task protos"
```

---

### Task 2: vmbackend seam — `VMConfig.Image` + `ImageValidator`

**Files:**
- Modify: `pkg/vmbackend/backend.go:38-49` (VMConfig), append interface at end of file (after `LogFollowerEnsurer`, which ends at line 87)

No test: declarations only. The `LogFollowerEnsurer` comment at `backend.go:75-82` explains the optional-interface pattern; `ImageValidator` follows it and must likewise live in this non-build-tagged file.

- [ ] **Step 1: Add Image to VMConfig**

In `pkg/vmbackend/backend.go`, change:

```go
	ID                string
	VCPU              int32
	MemoryMB          int32
	KernelPath        string
	RootfsPath        string // Path to this VM's writable rootfs image
```

to:

```go
	ID                string
	VCPU              int32
	MemoryMB          int32
	KernelPath        string
	RootfsPath        string // Path to this VM's writable rootfs image
	Image             string // Per-task OCI image ref; empty = backend's configured default (PRI-2150)
```

- [ ] **Step 2: Add the ImageValidator interface**

Append at end of `pkg/vmbackend/backend.go`:

```go
// ImageValidator is implemented by backends that support per-task image
// selection (VMConfig.Image). The daemon type-asserts a Backend to this
// interface when a task requests a specific image; backends that do not
// implement it cause the request to be rejected before any resources are
// allocated. Like LogFollowerEnsurer above, it lives in this non-build-
// tagged file so daemon code can reference it on all platforms.
type ImageValidator interface {
	// ValidateImage returns nil if ref is present in the backend's local
	// image store, or an error naming the ref and the available images.
	ValidateImage(ctx context.Context, ref string) error
}
```

- [ ] **Step 3: Build and commit**

Run: `CGO_ENABLED=0 go build ./pkg/vmbackend/`
Expected: success.

```bash
git add pkg/vmbackend/backend.go
git commit -m "feat(PRI-2150): add VMConfig.Image and ImageValidator backend seam"
```

---

### Task 3: apple-container honors `VMConfig.Image`

**Files:**
- Test: `pkg/vmbackend/apple_container_test.go`
- Modify: `pkg/vmbackend/apple_container.go:106-133` (buildRunArgs)

- [ ] **Step 1: Write the failing test**

Add to `pkg/vmbackend/apple_container_test.go` (model: `TestAppleContainerBackend_CreateVM_BuildsRunArgs` at line 219):

```go
func TestAppleContainerBackend_CreateVM_PerTaskImage(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["inspect"] = `[{"status":"running","networks":[{"ipv4Address":"192.168.64.5/24"}],"configuration":{"id":"stockyard-img12345"}}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{
		Image:    "stockyard-vm:container",
		StateDir: t.TempDir(),
	}, fr.run)
	b.skipLogFollower = true

	_, err := b.CreateVM(context.Background(), &VMConfig{
		ID:       "img12345",
		VCPU:     2,
		MemoryMB: 1024,
		Image:    "prudence-vm:1.2",
	})
	if err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	if len(fr.calls) == 0 {
		t.Fatal("expected at least one container call")
	}
	joined := strings.Join(fr.calls[0], " ")
	if !strings.Contains(joined, "prudence-vm:1.2") {
		t.Errorf("run args missing per-task image; got: %s", joined)
	}
	if strings.Contains(joined, "stockyard-vm:container") {
		t.Errorf("run args used backend default despite per-task image; got: %s", joined)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/vmbackend/ -run TestAppleContainerBackend_CreateVM_PerTaskImage -v`
Expected: FAIL — "run args missing per-task image" (buildRunArgs still appends `b.cfg.Image`).

- [ ] **Step 3: Implement**

In `pkg/vmbackend/apple_container.go` buildRunArgs, change:

```go
	args = append(args, b.cfg.Image)
	return args
```

to:

```go
	image := cfg.Image
	if image == "" {
		image = b.cfg.Image
	}
	args = append(args, image)
	return args
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/vmbackend/ -v`
Expected: PASS, including the pre-existing `TestAppleContainerBackend_CreateVM_BuildsRunArgs` (empty `VMConfig.Image` falls back to the backend default).

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat(PRI-2150): apple-container uses per-task image from VMConfig"
```

---

### Task 4: apple-container `ValidateImage`

**Files:**
- Modify: `pkg/vmbackend/apple_container_test.go:14-34` (fakeRunner), `pkg/vmbackend/apple_container.go` (new method)

The fake keys outputs by `args[0]`, but `container image inspect` and `container image ls` share `args[0] == "image"` — extend the fake to prefer a two-token key, backward-compatibly.

- [ ] **Step 1: Extend fakeRunner lookup**

In `pkg/vmbackend/apple_container_test.go`, change the body of `func (f *fakeRunner) run`:

```go
	sub := args[0]
	if err, ok := f.errs[sub]; ok {
		return []byte(f.outputs[sub]), err
	}
	return []byte(f.outputs[sub]), nil
```

to:

```go
	// Prefer a two-token key ("image inspect") so subcommand families like
	// `container image ...` can be scripted per verb; fall back to args[0].
	if len(args) > 1 {
		two := args[0] + " " + args[1]
		if err, ok := f.errs[two]; ok {
			return []byte(f.outputs[two]), err
		}
		if out, ok := f.outputs[two]; ok {
			return []byte(out), nil
		}
	}
	sub := args[0]
	if err, ok := f.errs[sub]; ok {
		return []byte(f.outputs[sub]), err
	}
	return []byte(f.outputs[sub]), nil
```

Run: `go test ./pkg/vmbackend/ -v` — Expected: PASS (pure extension; existing single-token keys still match).

- [ ] **Step 2: Write the failing tests**

Add to `pkg/vmbackend/apple_container_test.go` (add `"fmt"` to imports if absent):

```go
func TestAppleContainerBackend_ValidateImage_Found(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["image inspect"] = `[{"name":"prudence-vm:1.2"}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{Image: "stockyard-vm:latest"}, fr.run)

	if err := b.ValidateImage(context.Background(), "prudence-vm:1.2"); err != nil {
		t.Fatalf("ValidateImage: %v", err)
	}
	joined := strings.Join(fr.calls[0], " ")
	if !strings.Contains(joined, "image inspect prudence-vm:1.2") {
		t.Errorf("expected `image inspect <ref>` call; got: %s", joined)
	}
}

func TestAppleContainerBackend_ValidateImage_NotFound(t *testing.T) {
	fr := newFakeRunner()
	fr.errs["image inspect"] = fmt.Errorf("image not found")
	fr.outputs["image ls"] = "NAME          TAG\nstockyard-vm  latest"
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{Image: "stockyard-vm:latest"}, fr.run)

	err := b.ValidateImage(context.Background(), "nope:missing")
	if err == nil {
		t.Fatal("expected error for missing image")
	}
	for _, want := range []string{`"nope:missing"`, "stockyard-vm  latest"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./pkg/vmbackend/ -run TestAppleContainerBackend_ValidateImage -v`
Expected: compile error — `b.ValidateImage undefined`.

- [ ] **Step 4: Implement ValidateImage**

Add to `pkg/vmbackend/apple_container.go` (after `buildRunArgs`):

```go
// ValidateImage reports whether ref exists in the local `container` image
// store. On a miss the error lists the store's contents so callers can show
// what is available. Resolution is strictly on-host: no pulls, ever.
// Implements vmbackend.ImageValidator.
func (b *AppleContainerBackend) ValidateImage(ctx context.Context, ref string) error {
	if _, err := b.run(ctx, b.cfg.ContainerBin, "image", "inspect", ref); err == nil {
		return nil
	}
	available := "(could not list images)"
	if out, lsErr := b.run(ctx, b.cfg.ContainerBin, "image", "ls"); lsErr == nil {
		available = strings.TrimSpace(string(out))
	}
	return fmt.Errorf("image %q not found on host; available images:\n%s", ref, available)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/vmbackend/ -v`
Expected: PASS (all).

- [ ] **Step 6: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat(PRI-2150): apple-container ValidateImage against local image store"
```

---

### Task 5: State store persists the resolved image

**Files:**
- Modify: `pkg/daemon/state.go` — Task struct (24-37), CREATE TABLE (120-131), migrations (151-159), CreateTask INSERT (181-205), four SELECT column lists + adjacent Scans (lines 210, 283, 289, 446)
- Test: `pkg/daemon/state_test.go`

- [ ] **Step 1: Write the failing roundtrip test**

Add to `pkg/daemon/state_test.go` (it already uses `NewStateInMemory()`):

```go
func TestTaskImageRoundtrip(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	task := &Task{
		ID:        "img00001",
		Status:    "running",
		Image:     "prudence-vm:1.2",
		CreatedAt: time.Now(),
	}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, err := state.GetTask("img00001")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Image != "prudence-vm:1.2" {
		t.Errorf("Image = %q, want %q", got.Image, "prudence-vm:1.2")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/ -run TestTaskImageRoundtrip -v`
Expected: compile error — `unknown field Image in struct literal`.

- [ ] **Step 3: Implement**

In `pkg/daemon/state.go`:

a. Task struct — after `TailscaleHostname string` add:

```go
	Image             string // Resolved OCI image ref the task runs (PRI-2150)
```

b. CREATE TABLE — change `tailscale_hostname TEXT,` to:

```sql
	tailscale_hostname TEXT,
	image TEXT DEFAULT '',
```

c. Migrations slice — append:

```go
	`ALTER TABLE tasks ADD COLUMN image TEXT DEFAULT ''`,
```

d. CreateTask INSERT — change the query to:

```sql
	INSERT INTO tasks (id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, image, created_at, stopped_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
```

and in the `s.db.Exec(query, ...)` argument list insert `task.Image,` between `task.TailscaleHostname,` and `task.CreatedAt,`.

e. All four SELECTs (lines 210, 283, 289, 446) — change

```sql
	SELECT id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, created_at, stopped_at
```

to

```sql
	SELECT id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, image, created_at, stopped_at
```

and in each adjacent `.Scan(...)` insert the image destination (e.g. `&task.Image,`, matching the local variable name used there) between the `tailscale_hostname` and `created_at` destinations. The compiler will not catch a miscounted Scan — verify each Scan's argument count equals 13.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/daemon/ -v`
Expected: PASS, including pre-existing state tests (migration is additive; the DROP-COLUMN entries in the migrations slice already demonstrate error-swallowing is safe for re-runs).

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/state.go pkg/daemon/state_test.go
git commit -m "feat(PRI-2150): persist resolved task image in state store"
```

---

### Task 6: Daemon resolution + threading

**Files:**
- Modify: `pkg/daemon/tasks.go` — CreateTaskRequest (33-44), CreateTask (46-270)
- Create: `pkg/daemon/tasks_image_test.go`

- [ ] **Step 1: Write the failing tests**

Create `pkg/daemon/tasks_image_test.go`:

```go
package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type fakeImageValidator struct {
	err     error
	lastRef string
}

func (f *fakeImageValidator) ValidateImage(ctx context.Context, ref string) error {
	f.lastRef = ref
	return f.err
}

func TestResolveTaskImage_EmptyResolvesToDefault(t *testing.T) {
	got, err := resolveTaskImage(context.Background(), "", "apple-container", "stockyard-vm:latest", &fakeImageValidator{})
	if err != nil {
		t.Fatalf("resolveTaskImage: %v", err)
	}
	if got != "stockyard-vm:latest" {
		t.Errorf("resolved = %q, want default", got)
	}
}

func TestResolveTaskImage_UnsupportedBackendRejects(t *testing.T) {
	_, err := resolveTaskImage(context.Background(), "prudence-vm:1.2", "firecracker", "default", nil)
	if err == nil {
		t.Fatal("expected rejection when backend lacks ImageValidator")
	}
	want := "firecracker backend does not support per-task images yet (PRI-2150 phase 2)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want substring %q", err, want)
	}
}

func TestResolveTaskImage_ValidatorMissPropagates(t *testing.T) {
	v := &fakeImageValidator{err: fmt.Errorf(`image "nope" not found`)}
	_, err := resolveTaskImage(context.Background(), "nope", "apple-container", "stockyard-vm:latest", v)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected validator error to propagate, got %v", err)
	}
	if v.lastRef != "nope" {
		t.Errorf("validator called with %q, want \"nope\"", v.lastRef)
	}
}

func TestResolveTaskImage_ValidRequestResolves(t *testing.T) {
	got, err := resolveTaskImage(context.Background(), "prudence-vm:1.2", "apple-container", "stockyard-vm:latest", &fakeImageValidator{})
	if err != nil {
		t.Fatalf("resolveTaskImage: %v", err)
	}
	if got != "prudence-vm:1.2" {
		t.Errorf("resolved = %q, want requested ref", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/daemon/ -run TestResolveTaskImage -v`
Expected: compile error — `undefined: resolveTaskImage`.

- [ ] **Step 3: Implement resolveTaskImage**

Add to `pkg/daemon/tasks.go` (before CreateTask):

```go
// resolveTaskImage turns a per-task image request into the ref the task will
// actually run — never the empty string, so the stored record always names a
// real image. A nil validator means the backend cannot honor per-task images
// (PRI-2150 phase 1: only apple-container can).
func resolveTaskImage(ctx context.Context, requested, backendName, defaultImage string, validator vmbackend.ImageValidator) (string, error) {
	if requested == "" {
		return defaultImage, nil
	}
	if validator == nil {
		return "", fmt.Errorf("%s backend does not support per-task images yet (PRI-2150 phase 2)", backendName)
	}
	if err := validator.ValidateImage(ctx, requested); err != nil {
		return "", err
	}
	return requested, nil
}
```

Run: `go test ./pkg/daemon/ -run TestResolveTaskImage -v`
Expected: PASS (4 tests).

- [ ] **Step 4: Add Image to the daemon request and wire resolution into CreateTask**

a. In `CreateTaskRequest` (tasks.go:34-44), after `DotEnv []byte ...` add:

```go
	Image             string   // OCI image ref; empty = daemon default (PRI-2150)
```

b. In `CreateTask`, immediately after the defaults block (`if req.MemoryMB <= 0 { req.MemoryMB = 1024 }`, before `taskID := vmbackend.GenerateVMID()`), insert — fail fast, before any IP/ZFS/VM allocation:

```go
	// Resolve the task's image before allocating anything (PRI-2150).
	backendName := tm.daemon.cfg.Backend
	if backendName == "" {
		backendName = "firecracker"
	}
	defaultImage := "default" // Firecracker's registry name arrives in phase 2
	if backendName == "apple-container" {
		defaultImage = tm.daemon.cfg.AppleContainer.Image
	}
	validator, _ := tm.backend.(vmbackend.ImageValidator)
	resolvedImage, err := resolveTaskImage(ctx, req.Image, backendName, defaultImage, validator)
	if err != nil {
		return nil, err
	}
```

c. In the `vmCfg := &vmbackend.VMConfig{...}` literal (tasks.go:172-181), after `MemoryMB: req.MemoryMB,` add:

```go
			Image:             resolvedImage,
```

d. In the `task := &Task{...}` literal (tasks.go:~206-218), after `TailscaleHostname: tailscaleHostname,` add:

```go
		Image:             resolvedImage,
```

- [ ] **Step 5: Run tests to verify everything passes**

Run: `CGO_ENABLED=0 go build ./... && go test ./pkg/daemon/ -v`
Expected: build OK; PASS. (Note: `err` is shadowed throughout CreateTask; the inserted block declares its own `err` via `:=` — if the compiler complains about redeclaration, use `resolvedImage, imgErr := ...` and check `imgErr`.)

- [ ] **Step 6: Commit**

```bash
git add pkg/daemon/tasks.go pkg/daemon/tasks_image_test.go
git commit -m "feat(PRI-2150): daemon resolves and validates per-task image before allocation"
```

---

### Task 7: gRPC threading

**Files:**
- Modify: `pkg/daemon/grpc.go:29-55` (CreateTask handler), `pkg/daemon/grpc.go:189-204` (taskToProto)
- Test: `pkg/daemon/grpc_tasktoproto_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pkg/daemon/grpc_tasktoproto_test.go` (match the file's existing test style):

```go
func TestTaskToProto_Image(t *testing.T) {
	pt := taskToProto(&Task{ID: "img00001", Image: "prudence-vm:1.2"}, "apple-container")
	if pt.Image != "prudence-vm:1.2" {
		t.Errorf("Image = %q, want %q", pt.Image, "prudence-vm:1.2")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/ -run TestTaskToProto_Image -v`
Expected: FAIL — `pt.Image` is empty (field exists on the proto since Task 1, but taskToProto doesn't set it).

- [ ] **Step 3: Implement**

a. In the CreateTask handler's `&CreateTaskRequest{...}` literal, after `DotEnv: req.Dotenv,` add:

```go
		Image:             req.Image,
```

b. In `taskToProto`, after `VmId: t.VMID,` add:

```go
		Image:             t.Image,
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/daemon/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/grpc.go pkg/daemon/grpc_tasktoproto_test.go
git commit -m "feat(PRI-2150): thread image through gRPC create and task conversion"
```

---

### Task 8: CLI — `--image` flag and IMAGE column

**Files:**
- Modify: `cmd/stockyard/run.go:34-88` (request), `cmd/stockyard/run.go:128-137` (flags), `cmd/stockyard/list.go:36-45` (table)

No unit test: pure flag plumbing, covered by the e2e smoke (Task 11). The repo has no CLI-flag test precedent to follow.

- [ ] **Step 1: Add the flag**

In `cmd/stockyard/run.go`, alongside the other `runX` vars add:

```go
var runImage string
```

In `init()`, after the `--env-file` line add:

```go
	runCmd.Flags().StringVar(&runImage, "image", "", "OCI image ref for the task (default: daemon-configured image)")
```

In the `&pb.CreateTaskRequest{...}` literal, after `Dotenv: dotenv,` add:

```go
		Image:             runImage,
```

- [ ] **Step 2: Show the image in `stockyard list`**

In `cmd/stockyard/list.go`, change:

```go
		fmt.Fprintln(w, "ID\tNAME\tSTATUS\tCREATED")
```

to:

```go
		fmt.Fprintln(w, "ID\tNAME\tSTATUS\tIMAGE\tCREATED")
```

and change:

```go
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				t.Id, name, t.Status, t.CreatedAt)
```

to:

```go
			image := t.Image
			if image == "" {
				image = "-" // task predates PRI-2150
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				t.Id, name, t.Status, image, t.CreatedAt)
```

- [ ] **Step 3: Build and commit**

Run: `CGO_ENABLED=0 go build -o bin/stockyard ./cmd/stockyard && ./bin/stockyard run --help`
Expected: help text lists `--image`.

```bash
git add cmd/stockyard/run.go cmd/stockyard/list.go
git commit -m "feat(PRI-2150): stockyard run --image flag and IMAGE column in list"
```

---

### Task 9: Image contract doc

**Files:**
- Create: `docs/image-contract.md`

- [ ] **Step 1: Write the doc**

Create `docs/image-contract.md` with exactly this content:

```markdown
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
```

- [ ] **Step 2: Commit**

```bash
git add docs/image-contract.md
git commit -m "docs(PRI-2150): image contract for per-task images"
```

---

### Task 10: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Full test suite**

Run: `cd /Users/mw/Code/prime/stockyard && make test`
Expected: all packages PASS.

- [ ] **Step 2: Cross-compile for Linux**

The daemon must still build for the Firecracker host — this catches darwin-only symbols leaking into shared code:

Run: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...`
Expected: success, no output.

- [ ] **Step 3: Full local build**

Run: `make build`
Expected: binaries in `bin/`.

---

### Task 11: macOS e2e smoke (required before push)

**Files:** none (manual verification; scratch state under `/tmp/stockyard-smoke`)

Standing rule: `make test` + cross-compile is NOT enough — start the daemon and drive the CLI. Use a scratch instance config; `STOCKYARD_DATA_DIR` alone does not isolate the daemon.

- [ ] **Step 1: Preconditions**

Run: `container system status && container image ls`
Expected: service running; note an existing image ref to use as the default (call it `$DEFAULT_REF` below; e.g. `stockyard-vm:latest`). If the service is down: `container system start`.

- [ ] **Step 2: Scratch instance config**

```bash
mkdir -p /tmp/stockyard-smoke/secrets /tmp/stockyard-smoke/data
cat > /tmp/stockyard-smoke/config.json <<EOF
{
  "instance_id": "smoke",
  "backend": "apple-container",
  "secrets": {"provider": "file", "dir": "/tmp/stockyard-smoke/secrets"},
  "daemon": {"socket_path": "/tmp/stockyard-smoke/stockyardd.sock", "data_dir": "/tmp/stockyard-smoke/data"},
  "http": {"enabled": false},
  "apple_container": {"image": "$DEFAULT_REF"}
}
EOF
```

- [ ] **Step 3: Tag a second image and start the daemon**

```bash
container image tag "$DEFAULT_REF" stockyard-vm:smoke
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke ./bin/stockyardd & DAEMON_PID=$!
sleep 2
```

Expected: daemon starts; log shows `Daemon listening on /tmp/stockyard-smoke/stockyardd.sock` — the socket path proves the scratch config was honored (nothing logs the backend name).

Note: the daemon ignores `secrets.provider` and always tries 1Password first with a file-provider fallback (`cmd/stockyardd/main.go:40-46`), so each `run` may exec `op read ...`; errors are swallowed, but a locked 1Password app can pop auth prompts or add latency. Harmless to the smoke — don't chase it.

- [ ] **Step 4: Happy path — per-task image**

```bash
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke ./bin/stockyard run --name smoke1 --no-tailscale --image stockyard-vm:smoke
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke ./bin/stockyard list
```

Expected: task created; `list` shows IMAGE column with `stockyard-vm:smoke`.
Also verify the default path: `... run --name smoke2 --no-tailscale` (no `--image`) → list shows `$DEFAULT_REF` (the resolved default, not `-`).

- [ ] **Step 5: Negative path — legible miss**

```bash
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke ./bin/stockyard run --name smoke3 --no-tailscale --image nope:missing
```

Expected: fails fast with `image "nope:missing" not found on host; available images:` followed by the store listing. No task appears in `list`.

- [ ] **Step 6: Cleanup**

`destroy` takes task IDs (`cmd/stockyard/destroy.go:14`); read them from `list` first:

```bash
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke ./bin/stockyard list
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke ./bin/stockyard destroy --force <id-of-smoke1>
STOCKYARD_CONFIG_DIR=/tmp/stockyard-smoke ./bin/stockyard destroy --force <id-of-smoke2>
kill $DAEMON_PID
container image rm stockyard-vm:smoke || true
rm -rf /tmp/stockyard-smoke
```

- [ ] **Step 7: Record results**

Note pass/fail of steps 4–5 for the PR description and the PRI-2150 In-Review comment. The Firecracker rejection path cannot be smoked on macOS; its unit test (Task 6) covers it until phase 2's Linux smoke.

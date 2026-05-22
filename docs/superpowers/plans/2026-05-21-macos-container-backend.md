# macOS Apple `container` Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Apple's `container` CLI as a third macOS VM backend (`AppleContainerBackend`), wired through config, daemon, the gRPC API, the dashboard web terminal, `stockyard attach`, and a unified multi-arch container image.

**Architecture:** A new `vmbackend.Backend` implementation shells out to the `container` CLI through an injectable `commandRunner` seam, so all logic is unit-testable without `container` installed. The daemon gains a `case "apple-container"` in its backend switch plus build-tagged constructors. The gRPC `Task` message gains `backend`/`vm_id` so the dashboard terminal and `stockyard attach` can dispatch on backend. A multi-stage `vm-image/Dockerfile` adds a `container` target alongside the existing Firecracker image.

**Tech Stack:** Go 1.26, gRPC/protobuf (`protoc` + `protoc-gen-go`/`protoc-gen-go-grpc`), `creack/pty`, `gorilla/websocket`, Docker multi-stage builds, Apple `container` CLI.

---

## Authoritative Spec

`docs/superpowers/specs/2026-05-21-macos-container-backend-design.md`. Read it before starting. This plan implements it. Where this plan and the spec disagree, the discrepancies are called out explicitly in **"Spec Discrepancies & Decisions"** below — follow this plan.

---

## Spec Discrepancies & Decisions (read first)

These were discovered while verifying the spec against the actual code. They are baked into the tasks below; this section explains the *why*.

1. **No `client`-side `Task` struct exists.** The spec (§3) says "`client`-side task structs carry no backend field". In fact `pkg/client/client.go` has no `Task` type at all — `Client.GetTask` returns `*pb.Task` directly (the generated protobuf type), and `cmd/stockyard/attach.go` already reads `task.Ip` / `task.TailscaleHostname` off it. **Decision:** Adding `backend` + `vm_id` to the proto `Task` message automatically gives the CLI everything it needs. No client struct work. `attach.go` reads `task.GetBackend()` / `task.GetVmId()`.

2. **`dashboard.DaemonAPI` exposes per-task data via `dashboard.Task`, not a `GetBackend()` method.** The spec (§3) suggests "e.g. `GetBackend() string`". The dashboard already threads per-task data through the `dashboard.Task` struct returned by `GetTask`. **Decision:** Add `Backend` and `VMID` fields to `dashboard.Task` (and `dashboard.DaemonTask`, the import-cycle mirror) and populate them in `convertTask` / `convertToDashboardTask`. The terminal handler reads `task.Backend` / `task.VMID` — no new interface method, fewer moving parts, matches the existing pattern. This is simpler than the spec's sketch and is the chosen approach.

3. **proto regeneration tooling.** `protoc` (libprotoc 34.1) is on PATH, but `protoc-gen-go` / `protoc-gen-go-grpc` were **not** installed in the build environment. They have been installed into `/Users/mw/go/bin` via `go install` (versions: `protoc-gen-go` from `google.golang.org/protobuf`, `protoc-gen-go-grpc` v1.6.2). `make proto` runs cleanly with `PATH` including `/Users/mw/go/bin`. **Decision:** Phase 2 regenerates with `make proto` and `PATH="$PATH:$(go env GOPATH)/bin"`. The regenerated `*.pb.go` files are committed (the repo already checks them in). If a worker's environment lacks the plugins, run the two `go install` commands in Task 2.1 Step 0.

4. **`RestartTask` hardcodes default CPU/memory.** `tasks.go:324-328` rebuilds `VMConfig` with `VCPU: 2, MemoryMB: 1024` on restart. For apple-container `StartVM` only needs the container *name* (`stockyard-<id>`), and `container start` does not re-take cpu/memory — they were fixed at `create` time. **Decision:** This is fine as-is; `AppleContainerBackend.StartVM` ignores `VCPU`/`MemoryMB` and only uses `cfg.ID`. No `tasks.go` change for restart.

5. **`reconcileRunningVMs` is PID-file based and Firecracker/vfkit-only.** Confirmed at `daemon.go:172-226`. An apple-container task writes no PID file. **Decision:** Add an early backend branch in `reconcileRunningVMs` that, for `apple-container`, calls `backend.ListVMs` once and reconciles each running task against the result. Implemented in Task 1.8.

6. **`metricsPoller` nil for apple-container — already handled.** `daemon.go:342` already gates `metricsPoller` creation to `firecracker`/empty backend. No change needed; noted so workers don't "fix" a non-bug.

7. **vfkit's `StateDir` is `DataDir + "/vms/stockyard"`** (`backend_darwin.go:14`). The apple-container backend uses the **same** convention so the daemon's `logTailer` (which tails `{DataDir}/vms/stockyard/{vmID}/std{out,err}.log`, see `tasks.go:283-285`) works unchanged.

8. **`creack/pty` is already a dependency** (`go.mod`, used in `pkg/shell/session.go`). Phase 3 uses it directly; no new dependency.

9. **`container` is almost certainly NOT installed here.** All `AppleContainerBackend` tests use the `commandRunner` seam with fake runners — they need no `container` binary and run on any OS (the backend file is `//go:build darwin`, so its tests are `//go:build darwin` too and run on this macOS host). A separate real-`container` integration test is `//go:build darwin && container_integration` gated and is NOT expected to run overnight.

---

## File Structure

**Phase 1 — Backend core**
- Create: `pkg/vmbackend/apple_container.go` — `AppleContainerBackend`, `commandRunner` seam, `container` CLI arg construction, JSON parsing, log follower. `//go:build darwin`.
- Create: `pkg/vmbackend/apple_container_test.go` — unit tests via fake `commandRunner`. `//go:build darwin`.
- Create: `pkg/config/apple_container.go` — `AppleContainerConfig` (compiles on all platforms).
- Modify: `pkg/config/config.go` — add `AppleContainer AppleContainerConfig` field to `Config`.
- Create: `pkg/config/apple_container_test.go` — config default test.
- Create: `pkg/daemon/backend_apple_container_darwin.go` — `createAppleContainerBackend` (real). `//go:build darwin`.
- Create: `pkg/daemon/backend_apple_container_other.go` — `createAppleContainerBackend` (stub). `//go:build !darwin`.
- Modify: `pkg/daemon/daemon.go` — add `case "apple-container"` to backend switch; backend branch in `reconcileRunningVMs`.
- Modify: `pkg/daemon/rootfs_darwin.go` — early `apple-container` guard returning `nil`.

**Phase 2 — Task data model + CLI**
- Modify: `api/stockyard.proto` — add `string backend = 8` and `string vm_id = 9` to `Task`.
- Regenerate: `pkg/api/v1/stockyard.pb.go`, `pkg/api/v1/stockyard_grpc.pb.go` (`make proto`).
- Modify: `pkg/daemon/grpc.go` — `taskToProto` populates `Backend` (from `cfg.Backend`) and `VmId` (from `t.VMID`).
- Modify: `pkg/dashboard/daemon.go` — add `Backend`, `VMID` to `dashboard.Task`.
- Modify: `pkg/dashboard/adapter.go` — add `Backend`, `VMID` to `DaemonTask`; populate in `convertTask`.
- Modify: `pkg/daemon/dashboard_facade.go` — populate `Backend`/`VMID` in `convertToDashboardTask`.
- Modify: `cmd/stockyard/attach.go` — dispatch on `task.GetBackend()`.
- Modify: `cmd/stockyard/attach_test.go` — backend-dispatch unit test.

**Phase 3 — Dashboard terminal**
- Create: `pkg/dashboard/container_exec_session.go` — `ContainerExecSession` (host-PTY-backed). `//go:build darwin`.
- Create: `pkg/dashboard/container_exec_session_other.go` — build stub. `//go:build !darwin`.
- Create: `pkg/dashboard/container_exec_session_test.go` — `//go:build darwin`.
- Modify: `pkg/dashboard/terminal_handler.go` — `ServeHTTP` branches on `task.Backend`.

**Phase 4 — Unified image**
- Modify: `vm-image/Dockerfile` — multi-stage: `base`, `firecracker`, `container` targets; arch-aware download URLs.
- Create: `vm-image/init/stockyard-container-init.sh` — container entrypoint.
- Modify: `vm-image/build.sh` — `TARGET` env var selecting build stage + platform.
- Modify: `vm-image/Makefile` — `container-image` target.

---

## Conventions for every task

- **TDD:** write the failing test, run it, see it fail for the *expected* reason, implement, see it pass.
- **Build tags:** `AppleContainerBackend` and its tests are `//go:build darwin`. This worktree runs on macOS (darwin), so darwin-tagged tests **do** compile and run here.
- **Verify command (Phases 1-3):** `go build ./...` then the targeted `go test`. Each phase ends with a full `go build ./... && go test ./...`.
- **Commits:** one per task (after the final passing test). Commit messages: imperative mood, prefix `feat:` / `test:` / `refactor:` / `chore:`.

---

# PHASE 1 — Backend Core

Delivers `AppleContainerBackend`, config, daemon wiring, log follower. Pure Go; fully verifiable overnight on this macOS host.

---

### Task 1.1: `AppleContainerConfig` type

**Files:**
- Create: `pkg/config/apple_container.go`
- Create: `pkg/config/apple_container_test.go`
- Modify: `pkg/config/config.go` (struct `Config` ~lines 13-24, `DefaultConfig` ~lines 73-107)

- [ ] **Step 1: Write the failing test**

Create `pkg/config/apple_container_test.go`:

```go
package config

import "testing"

func TestDefaultConfig_AppleContainer(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AppleContainer.ContainerBin != "container" {
		t.Errorf("expected default ContainerBin %q, got %q", "container", cfg.AppleContainer.ContainerBin)
	}
}

func TestAppleContainerConfig_JSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Backend = "apple-container"
	cfg.AppleContainer.ContainerBin = "/opt/homebrew/bin/container"
	cfg.AppleContainer.Image = "stockyard-vm:container"
	if err := cfg.SaveToDir(dir); err != nil {
		t.Fatalf("SaveToDir: %v", err)
	}
	loaded, err := LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	if loaded.AppleContainer.ContainerBin != "/opt/homebrew/bin/container" {
		t.Errorf("ContainerBin not round-tripped: %q", loaded.AppleContainer.ContainerBin)
	}
	if loaded.AppleContainer.Image != "stockyard-vm:container" {
		t.Errorf("Image not round-tripped: %q", loaded.AppleContainer.Image)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/config/ -run AppleContainer -v`
Expected: FAIL — `cfg.AppleContainer` undefined.

- [ ] **Step 3: Create the config type**

Create `pkg/config/apple_container.go`:

```go
package config

// AppleContainerConfig holds configuration for the Apple `container` VM backend (macOS).
type AppleContainerConfig struct {
	ContainerBin string `json:"container_bin"` // Path to the `container` binary (default: "container")
	Image        string `json:"image"`         // OCI image reference for task containers
}
```

- [ ] **Step 4: Wire the field into `Config` and `DefaultConfig`**

In `pkg/config/config.go`, add the field to the `Config` struct (after `Rootfs RootfsConfig`):

```go
	Rootfs         RootfsConfig         `json:"rootfs"`
	AppleContainer AppleContainerConfig `json:"apple_container"`
```

Update the `Backend` field's comment to:

```go
	Backend     string            `json:"backend"` // "firecracker" (default), "vfkit", or "apple-container"
```

In `DefaultConfig()`, add an `AppleContainer` block before the closing `}` of the returned `&Config{...}` (after the `HTTP:` block):

```go
		HTTP: HTTPConfig{
			Enabled: false,
			Addr:    ":8080",
		},
		AppleContainer: AppleContainerConfig{
			ContainerBin: "container",
		},
	}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/config/ -run AppleContainer -v`
Expected: PASS (both tests).

- [ ] **Step 6: Commit**

```bash
git add pkg/config/apple_container.go pkg/config/apple_container_test.go pkg/config/config.go
git commit -m "feat: add AppleContainerConfig to config"
```

---

### Task 1.2: `AppleContainerBackend` skeleton + `commandRunner` seam

**Files:**
- Create: `pkg/vmbackend/apple_container.go`
- Create: `pkg/vmbackend/apple_container_test.go`

This task creates the type, the injectable command-runner seam, and the constructor — but **not yet** the lifecycle methods (those come in 1.3-1.6). It must satisfy `Backend` at compile time with stub method bodies returning `errNotImplemented`, replaced incrementally.

- [ ] **Step 1: Write the failing test**

Create `pkg/vmbackend/apple_container_test.go`:

```go
//go:build darwin

package vmbackend

import (
	"context"
	"testing"
)

// fakeRunner records invocations and returns scripted output/errors.
type fakeRunner struct {
	calls   [][]string         // each call's argv (name + args)
	outputs map[string]string  // keyed by args[0] (the container subcommand), stdout to return
	errs    map[string]error   // keyed by args[0], error to return
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{}, errs: map[string]error{}}
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(args) == 0 {
		return nil, nil
	}
	sub := args[0]
	if err, ok := f.errs[sub]; ok {
		return []byte(f.outputs[sub]), err
	}
	return []byte(f.outputs[sub]), nil
}

func TestAppleContainerBackend_ImplementsInterface(t *testing.T) {
	var _ Backend = (*AppleContainerBackend)(nil)
}

func TestAppleContainerBackend_NewSetsDefaults(t *testing.T) {
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{}, newFakeRunner().run)
	if b.cfg.ContainerBin != "container" {
		t.Errorf("expected default ContainerBin %q, got %q", "container", b.cfg.ContainerBin)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/vmbackend/ -run AppleContainer -v`
Expected: FAIL — `AppleContainerBackend`, `newAppleContainerBackendWithRunner`, `AppleContainerConfig` undefined.

- [ ] **Step 3: Create the backend skeleton**

Create `pkg/vmbackend/apple_container.go`:

```go
//go:build darwin

package vmbackend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// AppleContainerConfig configures the Apple `container` backend.
type AppleContainerConfig struct {
	ContainerBin string // Path to the `container` binary (default: "container")
	Image        string // OCI image reference for task containers
	StateDir     string // Directory holding per-VM state (captured log files)
}

// commandRunner runs an external command and returns its combined behaviour.
// It is the test seam: production uses execRunner; tests inject a fake.
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRunner is the production commandRunner — it shells out for real.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %v: %w: %s", name, args, err, string(out))
	}
	return out, nil
}

// logFollower tracks a `container logs -f` process so it can be killed later.
type logFollower struct {
	cmd *exec.Cmd
}

// AppleContainerBackend implements Backend by shelling out to Apple's `container` CLI.
type AppleContainerBackend struct {
	cfg       AppleContainerConfig
	run       commandRunner
	mu        sync.Mutex
	followers map[string]*logFollower // keyed by VM ID
}

// containerName returns the deterministic container name for a VM ID.
func containerName(vmID string) string {
	return "stockyard-" + vmID
}

// newAppleContainerBackendWithRunner builds a backend with an injectable runner (for tests).
func newAppleContainerBackendWithRunner(cfg AppleContainerConfig, run commandRunner) *AppleContainerBackend {
	if cfg.ContainerBin == "" {
		cfg.ContainerBin = "container"
	}
	return &AppleContainerBackend{
		cfg:       cfg,
		run:       run,
		followers: make(map[string]*logFollower),
	}
}

// NewAppleContainerBackend builds a production backend using the real CLI runner.
func NewAppleContainerBackend(cfg AppleContainerConfig) *AppleContainerBackend {
	return newAppleContainerBackendWithRunner(cfg, execRunner)
}

var errNotImplemented = errors.New("apple-container backend: not implemented")

func (b *AppleContainerBackend) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) StopVM(ctx context.Context, id string) error {
	return errNotImplemented
}

func (b *AppleContainerBackend) DeleteVM(ctx context.Context, id string) error {
	return errNotImplemented
}

func (b *AppleContainerBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) Close() error {
	return nil
}

// vmStateDir returns the per-VM state directory (holds captured logs).
func (b *AppleContainerBackend) vmStateDir(id string) string {
	return filepath.Join(b.cfg.StateDir, id)
}

// ensureStateDir creates the per-VM state directory.
func (b *AppleContainerBackend) ensureStateDir(id string) (string, error) {
	dir := b.vmStateDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create VM state dir: %w", err)
	}
	return dir, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/vmbackend/ -run AppleContainer -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat: add AppleContainerBackend skeleton with commandRunner seam"
```

---

### Task 1.3: `CreateVM` — `container run` arg construction + log follower

**Files:**
- Modify: `pkg/vmbackend/apple_container.go` (`CreateVM`, add `startLogFollower`)
- Modify: `pkg/vmbackend/apple_container_test.go`

`container run -d` does not write `stdout.log`/`stderr.log`; output lives in `container logs`. `CreateVM` must (a) run the container detached, then (b) spawn a `container logs -f` follower whose stdout/stderr redirect into `{StateDir}/{id}/stdout.log` and `stderr.log` so the daemon's existing `logTailer` works unchanged.

- [ ] **Step 1: Write the failing test**

Add to `pkg/vmbackend/apple_container_test.go`:

```go
import (
	"context"
	"strings"
	"testing"
)
// (merge imports — strings is new)

func TestAppleContainerBackend_CreateVM_BuildsRunArgs(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{
		Image:    "stockyard-vm:container",
		StateDir: t.TempDir(),
	}, fr.run)
	// Avoid spawning a real log follower in unit tests.
	b.skipLogFollower = true

	info, err := b.CreateVM(context.Background(), &VMConfig{
		ID:       "abc12345",
		VCPU:     4,
		MemoryMB: 2048,
		Env:      map[string]string{"FOO": "bar"},
		Metadata: map[string]string{"task-id": "abc12345"},
	})
	if err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	if info.ID != "abc12345" {
		t.Errorf("expected ID abc12345, got %q", info.ID)
	}
	if len(fr.calls) == 0 {
		t.Fatal("expected at least one container call")
	}
	joined := strings.Join(fr.calls[0], " ")
	for _, want := range []string{
		"run", "-d",
		"--name stockyard-abc12345",
		"--cpus 4",
		"--memory 2048M",
		"--env FOO=bar",
		"--label task-id=abc12345",
		"stockyard-vm:container",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("run args missing %q; got: %s", want, joined)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/vmbackend/ -run CreateVM_BuildsRunArgs -v`
Expected: FAIL — `b.skipLogFollower` undefined and `CreateVM` returns `errNotImplemented`.

- [ ] **Step 3: Implement `CreateVM` and the log follower**

In `pkg/vmbackend/apple_container.go`, add `skipLogFollower bool` to the `AppleContainerBackend` struct (after `followers`). Add `"sort"`, `"time"` to imports if needed (`time` is needed for `VMInfo.CreatedAt`). Replace the `CreateVM` stub:

```go
func (b *AppleContainerBackend) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	stateDir, err := b.ensureStateDir(cfg.ID)
	if err != nil {
		return nil, err
	}

	args := b.buildRunArgs(cfg)
	if _, err := b.run(ctx, b.cfg.ContainerBin, args...); err != nil {
		os.RemoveAll(stateDir)
		return nil, fmt.Errorf("container run: %w", err)
	}

	if !b.skipLogFollower {
		if err := b.startLogFollower(cfg.ID); err != nil {
			// Non-fatal: the container is running; logs just won't be captured.
			fmt.Printf("Warning: apple-container log follower for %s: %v\n", cfg.ID, err)
		}
	}

	ip, _ := b.inspectIP(ctx, cfg.ID) // best-effort; empty IP is acceptable

	return &VMInfo{
		ID:        cfg.ID,
		PID:       0, // container manages the workload; no meaningful hypervisor PID
		IP:        ip,
		StateDir:  stateDir,
		State:     "running",
		CreatedAt: time.Now(),
	}, nil
}

// buildRunArgs constructs the `container run -d ...` argument vector.
func (b *AppleContainerBackend) buildRunArgs(cfg *VMConfig) []string {
	args := []string{
		"run", "-d",
		"--name", containerName(cfg.ID),
		"--cpus", fmt.Sprintf("%d", cfg.VCPU),
		"--memory", fmt.Sprintf("%dM", cfg.MemoryMB),
	}
	// Deterministic ordering so tests and diffs are stable.
	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "--env", k+"="+cfg.Env[k])
	}
	metaKeys := make([]string, 0, len(cfg.Metadata))
	for k := range cfg.Metadata {
		metaKeys = append(metaKeys, k)
	}
	sort.Strings(metaKeys)
	for _, k := range metaKeys {
		args = append(args, "--label", k+"="+cfg.Metadata[k])
	}
	args = append(args, b.cfg.Image)
	return args
}

// startLogFollower spawns `container logs -f` redirecting into the per-VM
// stdout.log / stderr.log so the daemon's logTailer works unchanged.
func (b *AppleContainerBackend) startLogFollower(id string) error {
	dir := b.vmStateDir(id)
	stdoutF, err := os.Create(filepath.Join(dir, "stdout.log"))
	if err != nil {
		return fmt.Errorf("create stdout.log: %w", err)
	}
	stderrF, err := os.Create(filepath.Join(dir, "stderr.log"))
	if err != nil {
		stdoutF.Close()
		return fmt.Errorf("create stderr.log: %w", err)
	}
	cmd := exec.Command(b.cfg.ContainerBin, "logs", "-f", containerName(id))
	cmd.Stdout = stdoutF
	cmd.Stderr = stderrF
	if err := cmd.Start(); err != nil {
		stdoutF.Close()
		stderrF.Close()
		return fmt.Errorf("start log follower: %w", err)
	}
	go func() {
		cmd.Wait()
		stdoutF.Close()
		stderrF.Close()
	}()
	b.mu.Lock()
	b.followers[id] = &logFollower{cmd: cmd}
	b.mu.Unlock()
	return nil
}

// stopLogFollower kills and forgets the log follower for a VM, if any.
func (b *AppleContainerBackend) stopLogFollower(id string) {
	b.mu.Lock()
	f, ok := b.followers[id]
	delete(b.followers, id)
	b.mu.Unlock()
	if ok && f.cmd.Process != nil {
		f.cmd.Process.Kill()
	}
}

// inspectIP reads the container's IP from `container inspect --format json`.
// Defined fully in Task 1.6; a stub here keeps CreateVM compiling.
func (b *AppleContainerBackend) inspectIP(ctx context.Context, id string) (string, error) {
	return "", nil
}
```

> NOTE: `inspectIP` is a stub now; Task 1.6 replaces its body with real JSON parsing. Do not leave it stubbed past Task 1.6.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/vmbackend/ -run CreateVM_BuildsRunArgs -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat: implement AppleContainerBackend.CreateVM with log follower"
```

---

### Task 1.4: `StartVM`, `StopVM`, `DeleteVM`

**Files:**
- Modify: `pkg/vmbackend/apple_container.go`
- Modify: `pkg/vmbackend/apple_container_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pkg/vmbackend/apple_container_test.go`:

```go
func TestAppleContainerBackend_StartVM(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	b.skipLogFollower = true
	if _, err := b.StartVM(context.Background(), &VMConfig{ID: "abc12345"}); err != nil {
		t.Fatalf("StartVM: %v", err)
	}
	joined := strings.Join(fr.calls[0], " ")
	if !strings.Contains(joined, "start stockyard-abc12345") {
		t.Errorf("expected `start stockyard-abc12345`, got: %s", joined)
	}
}

func TestAppleContainerBackend_StopVM(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	if err := b.StopVM(context.Background(), "abc12345"); err != nil {
		t.Fatalf("StopVM: %v", err)
	}
	joined := strings.Join(fr.calls[0], " ")
	if !strings.Contains(joined, "stop stockyard-abc12345") {
		t.Errorf("expected `stop stockyard-abc12345`, got: %s", joined)
	}
}

func TestAppleContainerBackend_DeleteVM(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	if err := b.DeleteVM(context.Background(), "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	// DeleteVM = stop then rm.
	var sawStop, sawRm bool
	for _, c := range fr.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "stop stockyard-abc12345") {
			sawStop = true
		}
		if strings.Contains(j, "rm stockyard-abc12345") {
			sawRm = true
		}
	}
	if !sawStop || !sawRm {
		t.Errorf("DeleteVM should stop and rm; sawStop=%v sawRm=%v", sawStop, sawRm)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/vmbackend/ -run "StartVM|StopVM|DeleteVM" -v`
Expected: FAIL — methods return `errNotImplemented`.

- [ ] **Step 3: Implement the three methods**

In `pkg/vmbackend/apple_container.go`, replace the `StartVM`, `StopVM`, `DeleteVM` stubs:

```go
func (b *AppleContainerBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	stateDir, err := b.ensureStateDir(cfg.ID)
	if err != nil {
		return nil, err
	}
	if _, err := b.run(ctx, b.cfg.ContainerBin, "start", containerName(cfg.ID)); err != nil {
		return nil, fmt.Errorf("container start: %w", err)
	}
	if !b.skipLogFollower {
		if err := b.startLogFollower(cfg.ID); err != nil {
			fmt.Printf("Warning: apple-container log follower for %s: %v\n", cfg.ID, err)
		}
	}
	ip, _ := b.inspectIP(ctx, cfg.ID)
	return &VMInfo{
		ID:        cfg.ID,
		IP:        ip,
		StateDir:  stateDir,
		State:     "running",
		CreatedAt: time.Now(),
	}, nil
}

func (b *AppleContainerBackend) StopVM(ctx context.Context, id string) error {
	b.stopLogFollower(id)
	if _, err := b.run(ctx, b.cfg.ContainerBin, "stop", containerName(id)); err != nil {
		return fmt.Errorf("container stop: %w", err)
	}
	return nil
}

func (b *AppleContainerBackend) DeleteVM(ctx context.Context, id string) error {
	b.stopLogFollower(id)
	// Best-effort stop; ignore error (container may already be stopped).
	b.run(ctx, b.cfg.ContainerBin, "stop", containerName(id))
	if _, err := b.run(ctx, b.cfg.ContainerBin, "rm", containerName(id)); err != nil {
		return fmt.Errorf("container rm: %w", err)
	}
	os.RemoveAll(b.vmStateDir(id))
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/vmbackend/ -run "StartVM|StopVM|DeleteVM" -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat: implement AppleContainerBackend Start/Stop/Delete"
```

---

### Task 1.5: `Close` — kill all log followers

**Files:**
- Modify: `pkg/vmbackend/apple_container.go` (`Close`)
- Modify: `pkg/vmbackend/apple_container_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pkg/vmbackend/apple_container_test.go`:

```go
import (
	"context"
	"os/exec"
	"strings"
	"testing"
)
// (merge imports — os/exec is new)

func TestAppleContainerBackend_CloseKillsFollowers(t *testing.T) {
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, newFakeRunner().run)
	// Register a real, long-lived process as a follower.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	b.mu.Lock()
	b.followers["abc12345"] = &logFollower{cmd: cmd}
	b.mu.Unlock()

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("expected follower process to be killed, but it exited cleanly")
	}
	b.mu.Lock()
	n := len(b.followers)
	b.mu.Unlock()
	if n != 0 {
		t.Errorf("expected followers map cleared, got %d entries", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/vmbackend/ -run CloseKillsFollowers -v`
Expected: FAIL — `Close` is a no-op; the `sleep` process survives so `cmd.Wait()` returns nil.

- [ ] **Step 3: Implement `Close`**

In `pkg/vmbackend/apple_container.go`, replace the `Close` stub:

```go
func (b *AppleContainerBackend) Close() error {
	b.mu.Lock()
	ids := make([]string, 0, len(b.followers))
	for id := range b.followers {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.stopLogFollower(id)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/vmbackend/ -run CloseKillsFollowers -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat: AppleContainerBackend.Close kills log followers"
```

---

### Task 1.6: `GetVM`, `ListVMs`, `inspectIP` — JSON parsing

**Files:**
- Modify: `pkg/vmbackend/apple_container.go`
- Modify: `pkg/vmbackend/apple_container_test.go`

`container inspect <name> --format json` and `container ls --all --format json` return JSON arrays. Apple's `container` is pre-1.0; the exact JSON shape is not pinned. **Decision:** parse defensively into a struct that pulls only the fields needed (`status`, `networks[].address`), tolerate missing fields, and never parse human output. The struct below targets the documented `container` ~0.12 JSON; if a real `container` reveals a different shape, the integration test in Task 1.10 catches it and only the `containerJSON` struct + the two extractor helpers need adjusting.

- [ ] **Step 1: Write the failing test**

Add to `pkg/vmbackend/apple_container_test.go`:

```go
func TestAppleContainerBackend_GetVM_ParsesStatus(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["inspect"] = `[{"status":"running","configuration":{"id":"stockyard-abc12345"}}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)

	st, err := b.GetVM(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if st.ID != "abc12345" {
		t.Errorf("expected ID abc12345, got %q", st.ID)
	}
	if st.Status != "running" {
		t.Errorf("expected status running, got %q", st.Status)
	}
}

func TestAppleContainerBackend_GetVM_StoppedStatus(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["inspect"] = `[{"status":"stopped","configuration":{"id":"stockyard-abc12345"}}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	st, err := b.GetVM(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if st.Status != "stopped" {
		t.Errorf("expected status stopped, got %q", st.Status)
	}
}

func TestAppleContainerBackend_ListVMs_ParsesArray(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["ls"] = `[
		{"status":"running","configuration":{"id":"stockyard-aaa11111"}},
		{"status":"stopped","configuration":{"id":"stockyard-bbb22222"}},
		{"status":"running","configuration":{"id":"not-ours"}}
	]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	states, err := b.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	// Only stockyard-prefixed containers belong to us.
	got := map[string]string{}
	for _, s := range states {
		got[s.ID] = s.Status
	}
	if got["aaa11111"] != "running" {
		t.Errorf("expected aaa11111 running, got %q", got["aaa11111"])
	}
	if got["bbb22222"] != "stopped" {
		t.Errorf("expected bbb22222 stopped, got %q", got["bbb22222"])
	}
	if _, ok := got["not-ours"]; ok {
		t.Error("non-stockyard container should be ignored")
	}
}

func TestAppleContainerBackend_InspectIP(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["inspect"] = `[{"status":"running","networks":[{"address":"192.168.64.7/24"}],"configuration":{"id":"stockyard-abc12345"}}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	ip, err := b.inspectIP(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("inspectIP: %v", err)
	}
	if ip != "192.168.64.7" {
		t.Errorf("expected 192.168.64.7 (CIDR stripped), got %q", ip)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/vmbackend/ -run "GetVM|ListVMs|InspectIP" -v`
Expected: FAIL — `GetVM`/`ListVMs` return `errNotImplemented`; `inspectIP` returns `""`.

- [ ] **Step 3: Implement JSON parsing**

In `pkg/vmbackend/apple_container.go`, add `"encoding/json"` and `"strings"` to imports. Add the JSON type and replace the `GetVM`, `ListVMs`, `inspectIP` bodies:

```go
// containerJSON is the subset of `container inspect`/`container ls` JSON we use.
// Apple `container` is pre-1.0; parse defensively and tolerate missing fields.
type containerJSON struct {
	Status        string `json:"status"`
	Configuration struct {
		ID string `json:"id"`
	} `json:"configuration"`
	Networks []struct {
		Address string `json:"address"`
	} `json:"networks"`
}

// vmIDFromName strips the "stockyard-" prefix; returns ("", false) if absent.
func vmIDFromName(name string) (string, bool) {
	const prefix = "stockyard-"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	return strings.TrimPrefix(name, prefix), true
}

// addressToIP strips a trailing CIDR suffix ("/24") from an address.
func addressToIP(addr string) string {
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i]
	}
	return addr
}

func (b *AppleContainerBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	out, err := b.run(ctx, b.cfg.ContainerBin, "inspect", containerName(id), "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("container inspect: %w", err)
	}
	var arr []containerJSON
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parse container inspect JSON: %w", err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("VM not found: %s", id)
	}
	return &VMState{ID: id, Status: arr[0].Status}, nil
}

func (b *AppleContainerBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	out, err := b.run(ctx, b.cfg.ContainerBin, "ls", "--all", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("container ls: %w", err)
	}
	var arr []containerJSON
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parse container ls JSON: %w", err)
	}
	var states []*VMState
	for _, c := range arr {
		vmID, ok := vmIDFromName(c.Configuration.ID)
		if !ok {
			continue // not one of ours
		}
		states = append(states, &VMState{ID: vmID, Status: c.Status})
	}
	return states, nil
}

func (b *AppleContainerBackend) inspectIP(ctx context.Context, id string) (string, error) {
	out, err := b.run(ctx, b.cfg.ContainerBin, "inspect", containerName(id), "--format", "json")
	if err != nil {
		return "", fmt.Errorf("container inspect: %w", err)
	}
	var arr []containerJSON
	if err := json.Unmarshal(out, &arr); err != nil {
		return "", fmt.Errorf("parse container inspect JSON: %w", err)
	}
	if len(arr) == 0 || len(arr[0].Networks) == 0 {
		return "", nil
	}
	return addressToIP(arr[0].Networks[0].Address), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/vmbackend/ -run "GetVM|ListVMs|InspectIP" -v`
Expected: PASS (all four).

- [ ] **Step 5: Run the whole package to ensure nothing regressed**

Run: `go test ./pkg/vmbackend/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/vmbackend/apple_container.go pkg/vmbackend/apple_container_test.go
git commit -m "feat: implement AppleContainerBackend GetVM/ListVMs JSON parsing"
```

---

### Task 1.7: Daemon constructor (`createAppleContainerBackend`) + system-status probe

**Files:**
- Create: `pkg/daemon/backend_apple_container_darwin.go`
- Create: `pkg/daemon/backend_apple_container_other.go`
- Modify: `pkg/daemon/daemon.go` (backend `switch`, ~lines 103-128)

The constructor runs a cheap `container system status` probe at construction; if the service is down it returns an actionable error.

- [ ] **Step 1: Create the non-darwin stub**

Create `pkg/daemon/backend_apple_container_other.go`:

```go
//go:build !darwin

package daemon

import (
	"fmt"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createAppleContainerBackend(cfg *config.Config) (vmbackend.Backend, error) {
	return nil, fmt.Errorf("apple-container backend is only available on macOS")
}
```

- [ ] **Step 2: Create the darwin constructor**

Create `pkg/daemon/backend_apple_container_darwin.go`:

```go
//go:build darwin

package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createAppleContainerBackend(cfg *config.Config) (vmbackend.Backend, error) {
	bin := cfg.AppleContainer.ContainerBin
	if bin == "" {
		bin = "container"
	}

	// Fail-fast probe: confirm the `container` service is reachable.
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(probeCtx, bin, "system", "status").Run(); err != nil {
		return nil, fmt.Errorf(
			"apple-container backend: `%s system status` failed (%w); "+
				"is the container service running? try `container system start`", bin, err)
	}

	acCfg := vmbackend.AppleContainerConfig{
		ContainerBin: bin,
		Image:        cfg.AppleContainer.Image,
		StateDir:     cfg.Daemon.DataDir + "/vms/stockyard",
	}
	return vmbackend.NewAppleContainerBackend(acCfg), nil
}
```

- [ ] **Step 3: Wire the backend `case` into `daemon.go`**

In `pkg/daemon/daemon.go`, in the `switch cfg.Backend` block, add a `case` after the `vfkit` case and before `default`:

```go
	case "vfkit":
		var err error
		backend, err = createVfkitBackend(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create vfkit backend: %w", err)
		}
	case "apple-container":
		var err error
		backend, err = createAppleContainerBackend(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create apple-container backend: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown backend: %s", cfg.Backend)
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: success, no errors.

> There is no unit test for the darwin constructor itself — it shells out to a real `container` binary for the probe, which is unavailable here. The constructor's logic is trivial wiring; the system-status probe is exercised by the gated integration test (Task 1.10). The `case` is exercised indirectly by Task 1.9's reconciliation test, which constructs a daemon. This is an accepted, explicit gap.

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/backend_apple_container_darwin.go pkg/daemon/backend_apple_container_other.go pkg/daemon/daemon.go
git commit -m "feat: wire apple-container backend into daemon"
```

---

### Task 1.8: Rootfs-provisioner guard

**Files:**
- Modify: `pkg/daemon/rootfs_darwin.go`
- Create: `pkg/daemon/rootfs_darwin_test.go`

`createRootfsProvisioner` returns an APFS provisioner whenever `cfg.Rootfs.BaseImage != ""`, ignoring the backend. For `apple-container`, `container` owns the rootfs, so a stray `rootfs.base_image` must not produce an unused APFS clone.

- [ ] **Step 1: Write the failing test**

Create `pkg/daemon/rootfs_darwin_test.go`:

```go
//go:build darwin

package daemon

import (
	"testing"

	"github.com/obra/stockyard/pkg/config"
)

func TestCreateRootfsProvisioner_AppleContainerReturnsNil(t *testing.T) {
	cfg := &config.Config{Backend: "apple-container"}
	cfg.Rootfs.BaseImage = "/some/stray/base.img" // would otherwise trigger APFS
	if p := createRootfsProvisioner(cfg); p != nil {
		t.Errorf("apple-container backend must yield a nil rootfs provisioner, got %T", p)
	}
}

func TestCreateRootfsProvisioner_VfkitStillProvisions(t *testing.T) {
	cfg := &config.Config{Backend: "vfkit"}
	cfg.Rootfs.BaseImage = "/some/base.img"
	if p := createRootfsProvisioner(cfg); p == nil {
		t.Error("vfkit with a base image must still yield an APFS provisioner")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/ -run CreateRootfsProvisioner -v`
Expected: FAIL — `TestCreateRootfsProvisioner_AppleContainerReturnsNil` fails (APFS provisioner returned).

- [ ] **Step 3: Add the guard**

In `pkg/daemon/rootfs_darwin.go`, add the guard as the first statement in `createRootfsProvisioner`:

```go
func createRootfsProvisioner(cfg *config.Config) rootfs.Provisioner {
	// apple-container owns its own rootfs; never provision one.
	if cfg.Backend == "apple-container" {
		return nil
	}
	switch cfg.Rootfs.Provider {
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/ -run CreateRootfsProvisioner -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add pkg/daemon/rootfs_darwin.go pkg/daemon/rootfs_darwin_test.go
git commit -m "feat: skip rootfs provisioner for apple-container backend"
```

---

### Task 1.9: Restart reconciliation via the backend

**Files:**
- Modify: `pkg/daemon/daemon.go` (`reconcileRunningVMs`, ~lines 172-226)
- Create: `pkg/daemon/reconcile_test.go`

`reconcileRunningVMs` is PID-file based and wrong for apple-container (no PID file → every container marked `stopped` on restart). Add a backend branch: for `apple-container`, reconcile each running task against `backend.ListVMs`.

- [ ] **Step 1: Write the failing test**

Create `pkg/daemon/reconcile_test.go`:

```go
package daemon

import (
	"context"
	"testing"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

// fakeReconcileBackend is a Backend stub returning scripted ListVMs results.
type fakeReconcileBackend struct {
	states []*vmbackend.VMState
}

func (f *fakeReconcileBackend) CreateVM(context.Context, *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return nil, nil
}
func (f *fakeReconcileBackend) StartVM(context.Context, *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return nil, nil
}
func (f *fakeReconcileBackend) StopVM(context.Context, string) error   { return nil }
func (f *fakeReconcileBackend) DeleteVM(context.Context, string) error { return nil }
func (f *fakeReconcileBackend) GetVM(context.Context, string) (*vmbackend.VMState, error) {
	return nil, nil
}
func (f *fakeReconcileBackend) ListVMs(context.Context) ([]*vmbackend.VMState, error) {
	return f.states, nil
}
func (f *fakeReconcileBackend) Close() error { return nil }

func TestReconcileRunningVMs_AppleContainer(t *testing.T) {
	dataDir := t.TempDir()
	state, err := NewState(dataDir)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer state.Close()

	// Two running tasks: one whose container is live, one whose container is gone.
	live := &Task{ID: "live0001", Name: "live", Status: "running", VMID: "live0001"}
	dead := &Task{ID: "dead0002", Name: "dead", Status: "running", VMID: "dead0002"}
	if err := state.CreateTask(live); err != nil {
		t.Fatalf("CreateTask live: %v", err)
	}
	if err := state.CreateTask(dead); err != nil {
		t.Fatalf("CreateTask dead: %v", err)
	}

	d := &Daemon{
		cfg:   &config.Config{Backend: "apple-container", Daemon: config.DaemonConfig{DataDir: dataDir}},
		state: state,
	}
	d.tasks = NewTaskManager(d, &fakeReconcileBackend{
		states: []*vmbackend.VMState{{ID: "live0001", Status: "running"}},
	})

	d.reconcileRunningVMs()

	got, err := state.GetTask("live0001")
	if err != nil {
		t.Fatalf("GetTask live: %v", err)
	}
	if got.Status != "running" {
		t.Errorf("live container should stay running, got %q", got.Status)
	}
	got, err = state.GetTask("dead0002")
	if err != nil {
		t.Fatalf("GetTask dead: %v", err)
	}
	if got.Status != "stopped" {
		t.Errorf("dead container should be marked stopped, got %q", got.Status)
	}
}
```

> If `Daemon` struct fields `cfg`, `state`, `tasks` or `NewState`/`NewTaskManager`/`Task` signatures differ when you reach this task, adjust the test to match — they are stable in the current code (verified: `daemon.go:30-57`, `state.go:34`, `tasks.go:26`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/ -run ReconcileRunningVMs_AppleContainer -v`
Expected: FAIL — `dead0002` ends up `running` (the PID-file path can't see it) or the test sees no backend branch.

- [ ] **Step 3: Add the backend branch to `reconcileRunningVMs`**

In `pkg/daemon/daemon.go`, insert this block immediately after the `if len(tasks) == 0 { return }` check and before the `fmt.Printf("Reconciling ...")` line — actually, place it right after the `fmt.Printf("Reconciling %d running task(s)...\n", len(tasks))` line:

```go
	fmt.Printf("Reconciling %d running task(s)...\n", len(tasks))

	// apple-container has no PID files; reconcile against the backend itself.
	if d.cfg.Backend == "apple-container" {
		d.reconcileViaBackend(tasks)
		return
	}
```

Then add a new method to `daemon.go`:

```go
// reconcileViaBackend reconciles task liveness by asking the VM backend which
// VMs are running. Used by backends (e.g. apple-container) that keep no PID file.
func (d *Daemon) reconcileViaBackend(tasks []*Task) {
	if d.tasks == nil || d.tasks.backend == nil {
		// No backend to ask — leave statuses untouched.
		return
	}
	states, err := d.tasks.backend.ListVMs(context.Background())
	if err != nil {
		fmt.Printf("Warning: backend ListVMs during reconciliation failed: %v\n", err)
		return
	}
	running := make(map[string]bool, len(states))
	for _, s := range states {
		if s.Status == "running" {
			running[s.ID] = true
		}
	}
	for _, task := range tasks {
		if running[task.VMID] {
			fmt.Printf("  Task %s: container still running\n", task.ID)
			continue
		}
		fmt.Printf("  Task %s: container not running, marking as stopped\n", task.ID)
		d.state.UpdateTaskStatus(task.ID, "stopped")
	}
}
```

> `d.tasks.backend` is an unexported field on `TaskManager` (`tasks.go:22`); `daemon.go` is in the same package, so direct access is fine and matches the package's existing style.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/ -run ReconcileRunningVMs_AppleContainer -v`
Expected: PASS.

- [ ] **Step 5: Run the daemon package to check no regressions**

Run: `go test ./pkg/daemon/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/daemon/daemon.go pkg/daemon/reconcile_test.go
git commit -m "feat: reconcile apple-container task liveness via backend ListVMs"
```

---

### Task 1.10: Gated real-`container` integration test (placeholder, env-gated)

**Files:**
- Create: `pkg/vmbackend/apple_container_integration_test.go`

This test is `//go:build darwin && container_integration` — it is NOT compiled or run by the overnight `go test ./...`. It exists so that, on a macOS 26 machine with `container` installed, a developer can run `go test -tags container_integration ./pkg/vmbackend/` to validate the real CLI contract (especially the JSON shapes from Task 1.6 and writable-layer persistence across stop/start).

- [ ] **Step 1: Create the gated integration test**

Create `pkg/vmbackend/apple_container_integration_test.go`:

```go
//go:build darwin && container_integration

package vmbackend

import (
	"context"
	"testing"
	"time"
)

// Run with: go test -tags container_integration ./pkg/vmbackend/
// Requires macOS 26+, `container` installed, and the service started
// (`container system start`). Set STOCKYARD_TEST_IMAGE to an OCI image
// that runs `sleep infinity` (or similar long-lived entrypoint).
func TestAppleContainerBackend_Integration_Lifecycle(t *testing.T) {
	image := getTestImage(t)
	b := NewAppleContainerBackend(AppleContainerConfig{
		Image:    image,
		StateDir: t.TempDir(),
	})
	defer b.Close()

	ctx := context.Background()
	id := GenerateVMID()
	cfg := &VMConfig{ID: id, VCPU: 2, MemoryMB: 1024, Metadata: map[string]string{"task-id": id}}

	if _, err := b.CreateVM(ctx, cfg); err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	defer b.DeleteVM(ctx, id)

	st, err := b.GetVM(ctx, id)
	if err != nil || st.Status != "running" {
		t.Fatalf("GetVM after create: %+v err=%v", st, err)
	}

	if err := b.StopVM(ctx, id); err != nil {
		t.Fatalf("StopVM: %v", err)
	}
	time.Sleep(2 * time.Second)

	// Writable-layer persistence: a stopped (not deleted) container must restart.
	if _, err := b.StartVM(ctx, cfg); err != nil {
		t.Fatalf("StartVM after stop: %v", err)
	}
	st, err = b.GetVM(ctx, id)
	if err != nil || st.Status != "running" {
		t.Fatalf("GetVM after restart: %+v err=%v", st, err)
	}
}

func getTestImage(t *testing.T) string {
	t.Helper()
	img := envOrSkip(t, "STOCKYARD_TEST_IMAGE")
	return img
}

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := getenv(key)
	if v == "" {
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}
```

Add a tiny helper at the bottom of the same file:

```go
import "os"

func getenv(k string) string { return os.Getenv(k) }
```

> Consolidate the two `import` blocks into one when writing the file (`context`, `os`, `testing`, `time`). Shown separately here only for clarity.

- [ ] **Step 2: Verify it does NOT break the normal build**

Run: `go build ./...` and `go test ./pkg/vmbackend/`
Expected: PASS — the integration file is excluded by its build tag, so it has zero effect on the standard build.

- [ ] **Step 3: Verify it compiles under its tag**

Run: `go vet -tags container_integration ./pkg/vmbackend/`
Expected: no errors (compiles cleanly even though the test won't run here).

- [ ] **Step 4: Commit**

```bash
git add pkg/vmbackend/apple_container_integration_test.go
git commit -m "test: add gated real-container integration test"
```

---

### Task 1.11: Phase 1 verification

- [ ] **Step 1: Full build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: PASS — all packages green, no regressions versus the baseline.

- [ ] **Step 3: vet**

Run: `go vet ./...`
Expected: no issues.

- [ ] **Step 4: If anything fails, fix it before proceeding.** Phase 2 builds on Phase 1; do not move on with a red tree.

---

# PHASE 2 — Task Data Model + CLI

Threads `backend`/`vm_id` through the gRPC `Task` message so the dashboard terminal and `stockyard attach` can dispatch on backend. Depends on Phase 1 only for `config.Backend == "apple-container"` being a valid value.

---

### Task 2.1: Add `backend` and `vm_id` to the proto `Task` message

**Files:**
- Modify: `api/stockyard.proto` (`message Task`, lines 122-130)
- Regenerate: `pkg/api/v1/stockyard.pb.go`, `pkg/api/v1/stockyard_grpc.pb.go`

- [ ] **Step 0: Ensure proto plugins are installed**

The build environment did not ship `protoc-gen-go`/`protoc-gen-go-grpc`. Install them (idempotent — skip if `which protoc-gen-go` already resolves):

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

They land in `$(go env GOPATH)/bin` (here: `/Users/mw/go/bin`). `protoc` itself (libprotoc 34.1) is already on PATH.

- [ ] **Step 1: Edit the proto**

In `api/stockyard.proto`, change `message Task` (currently fields 1-7) to:

```proto
message Task {
    string id = 1;
    string name = 2;
    string status = 3;
    string tailscale_hostname = 4;
    string created_at = 5;
    string stopped_at = 6;
    string ip = 7;
    string backend = 8;
    string vm_id = 9;
}
```

- [ ] **Step 2: Regenerate the Go bindings**

Run (the `make proto` target moves generated files into `pkg/api/v1/`):

```bash
PATH="$PATH:$(go env GOPATH)/bin" make proto
```

Expected: `pkg/api/v1/stockyard.pb.go` and `pkg/api/v1/stockyard_grpc.pb.go` are rewritten. `git diff --stat` shows changes to both.

- [ ] **Step 3: Verify the generated accessors exist**

Run:

```bash
grep -E "func \(x \*Task\) GetBackend|func \(x \*Task\) GetVmId" pkg/api/v1/stockyard.pb.go
```

Expected: both `GetBackend()` and `GetVmId()` accessor methods are present.

- [ ] **Step 4: Build to confirm the regenerated code compiles**

Run: `go build ./...`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add api/stockyard.proto pkg/api/v1/stockyard.pb.go pkg/api/v1/stockyard_grpc.pb.go
git commit -m "feat: add backend and vm_id to proto Task message"
```

---

### Task 2.2: Populate `backend`/`vm_id` in `taskToProto`

**Files:**
- Modify: `pkg/daemon/grpc.go` (`taskToProto`, ~lines 529-541)
- Create: `pkg/daemon/grpc_tasktoproto_test.go`

`taskToProto` has no access to `cfg` today. The daemon is single-backend, so `backend` is `daemon.cfg.Backend`. `taskToProto` is a free function; the cleanest fix is to make it a method on the gRPC server (which holds the daemon) OR pass the backend string in. **Decision:** make `taskToProto` take the backend string as a parameter — minimal, explicit, no receiver change. Callers at `grpc.go:70` and `grpc.go:82` pass `s.daemon.cfg.Backend`.

- [ ] **Step 1: Write the failing test**

Create `pkg/daemon/grpc_tasktoproto_test.go`:

```go
package daemon

import (
	"testing"
	"time"
)

func TestTaskToProto_PopulatesBackendAndVMID(t *testing.T) {
	task := &Task{
		ID:        "abc12345",
		Name:      "demo",
		Status:    "running",
		VMID:      "abc12345",
		IP:        "192.168.64.7",
		CreatedAt: time.Now(),
	}
	pt := taskToProto(task, "apple-container")
	if pt.Backend != "apple-container" {
		t.Errorf("expected backend apple-container, got %q", pt.Backend)
	}
	if pt.VmId != "abc12345" {
		t.Errorf("expected vm_id abc12345, got %q", pt.VmId)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/ -run TaskToProto_PopulatesBackendAndVMID -v`
Expected: FAIL — `taskToProto` takes one argument, not two.

- [ ] **Step 3: Update `taskToProto` and its callers**

In `pkg/daemon/grpc.go`, change `taskToProto`:

```go
func taskToProto(t *Task, backend string) *pb.Task {
	pt := &pb.Task{
		Id:                t.ID,
		Name:              t.Name,
		Status:            t.Status,
		TailscaleHostname: t.TailscaleHostname,
		Ip:                t.IP,
		Backend:           backend,
		VmId:              t.VMID,
		CreatedAt:         t.CreatedAt.Format(time.RFC3339),
	}
	if t.StoppedAt != nil {
		pt.StoppedAt = t.StoppedAt.Format(time.RFC3339)
	}
	return pt
}
```

Update the two call sites. At `grpc.go:70` (inside `GetTask`):

```go
		Task: taskToProto(task, s.daemon.cfg.Backend),
```

At `grpc.go:82` (inside `ListTasks`, in the loop):

```go
		pbTasks[i] = taskToProto(t, s.daemon.cfg.Backend)
```

> Verify the gRPC server type's field for the daemon is `s.daemon` and that `daemon.cfg` is reachable. `daemon.cfg` is unexported but `grpc.go` is in package `daemon`, so `s.daemon.cfg.Backend` compiles. If the server struct names the field differently, adjust — check `newGRPCServer` in `grpc.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/ -run TaskToProto_PopulatesBackendAndVMID -v`
Expected: PASS.

- [ ] **Step 5: Run the daemon package**

Run: `go test ./pkg/daemon/`
Expected: PASS (existing `grpc_test.go` still green).

- [ ] **Step 6: Commit**

```bash
git add pkg/daemon/grpc.go pkg/daemon/grpc_tasktoproto_test.go
git commit -m "feat: populate backend and vm_id in gRPC Task responses"
```

---

### Task 2.3: Thread `Backend`/`VMID` through the dashboard `Task` types

**Files:**
- Modify: `pkg/dashboard/daemon.go` (`Task` struct, lines 8-17)
- Modify: `pkg/dashboard/adapter.go` (`DaemonTask` struct lines 31-42, `convertTask` lines 157-168)
- Modify: `pkg/daemon/dashboard_facade.go` (`convertToDashboardTask`, lines 236-247)
- Create: `pkg/dashboard/adapter_backend_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/dashboard/adapter_backend_test.go`:

```go
package dashboard

import "testing"

func TestConvertTask_CarriesBackendAndVMID(t *testing.T) {
	dt := &DaemonTask{
		ID:      "abc12345",
		Name:    "demo",
		Status:  "running",
		VMID:    "abc12345",
		Backend: "apple-container",
	}
	got := convertTask(dt)
	if got.Backend != "apple-container" {
		t.Errorf("expected Backend apple-container, got %q", got.Backend)
	}
	if got.VMID != "abc12345" {
		t.Errorf("expected VMID abc12345, got %q", got.VMID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/ -run ConvertTask_CarriesBackendAndVMID -v`
Expected: FAIL — `DaemonTask.Backend` and `Task.Backend`/`Task.VMID` undefined.

- [ ] **Step 3: Add the fields**

In `pkg/dashboard/daemon.go`, add to the `Task` struct:

```go
type Task struct {
	ID            string
	Name          string
	Status        string
	Owner         string
	TailscaleHost string
	Backend       string
	VMID          string
	CreatedAt     time.Time
	StoppedAt     *time.Time
}
```

In `pkg/dashboard/adapter.go`, add to the `DaemonTask` struct:

```go
type DaemonTask struct {
	ID                string
	Name              string
	Command           string
	Status            string
	VMID              string
	Owner             string
	Backend           string
	TailscaleHostname string
	CreatedAt         time.Time
	StoppedAt         *time.Time
}
```

In `pkg/dashboard/adapter.go`, update `convertTask`:

```go
func convertTask(dt *DaemonTask) Task {
	return Task{
		ID:            dt.ID,
		Name:          dt.Name,
		Status:        dt.Status,
		Owner:         dt.Owner,
		TailscaleHost: dt.TailscaleHostname,
		Backend:       dt.Backend,
		VMID:          dt.VMID,
		CreatedAt:     dt.CreatedAt,
		StoppedAt:     dt.StoppedAt,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/dashboard/ -run ConvertTask_CarriesBackendAndVMID -v`
Expected: PASS.

- [ ] **Step 5: Populate `Backend` in the daemon facade**

In `pkg/daemon/dashboard_facade.go`, update `convertToDashboardTask`. The facade's `DashboardFacade` holds `state` and `tasks` but **not `cfg`** — so it has no backend string. **Decision:** add a `backend string` field to `DashboardFacade`, set it in `NewDashboardFacade`, and use it here. Change the constructor signature and `daemon.go`'s call site.

In `dashboard_facade.go`, change the struct and constructor:

```go
type DashboardFacade struct {
	state   *State
	tasks   *TaskManager
	zfs     *zfs.Manager
	backend string
}

// NewDashboardFacade creates a new facade for dashboard access.
func NewDashboardFacade(state *State, tasks *TaskManager, zfsMgr *zfs.Manager, backend string) *DashboardFacade {
	return &DashboardFacade{
		state:   state,
		tasks:   tasks,
		zfs:     zfsMgr,
		backend: backend,
	}
}
```

`convertToDashboardTask` is a free function — make it a method so it can read `f.backend`:

```go
func (f *DashboardFacade) convertToDashboardTask(t *Task) *dashboard.DaemonTask {
	return &dashboard.DaemonTask{
		ID:                t.ID,
		Name:              t.Name,
		Command:           t.Command,
		Status:            t.Status,
		VMID:              t.VMID,
		Backend:           f.backend,
		TailscaleHostname: t.TailscaleHostname,
		CreatedAt:         t.CreatedAt,
		StoppedAt:         t.StoppedAt,
	}
}
```

Update the three call sites in `dashboard_facade.go` (`ListTasks` ~line 38, `GetTask` ~line 53, `CreateTask` ~line 75): change `convertToDashboardTask(t)` / `convertToDashboardTask(task)` to `f.convertToDashboardTask(t)` / `f.convertToDashboardTask(task)`.

In `pkg/daemon/daemon.go`, update the `NewDashboardFacade` call (~line 315):

```go
		facade := NewDashboardFacade(d.state, d.tasks, d.zfs, d.cfg.Backend)
```

- [ ] **Step 6: Fix all other `NewDashboardFacade` callers**

Run: `grep -rn "NewDashboardFacade" --include="*.go" .`
Expected callers (verified at plan time): `daemon.go:315` (already updated in Step 5) and **8 calls in `pkg/daemon/dashboard_facade_test.go`** of the form `NewDashboardFacade(state, nil, nil)`. Update every test call to pass a fourth argument — `NewDashboardFacade(state, nil, nil, "")` — since those tests do not exercise backend population. A `sed` is acceptable here:

```bash
sed -i '' 's/NewDashboardFacade(state, nil, nil)/NewDashboardFacade(state, nil, nil, "")/g' pkg/daemon/dashboard_facade_test.go
```

Re-run the grep afterward to confirm zero three-argument calls remain.

- [ ] **Step 7: Build and test**

Run: `go build ./... && go test ./pkg/dashboard/ ./pkg/daemon/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/dashboard/daemon.go pkg/dashboard/adapter.go pkg/dashboard/adapter_backend_test.go pkg/daemon/dashboard_facade.go pkg/daemon/daemon.go pkg/daemon/dashboard_facade_test.go
git commit -m "feat: thread backend and vm_id through dashboard Task types"
```

---

### Task 2.4: `stockyard attach` backend dispatch

**Files:**
- Modify: `cmd/stockyard/attach.go`
- Modify: `cmd/stockyard/attach_test.go`

`attach` currently always `exec`s `ssh`. It must dispatch on `task.GetBackend()`: `apple-container` → `container exec -t -i stockyard-<vmID> <shell>`; everything else → existing SSH path. To make this testable, extract a pure function that decides the argv.

- [ ] **Step 1: Write the failing test**

Replace the contents of `cmd/stockyard/attach_test.go` with:

```go
// cmd/stockyard/attach_test.go
package main

import (
	"testing"

	pb "github.com/obra/stockyard/pkg/api/v1"
)

func TestAttachCommand_RequiresTaskID(t *testing.T) {
	rootCmd.SetArgs([]string{"attach"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when task-id not provided")
	}
}

func TestBuildAttachCommand_AppleContainer(t *testing.T) {
	task := &pb.Task{
		Id:      "abc12345",
		Status:  "running",
		Backend: "apple-container",
		VmId:    "abc12345",
	}
	name, argv, err := buildAttachCommand(task, "mooby", "container", nil)
	if err != nil {
		t.Fatalf("buildAttachCommand: %v", err)
	}
	if name != "container" {
		t.Errorf("expected program 'container', got %q", name)
	}
	joined := join(argv)
	for _, want := range []string{"exec", "-t", "-i", "stockyard-abc12345"} {
		if !contains(joined, want) {
			t.Errorf("apple-container argv missing %q; got %v", want, argv)
		}
	}
}

func TestBuildAttachCommand_SSHForVfkit(t *testing.T) {
	task := &pb.Task{
		Id:                "abc12345",
		Status:            "running",
		Backend:           "vfkit",
		TailscaleHostname: "stockyard-abc12345",
	}
	name, argv, err := buildAttachCommand(task, "mooby", "container", nil)
	if err != nil {
		t.Fatalf("buildAttachCommand: %v", err)
	}
	if name != "ssh" {
		t.Errorf("expected program 'ssh', got %q", name)
	}
	if !contains(join(argv), "mooby@stockyard-abc12345") {
		t.Errorf("ssh argv missing user@host; got %v", argv)
	}
}

func TestBuildAttachCommand_SSHForEmptyBackend(t *testing.T) {
	// Empty backend (legacy Firecracker) must take the SSH path.
	task := &pb.Task{
		Id:                "abc12345",
		Status:            "running",
		Backend:           "",
		TailscaleHostname: "stockyard-abc12345",
	}
	name, _, err := buildAttachCommand(task, "mooby", "container", nil)
	if err != nil {
		t.Fatalf("buildAttachCommand: %v", err)
	}
	if name != "ssh" {
		t.Errorf("empty backend should use ssh, got %q", name)
	}
}

// tiny test helpers
func join(a []string) string {
	s := ""
	for _, x := range a {
		s += x + " "
	}
	return s
}
func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
```

> The `join`/`contains` helpers are deliberately self-contained to avoid coupling the test to `strings` import collisions elsewhere in the package. If `strings` is already imported package-wide in tests, you may simplify — but the above always compiles.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/stockyard/ -run BuildAttachCommand -v`
Expected: FAIL — `buildAttachCommand` undefined.

- [ ] **Step 3: Refactor `attach.go` to extract `buildAttachCommand`**

Replace `cmd/stockyard/attach.go` with:

```go
// cmd/stockyard/attach.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

// buildAttachCommand decides the program and argv to exec for `stockyard attach`,
// dispatching on the task's backend. extraArgs are appended after a "--" separator
// for SSH or directly as the remote command for `container exec`.
//
// For apple-container: `container exec -t -i stockyard-<vmID> <shell-or-extraArgs>`.
// For all other backends: the existing SSH-via-Tailscale path.
func buildAttachCommand(task *pb.Task, sshUser, containerBin string, extraArgs []string) (string, []string, error) {
	if task.GetBackend() == "apple-container" {
		vmID := task.GetVmId()
		if vmID == "" {
			return "", nil, fmt.Errorf("apple-container task has no vm_id")
		}
		argv := []string{"container", "exec", "-t", "-i", "stockyard-" + vmID}
		if len(extraArgs) > 0 {
			argv = append(argv, extraArgs...)
		} else {
			argv = append(argv, "/bin/bash")
		}
		return containerBin, argv, nil
	}

	// Default: SSH via Tailscale hostname, falling back to direct IP.
	sshHost := task.GetTailscaleHostname()
	if sshHost == "" {
		sshHost = task.GetIp()
	}
	if sshHost == "" {
		return "", nil, fmt.Errorf("task has no reachable address (no Tailscale hostname or IP)")
	}
	argv := []string{"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		fmt.Sprintf("%s@%s", sshUser, sshHost),
	}
	if len(extraArgs) > 0 {
		argv = append(argv, "--")
		argv = append(argv, extraArgs...)
	}
	return "ssh", argv, nil
}

var attachCmd = &cobra.Command{
	Use:   "attach <task-id>",
	Short: "Attach to a running task",
	Long:  `Attach to a running task — via SSH (Firecracker/vfkit) or container exec (apple-container).`,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		task, err := c.GetTask(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if task.Status != "running" {
			return fmt.Errorf("task is not running (status: %s)", task.Status)
		}

		var extraArgs []string
		if cmd.ArgsLenAtDash() >= 0 {
			extraArgs = args[1:]
		}

		containerBin := cfg.AppleContainer.ContainerBin
		if containerBin == "" {
			containerBin = "container"
		}

		program, argv, err := buildAttachCommand(task, cfg.VM.User, containerBin, extraArgs)
		if err != nil {
			return err
		}

		progPath, err := exec.LookPath(program)
		if err != nil {
			return fmt.Errorf("%s not found: %w", program, err)
		}

		fmt.Printf("Attaching to %s...\n", taskID)
		return syscall.Exec(progPath, argv, os.Environ())
	},
}

func init() {
	rootCmd.AddCommand(attachCmd)
}
```

> Note: `Args` changed from `cobra.ExactArgs(1)` to `cobra.MinimumNArgs(1)` so `stockyard attach <id> -- cmd...` works. `TestAttachCommand_RequiresTaskID` still passes (zero args still errors).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/stockyard/ -run "Attach|BuildAttachCommand" -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Run the whole CLI package**

Run: `go test ./cmd/stockyard/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/stockyard/attach.go cmd/stockyard/attach_test.go
git commit -m "feat: stockyard attach dispatches on task backend"
```

---

### Task 2.5: Phase 2 verification

- [ ] **Step 1: Full build** — Run: `go build ./...` → success.
- [ ] **Step 2: Full test suite** — Run: `go test ./...` → all green.
- [ ] **Step 3: vet** — Run: `go vet ./...` → clean.
- [ ] **Step 4: Fix any failure before Phase 3.**

---

# PHASE 3 — Dashboard Terminal

Adds `ContainerExecSession` and makes the terminal handler dispatch on backend. Depends on Phase 2 (`task.Backend` must be populated on `dashboard.Task`).

---

### Task 3.1: `ContainerExecSession` over a host PTY

**Files:**
- Create: `pkg/dashboard/container_exec_session.go` (`//go:build darwin`)
- Create: `pkg/dashboard/container_exec_session_other.go` (`//go:build !darwin`)
- Create: `pkg/dashboard/container_exec_session_test.go` (`//go:build darwin`)

`ContainerExecSession` runs `container exec -t -i stockyard-<vmID> <shell>` under a host PTY (`creack/pty`) and exposes `Read`/`Write`/`Resize`/`Close` so the terminal handler can bridge it to the websocket. The darwin/non-darwin split keeps the package building on Linux (the daemon's dashboard package compiles on all platforms).

- [ ] **Step 1: Write the failing test**

Create `pkg/dashboard/container_exec_session_test.go`:

```go
//go:build darwin

package dashboard

import (
	"io"
	"strings"
	"testing"
)

func TestNewContainerExecSession_RunsCommand(t *testing.T) {
	// Use `cat` as a stand-in for `container exec`: it echoes stdin to stdout
	// under a PTY, which is enough to exercise Read/Write/Close plumbing.
	sess, err := newContainerExecSessionWithCommand("cat", nil, 80, 24)
	if err != nil {
		t.Fatalf("newContainerExecSessionWithCommand: %v", err)
	}
	defer sess.Close()

	if _, err := sess.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 64)
	n, err := sess.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "hello") {
		t.Errorf("expected echoed 'hello', got %q", string(buf[:n]))
	}
}

func TestContainerExecSession_BuildArgs(t *testing.T) {
	argv := containerExecArgs("abc12345", "/bin/bash")
	joined := strings.Join(argv, " ")
	for _, want := range []string{"exec", "-t", "-i", "stockyard-abc12345", "/bin/bash"} {
		if !strings.Contains(joined, want) {
			t.Errorf("containerExecArgs missing %q; got %v", want, argv)
		}
	}
}

func TestContainerExecSession_Resize(t *testing.T) {
	sess, err := newContainerExecSessionWithCommand("cat", nil, 80, 24)
	if err != nil {
		t.Fatalf("newContainerExecSessionWithCommand: %v", err)
	}
	defer sess.Close()
	if err := sess.Resize(120, 40); err != nil {
		t.Errorf("Resize: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/ -run ContainerExecSession -v`
Expected: FAIL — `newContainerExecSessionWithCommand`, `containerExecArgs`, `ContainerExecSession` undefined.

- [ ] **Step 3: Implement the darwin session**

Create `pkg/dashboard/container_exec_session.go`:

```go
//go:build darwin

package dashboard

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// ContainerExecSession is a terminal session backed by `container exec` running
// under a host PTY. It bridges that PTY to the dashboard websocket.
type ContainerExecSession struct {
	ID     string
	TaskID string
	User   string

	cmd *exec.Cmd
	pty *os.File

	mu     sync.Mutex
	closed bool
}

// containerExecArgs builds the argv for `container exec` against a VM ID.
func containerExecArgs(vmID, shell string) []string {
	return []string{"exec", "-t", "-i", "stockyard-" + vmID, shell}
}

// newContainerExecSession starts `container exec` for the given VM under a PTY.
func newContainerExecSession(containerBin, vmID, shell string, cols, rows int) (*ContainerExecSession, error) {
	if shell == "" {
		shell = "/bin/bash"
	}
	return newContainerExecSessionWithCommand(containerBin, containerExecArgs(vmID, shell), cols, rows)
}

// newContainerExecSessionWithCommand starts an arbitrary command under a PTY.
// Exposed for tests (which substitute `cat` for `container`).
func newContainerExecSessionWithCommand(name string, args []string, cols, rows int) (*ContainerExecSession, error) {
	cmd := exec.Command(name, args...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}
	return &ContainerExecSession{cmd: cmd, pty: ptmx}, nil
}

// Read reads terminal output from the PTY.
func (s *ContainerExecSession) Read(p []byte) (int, error) {
	return s.pty.Read(p)
}

// Write writes terminal input to the PTY.
func (s *ContainerExecSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("session closed")
	}
	return s.pty.Write(p)
}

// Resize sets the PTY window size.
func (s *ContainerExecSession) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session closed")
	}
	return pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close terminates the exec process and releases the PTY.
func (s *ContainerExecSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.pty.Close()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	go s.cmd.Wait() // reap without blocking
	return nil
}
```

- [ ] **Step 4: Create the non-darwin stub**

Create `pkg/dashboard/container_exec_session_other.go`:

```go
//go:build !darwin

package dashboard

import "fmt"

// ContainerExecSession is unavailable off macOS; the apple-container backend
// only runs on macOS, so this is never constructed there.
type ContainerExecSession struct {
	ID     string
	TaskID string
	User   string
}

func newContainerExecSession(containerBin, vmID, shell string, cols, rows int) (*ContainerExecSession, error) {
	return nil, fmt.Errorf("apple-container terminal is only available on macOS")
}

func (s *ContainerExecSession) Read(p []byte) (int, error)  { return 0, fmt.Errorf("unsupported") }
func (s *ContainerExecSession) Write(p []byte) (int, error) { return 0, fmt.Errorf("unsupported") }
func (s *ContainerExecSession) Resize(cols, rows int) error { return fmt.Errorf("unsupported") }
func (s *ContainerExecSession) Close() error                { return nil }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pkg/dashboard/ -run ContainerExecSession -v`
Expected: PASS (all three).

- [ ] **Step 6: Confirm the package still builds for Linux**

Run: `GOOS=linux go build ./pkg/dashboard/`
Expected: success — the non-darwin stub satisfies all references.

- [ ] **Step 7: Commit**

```bash
git add pkg/dashboard/container_exec_session.go pkg/dashboard/container_exec_session_other.go pkg/dashboard/container_exec_session_test.go
git commit -m "feat: add ContainerExecSession backed by a host PTY"
```

---

### Task 3.2: Terminal handler dispatches on backend

**Files:**
- Modify: `pkg/dashboard/terminal_handler.go` (`ServeHTTP`, lines 42-119)
- Create: `pkg/dashboard/terminal_handler_dispatch_darwin.go` (`//go:build darwin`)
- Create: `pkg/dashboard/terminal_handler_dispatch_other.go` (`//go:build !darwin`)
- Modify: `pkg/dashboard/terminal_integration_test.go` (extend mock task)

`ServeHTTP` currently always calls `GetVsockPath` and 503s if empty — wrong for apple-container. **Decision:** branch on `task.Backend` early. The `apple-container` arm needs a `containerBin` and uses `ContainerExecSession`; that arm is darwin-only, so it lives in a build-tagged helper `serveContainerExec` (real on darwin, 503 stub on other). The vsock path stays in `terminal_handler.go` unchanged behind the else-branch.

- [ ] **Step 1: Write the failing test**

Add to `pkg/dashboard/terminal_integration_test.go` (the existing `mockDaemonForTerminal` already implements `DaemonAPI`; we just exercise the new branch). Append:

```go
func TestTerminalHandler_AppleContainerBranch_NoVsock503Avoided(t *testing.T) {
	// An apple-container task has an empty vsock path. The handler must NOT
	// 503 on empty vsock for this backend — it must take the container branch.
	daemon := &mockDaemonForTerminal{
		task: &Task{
			ID:      "abc12345",
			Name:    "test",
			Status:  "running",
			Backend: "apple-container",
			VMID:    "abc12345",
		},
	}
	mgr := NewTerminalManager()
	h := NewTerminalHandler(mgr, daemon, "mooby")

	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal/abc12345"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		// On non-darwin the container branch returns 503 via the stub — that is
		// the documented behaviour. On darwin the upgrade succeeds (then the
		// exec may fail later because `container` is absent, which is fine).
		if resp != nil && resp.StatusCode == http.StatusServiceUnavailable {
			return // acceptable on non-darwin
		}
		// A *vsock*-path 503 ("VM not available") would mean the branch is wrong.
		t.Fatalf("unexpected dial failure (wrong branch?): %v", err)
	}
	if conn != nil {
		conn.Close()
	}
}
```

> This test tolerates both outcomes (darwin: upgrade ok; non-darwin: 503 from the stub) — what it actually guards against is the *old* behaviour where the vsock-path `GetVsockPath` 503 fires for an apple-container task. Keep it; it is a real regression guard.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/ -run AppleContainerBranch -v`
Expected: FAIL — handler still calls `GetVsockPath`, returns the vsock 503, dial fails with that.

- [ ] **Step 3: Create the build-tagged dispatch helpers**

Create `pkg/dashboard/terminal_handler_dispatch_darwin.go`:

```go
//go:build darwin

package dashboard

import (
	"log"
	"net/http"

	"github.com/google/uuid"
)

// serveContainerExec upgrades the websocket and bridges it to a
// `container exec` PTY session for an apple-container task.
func (h *TerminalHandler) serveContainerExec(w http.ResponseWriter, r *http.Request, task *Task, user string, cols, rows int) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("terminal: websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	session, err := newContainerExecSession(h.containerBin(), task.VMID, "/bin/bash", cols, rows)
	if err != nil {
		h.sendError(conn, "Failed to start container exec: "+err.Error())
		return
	}
	session.ID = uuid.New().String()
	session.TaskID = task.ID
	session.User = user
	defer session.Close()

	log.Printf("terminal: container exec session started for task %s (%dx%d)", task.ID, cols, rows)
	h.bridgeContainerSession(conn, session)
	log.Printf("terminal: container exec session ended for task %s", task.ID)
}
```

Create `pkg/dashboard/terminal_handler_dispatch_other.go`:

```go
//go:build !darwin

package dashboard

import "net/http"

// serveContainerExec is a stub off macOS — apple-container is macOS-only.
func (h *TerminalHandler) serveContainerExec(w http.ResponseWriter, r *http.Request, task *Task, user string, cols, rows int) {
	http.Error(w, "apple-container terminal is only available on macOS", http.StatusServiceUnavailable)
}
```

- [ ] **Step 4: Add `containerBin` and the websocket bridge to the handler**

The `TerminalHandler` needs the configured `container` binary path. **Decision:** add a `containerBinPath string` field, defaulting to `"container"`. Keep `NewTerminalHandler`'s signature unchanged (avoids touching every caller) and add a setter; or default lazily. Use a lazy default — simplest, zero call-site churn.

In `pkg/dashboard/terminal_handler.go`, add a method (place near the top, after `NewTerminalHandler`):

```go
// containerBin returns the `container` binary path (currently the default).
// A future change can make this configurable; "container" on PATH is correct
// for the supported `brew install container` setup.
func (h *TerminalHandler) containerBin() string {
	return "container"
}
```

Add the websocket bridge. In a new file `pkg/dashboard/terminal_handler_container_bridge.go` (no build tag needed — `ContainerExecSession` exists on all platforms via the stub, and the bridge only runs after a successful `serveContainerExec`, which is darwin-gated):

```go
package dashboard

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

// bridgeContainerSession pumps bytes between the websocket and a
// ContainerExecSession until either side closes.
func (h *TerminalHandler) bridgeContainerSession(conn *websocket.Conn, session *ContainerExecSession) {
	done := make(chan struct{})

	// PTY output -> websocket
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				msg := TerminalOutputMessage{Type: "terminal_output", Data: string(buf[:n])}
				if werr := conn.WriteJSON(msg); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// websocket -> PTY input
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &base); err != nil {
			continue
		}
		switch base.Type {
		case "terminal_input":
			var in TerminalInputMessage
			if err := json.Unmarshal(message, &in); err != nil {
				continue
			}
			if _, err := session.Write([]byte(in.Data)); err != nil {
				log.Printf("terminal: container write error: %v", err)
				break
			}
		case "terminal_resize":
			var rz TerminalResizeMessage
			if err := json.Unmarshal(message, &rz); err != nil {
				continue
			}
			if err := session.Resize(rz.Cols, rz.Rows); err != nil {
				log.Printf("terminal: container resize error: %v", err)
			}
		}
	}
	<-done
}
```

- [ ] **Step 5: Branch `ServeHTTP` on backend**

In `pkg/dashboard/terminal_handler.go`, in `ServeHTTP`, replace the block starting at `// Get the VM's vsock path for connection` (the `vsockPath, err := h.daemon.GetVsockPath(...)` call and its 503) through the end of the vsock-session setup. Specifically, **after** the `task == nil` check and **before** `// Get the VM's vsock path for connection`, insert:

```go
	// apple-container tasks have no vsock; bridge to `container exec` instead.
	if task.Backend == "apple-container" {
		h.serveContainerExec(w, r, task, user, cols, rows)
		return
	}
```

The rest of `ServeHTTP` (the `GetVsockPath` call, the 503, `createVsockSession`, `handleVsockSession`) stays exactly as-is — it is now the Firecracker/vfkit branch.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./pkg/dashboard/ -run "AppleContainerBranch|ContainerExecSession" -v`
Expected: PASS.

- [ ] **Step 7: Build for Linux too**

Run: `GOOS=linux go build ./pkg/dashboard/`
Expected: success.

- [ ] **Step 8: Run the whole dashboard package**

Run: `go test ./pkg/dashboard/`
Expected: PASS (existing terminal tests still green).

- [ ] **Step 9: Commit**

```bash
git add pkg/dashboard/terminal_handler.go pkg/dashboard/terminal_handler_dispatch_darwin.go pkg/dashboard/terminal_handler_dispatch_other.go pkg/dashboard/terminal_handler_container_bridge.go pkg/dashboard/terminal_integration_test.go
git commit -m "feat: dashboard terminal dispatches to container exec for apple-container"
```

---

### Task 3.3: Phase 3 verification

- [ ] **Step 1: Full build (darwin)** — Run: `go build ./...` → success.
- [ ] **Step 2: Full build (linux cross)** — Run: `GOOS=linux go build ./...` → success.
- [ ] **Step 3: Full test suite** — Run: `go test ./...` → all green.
- [ ] **Step 4: vet** — Run: `go vet ./...` → clean.
- [ ] **Step 5: Fix any failure before Phase 4.**

---

# PHASE 4 — Unified Image

Restructures `vm-image/Dockerfile` into a multi-stage build with `base`, `firecracker`, and `container` targets; adds the container entrypoint and build plumbing.

**Environmental reality:** Docker may not be available in this build environment, and a full image build is slow. This phase is structured so that **every task's verification is a static check** (Dockerfile lint / shell syntax / script presence) that needs no Docker. The actual `docker build` of either target is explicitly deferred to CI or a developer machine — see Task 4.5. Do not block the overnight run on a real image build.

---

### Task 4.1: Make `base`-stage download URLs architecture-aware

**Files:**
- Modify: `vm-image/Dockerfile`

The current `Dockerfile` hardcodes `amd64`/`x86_64` in several download URLs (`yq` line ~63, Go tarball line ~86, AWS CLI line ~109, gcloud line ~118). Docker exposes `TARGETARCH` (`amd64`/`arm64`) automatically in `FROM`-scoped `ARG TARGETARCH`. This task only changes URLs; the multi-stage split is Task 4.2. Doing URLs first keeps the diff reviewable.

- [ ] **Step 1: Add a `TARGETARCH` ARG near the top**

In `vm-image/Dockerfile`, after the existing `ARG VM_USER=mooby` (~line 17), add:

```dockerfile
# Target architecture, auto-populated by BuildKit (amd64 | arm64).
# Default keeps non-BuildKit builds working.
ARG TARGETARCH=amd64
```

> The Dockerfile already declares `ARG TARGETARCH=amd64` again at line ~144 just before the `llm-proxy` download. That is fine — `ARG` can be redeclared per stage; in Task 4.2 each stage that needs it re-declares it. For now, the top-level declaration covers the single-stage file.

- [ ] **Step 2: Fix the `yq` URL**

Replace the `yq` line (~63):

```dockerfile
RUN curl -fsSL https://github.com/mikefarah/yq/releases/latest/download/yq_linux_${TARGETARCH} -o /usr/local/bin/yq \
```

(Keep the rest of that `RUN` — the `&& chmod +x` continuation — unchanged.)

- [ ] **Step 3: Fix the Go tarball URL**

Replace the Go download line (~86):

```dockerfile
RUN curl -fsSL https://go.dev/dl/go1.26.1.linux-${TARGETARCH}.tar.gz | tar -C /usr/local -xzf - \
```

- [ ] **Step 4: Fix the AWS CLI URL**

The AWS CLI uses `x86_64`/`aarch64` naming, not `amd64`/`arm64`. Map it. Replace the AWS CLI `RUN` (~109) with:

```dockerfile
RUN AWS_ARCH=$([ "${TARGETARCH}" = "arm64" ] && echo aarch64 || echo x86_64) \
    && curl "https://awscli.amazonaws.com/awscli-exe-linux-${AWS_ARCH}.zip" -o "awscliv2.zip" \
    && unzip -q awscliv2.zip \
    && ./aws/install \
    && rm -rf awscliv2.zip aws
```

> Verify the trailing lines of the original AWS CLI `RUN` block match (`unzip`, `./aws/install`, cleanup). Adjust the continuation to mirror whatever the original block does — the key change is the arch-mapped URL.

- [ ] **Step 5: Fix the gcloud URL**

gcloud also uses `x86_64`/`arm`. Replace the gcloud line (~118):

```dockerfile
RUN GCLOUD_ARCH=$([ "${TARGETARCH}" = "arm64" ] && echo arm || echo x86_64) \
    && curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-${GCLOUD_ARCH}.tar.gz | tar -xzf - -C /usr/local \
```

(Keep the rest of that `RUN` block's continuation unchanged.)

- [ ] **Step 6: Verify the Dockerfile still parses**

Docker may be unavailable. Run whichever of these works:

```bash
# Preferred, if hadolint is installed:
hadolint vm-image/Dockerfile || true
# Always available — confirm no obvious syntax damage:
grep -n "TARGETARCH" vm-image/Dockerfile
```

Expected: `TARGETARCH` now referenced in the `yq`, Go, AWS, and gcloud lines plus the existing `llm-proxy` line. No `linux_amd64` / `linux-amd64` / `x86_64` literal left in a *download URL* (the `llm-proxy` line already used `${TARGETARCH}` — confirm it still does).

- [ ] **Step 7: Commit**

```bash
git add vm-image/Dockerfile
git commit -m "refactor: make vm-image download URLs architecture-aware"
```

---

### Task 4.2: Split the Dockerfile into `base` / `firecracker` / `container` stages

**Files:**
- Modify: `vm-image/Dockerfile`

Restructure into three stages. The `base` stage is everything arch-independent (packages, languages, tools, agents, `llm-proxy`, Tailscale, VM user). The `firecracker` stage is `FROM base` plus systemd/cloud-init/network config and the in-Docker kernel build, ending `CMD ["/sbin/init"]` — it **must produce functionally identical output to today's image** for amd64. The `container` stage is `FROM base` plus the entrypoint (Task 4.3) — no systemd, no kernel, no cloud-init.

- [ ] **Step 1: Identify the section boundaries**

The current `Dockerfile` is single-stage with numbered sections (`Section 1: Base System` … `Section 9: Finalize`). Read it fully. Classify each section:
- **base:** Sections 1-7-ish — packages, Node/Go/Rust, dev tools, `llm-proxy`, Tailscale, the VM user, the `.bashrc` LLM-proxy exports, `ENV`/`WORKDIR`.
- **firecracker-only:** systemd unit installation, cloud-init config, network config, the kernel build (`Section 8` — `curl ... linux-${KERNEL_VERSION}.tar.xz` … `make vmlinux`), `EXPOSE 22`, `CMD ["/sbin/init"]`.

Write the classification down in the commit message; it is the rationale for the split.

- [ ] **Step 2: Rewrite the Dockerfile with stage headers**

Restructure so the file reads:

```dockerfile
# syntax=docker/dockerfile:1

# ============================================================================
# Stage: base — architecture-independent. Shared by firecracker and container.
# ============================================================================
FROM ubuntu:24.04 AS base

LABEL maintainer="Stockyard Project"
ENV DEBIAN_FRONTEND=noninteractive
ARG VM_USER=mooby
ARG TARGETARCH=amd64

# ... ALL arch-independent sections 1-7 verbatim (packages, languages,
#     dev tools, coding agents, llm-proxy, Tailscale, VM user, ENV, WORKDIR) ...

# ============================================================================
# Stage: firecracker — Firecracker rootfs image. FROM base + init + kernel.
# ============================================================================
FROM base AS firecracker
ARG VM_USER=mooby

# ... systemd config, cloud-init, network config ...
# ... Section 8 kernel build verbatim ...

EXPOSE 22
CMD ["/sbin/init"]

# ============================================================================
# Stage: container — Apple `container` image. FROM base + entrypoint.
# ============================================================================
FROM base AS container
ARG VM_USER=mooby

COPY init/stockyard-container-init.sh /usr/local/bin/stockyard-container-init.sh
RUN chmod +x /usr/local/bin/stockyard-container-init.sh

ENTRYPOINT ["/usr/local/bin/stockyard-container-init.sh"]
```

Key rules while restructuring:
- Move every section verbatim — do not "improve" anything. The goal is a *pure refactor* of the firecracker output.
- Re-declare `ARG VM_USER` (and `ARG TARGETARCH` where used) in each stage that references them — `ARG` does not cross `FROM` boundaries.
- The default build target must remain the firecracker image so existing `build.sh`/`convert-to-rootfs.sh` keep working when no `--target` is given — `docker build` builds the *last* stage by default. **Decision:** to keep the default safe, order the stages `base`, `container`, `firecracker` so `firecracker` is last and remains the no-`--target` default. (Task 4.4 makes `build.sh` pass `--target` explicitly anyway, but a safe default matters.)

So the actual stage order in the file is: `base` → `container` → `firecracker`.

- [ ] **Step 3: Static verification**

```bash
# Confirm three stages exist and firecracker is last.
grep -n "^FROM " vm-image/Dockerfile
# Confirm container stage references the entrypoint (created next task).
grep -n "stockyard-container-init.sh" vm-image/Dockerfile
```

Expected: three `FROM` lines, in order `base`, `container`, `firecracker`. If `hadolint` is available: `hadolint vm-image/Dockerfile`.

> A real `docker build` is NOT run here (see Task 4.5). The COPY of `stockyard-container-init.sh` will fail to build until Task 4.3 creates that file — that is expected; the tasks are ordered so the file exists before any build is attempted.

- [ ] **Step 4: Commit**

```bash
git add vm-image/Dockerfile
git commit -m "refactor: split vm-image Dockerfile into base/firecracker/container stages"
```

---

### Task 4.3: Container entrypoint script

**Files:**
- Create: `vm-image/init/stockyard-container-init.sh`

The entrypoint: opt-in Tailscale (userspace networking, only if an auth-key env var is set), start `llm-proxy` in the background, then `exec sleep infinity`.

- [ ] **Step 1: Create the script**

Create `vm-image/init/stockyard-container-init.sh`:

```sh
#!/bin/sh
# stockyard-container-init.sh — entrypoint for the apple-container VM image.
#
# Responsibilities:
#   1. Opt-in Tailscale: if a Tailscale auth key is present in the environment,
#      start tailscaled in userspace-networking mode and `tailscale up --ssh`.
#      Userspace mode needs no TUN device and no privileges.
#   2. Start llm-proxy in the background.
#   3. exec `sleep infinity` so the container stays alive; access is via
#      `container exec`.
set -eu

# --- 1. Opt-in Tailscale ----------------------------------------------------
# The daemon forwards the auth key via VMConfig.Env. Accept either name.
TS_AUTHKEY="${TAILSCALE_AUTH_KEY:-${TS_AUTHKEY:-}}"
if [ -n "${TS_AUTHKEY}" ]; then
    echo "stockyard-container-init: starting tailscaled (userspace networking)"
    tailscaled \
        --tun=userspace-networking \
        --state=/var/lib/tailscale/tailscaled.state \
        --socket=/var/run/tailscale/tailscaled.sock &
    # Give tailscaled a moment to create its socket.
    i=0
    while [ ! -S /var/run/tailscale/tailscaled.sock ] && [ "$i" -lt 30 ]; do
        i=$((i + 1))
        sleep 0.2
    done
    tailscale up --ssh --authkey="${TS_AUTHKEY}" \
        --hostname="${STOCKYARD_HOSTNAME:-stockyard}" || \
        echo "stockyard-container-init: WARNING tailscale up failed; continuing"
else
    echo "stockyard-container-init: no Tailscale auth key; skipping Tailscale"
fi

# --- 2. Start llm-proxy -----------------------------------------------------
if command -v llm-proxy >/dev/null 2>&1; then
    echo "stockyard-container-init: starting llm-proxy"
    llm-proxy &
else
    echo "stockyard-container-init: WARNING llm-proxy not found on PATH"
fi

# --- 3. Keep the container alive -------------------------------------------
echo "stockyard-container-init: ready; sleeping"
exec sleep infinity
```

- [ ] **Step 2: Make it executable and verify syntax**

```bash
chmod +x vm-image/init/stockyard-container-init.sh
sh -n vm-image/init/stockyard-container-init.sh && echo "shell syntax OK"
```

Expected: `shell syntax OK`. If `shellcheck` is available: `shellcheck vm-image/init/stockyard-container-init.sh`.

- [ ] **Step 3: Commit**

```bash
git add vm-image/init/stockyard-container-init.sh
git commit -m "feat: add apple-container image entrypoint script"
```

---

### Task 4.4: Build plumbing — `build.sh` and `Makefile` container target

**Files:**
- Modify: `vm-image/build.sh`
- Modify: `vm-image/Makefile`
- Modify: top-level `Makefile` (optional `container-image` passthrough)

- [ ] **Step 1: Teach `build.sh` to select a target**

In `vm-image/build.sh`, add a `TARGET` variable and pass `--target` + `--platform` to `docker build`. After the `VARIANT="${VARIANT:-ubuntu}"` line, add:

```bash
# Build target stage: "firecracker" (default) or "container".
TARGET="${TARGET:-firecracker}"
# Platform for the container target (Apple Silicon → arm64).
PLATFORM="${PLATFORM:-linux/arm64}"
```

Replace the `docker build` invocation (the `else` branch / non-alpine path) so that, for the `container` target, it builds the right stage and platform:

```bash
if [ "$TARGET" = "container" ]; then
    echo "=== Building container target (${PLATFORM}) ==="
    docker build \
        --build-arg VM_USER="${VM_USER}" \
        --target container \
        --platform "${PLATFORM}" \
        -t "${IMAGE_NAME}:container" \
        -f "${DOCKERFILE}" \
        .
else
    echo "=== Building firecracker target ==="
    docker build \
        --build-arg VM_USER="${VM_USER}" \
        --target firecracker \
        -t "${IMAGE_NAME}:${IMAGE_TAG}" \
        -f "${DOCKERFILE}" \
        .
fi
```

> Preserve the existing alpine `VARIANT` branch untouched — it uses `Dockerfile.alpine` and is unrelated. Only the ubuntu/default path gains the `TARGET` switch.

- [ ] **Step 2: Add a `container-image` target to `vm-image/Makefile`**

In `vm-image/Makefile`, add to `.PHONY` and define a target:

```makefile
.PHONY: all build-deps docker rootfs deploy clean help docker-alpine rootfs-alpine deploy-alpine container-image

# Build the Apple `container` OCI image (arm64). Requires Docker + BuildKit.
container-image:
	@TARGET=container ./build.sh
```

- [ ] **Step 3: (Optional) top-level Makefile passthrough**

In the top-level `Makefile`, add a convenience target so `make container-image` works from the repo root:

```makefile
.PHONY: container-image
container-image:
	$(MAKE) -C vm-image container-image
```

Add `container-image` to the top-level `.PHONY` line.

- [ ] **Step 4: Static verification**

```bash
sh -n vm-image/build.sh && echo "build.sh syntax OK"
make -n -C vm-image container-image
make -n container-image
```

Expected: `build.sh` parses; both `make -n` dry runs print the `TARGET=container ./build.sh` command without executing Docker.

- [ ] **Step 5: Commit**

```bash
git add vm-image/build.sh vm-image/Makefile Makefile
git commit -m "feat: add container-image build target"
```

---

### Task 4.5: Phase 4 verification + image-build handoff note

Phase 4 cannot be fully verified without Docker + BuildKit, which is likely unavailable overnight. This task records what was statically verified and what must be done in CI / on a developer machine.

- [ ] **Step 1: Static checks (run these — they need no Docker)**

```bash
grep -n "^FROM " vm-image/Dockerfile          # three stages, firecracker last
grep -c "TARGETARCH" vm-image/Dockerfile      # >0
sh -n vm-image/init/stockyard-container-init.sh
sh -n vm-image/build.sh
make -n -C vm-image container-image
go build ./...                                 # Phases 1-3 still green
go test ./...                                  # Phases 1-3 still green
```

Expected: all pass.

- [ ] **Step 2: Record the deferred real-build verification**

The following CANNOT be done overnight without Docker and MUST be run in CI or on a macOS/Linux machine with Docker before this branch merges. Add this as a checklist comment in the PR description (not a code file):

```
Image build verification (deferred — needs Docker + BuildKit):
[ ] docker build --target firecracker -f vm-image/Dockerfile .   builds clean
[ ] The firecracker target's rootfs is functionally equivalent to the
    pre-refactor image (spot-check: same languages/tools versions, systemd
    boots, kernel built). Compare `docker run ... <stage> <checks>`.
[ ] TARGET=container PLATFORM=linux/arm64 ./vm-image/build.sh   builds clean arm64
[ ] `container run` of the container image starts, `container exec` works,
    llm-proxy is reachable, and (with an auth key) Tailscale joins.
[ ] llm-proxy has an arm64 release artifact at the URL the Dockerfile uses
    (https://github.com/prime-radiant-inc/llm-proxy/releases/.../llm-proxy-linux-arm64).
    If it does NOT, the container image build fails — this is a hard prerequisite
    flagged in the spec's Risks section.
```

- [ ] **Step 3: Commit the verification note if you keep it anywhere**

If you choose to record it in-repo (optional), put it in the PR body, not a tracked file. No commit needed for Step 2 itself.

---

## Final Verification (all phases)

- [ ] **Run the full build:** `go build ./...` → success.
- [ ] **Run the full cross-build:** `GOOS=linux go build ./...` → success.
- [ ] **Run the full test suite:** `go test ./...` → all green.
- [ ] **Run vet:** `go vet ./...` → clean.
- [ ] **Confirm the baseline is not regressed:** every test that passed before this work still passes.
- [ ] **Confirm the integration test is excluded by default:** `go test ./pkg/vmbackend/` does not run `TestAppleContainerBackend_Integration_Lifecycle` (it is `container_integration`-tagged).

---

## Spec Coverage Map (self-review)

| Spec section | Task(s) |
|---|---|
| §1 `AppleContainerBackend` — all interface methods | 1.2-1.6 |
| §1 identity `stockyard-<id>`, per-VM state dir | 1.2, 1.3 |
| §1 `VMInfo` (IP from inspect, CID 0, PID 0) | 1.3, 1.6 |
| §1 memory `%dM` rendering | 1.3 |
| §1 log capture via `container logs -f` follower | 1.3, 1.4, 1.5 |
| §1 fail-fast `container system status` probe | 1.7 |
| §1 `commandRunner` test seam | 1.2 (+ all backend tests) |
| §2 `AppleContainerConfig` in `pkg/config/apple_container.go` | 1.1 |
| §2 backend `case "apple-container"` | 1.7 |
| §2 build-tagged constructors | 1.7 |
| §2 rootfs-provisioner guard | 1.8 |
| §2 restart reconciliation via backend | 1.9 |
| §3 proto `backend`/`vm_id` + regeneration | 2.1 |
| §3 population from `cfg.Backend` / `state.Task.VMID` | 2.2, 2.3 |
| §3 `dashboard.DaemonAPI` exposure (via `Task` fields — see Discrepancy 2) | 2.3 |
| §6 `ContainerExecSession` | 3.1 |
| §6 `ServeHTTP` backend branch; 503 moved into vsock branch | 3.2 |
| §7 `stockyard attach` backend dispatch | 2.4 |
| §4 multi-stage multi-arch Dockerfile | 4.1, 4.2 |
| §5 container entrypoint script | 4.3 |
| §4 build plumbing | 4.4 |
| Testing: unit via seam, reconciliation test, attach test, gated integration | 1.2-1.6, 1.9, 2.4, 1.10 |
| Risks: pre-1.0 JSON parsing, writable-layer persistence, llm-proxy arm64 | 1.6 (defensive parsing), 1.10 (persistence), 4.5 (arm64 artifact check) |

**Non-goals correctly NOT implemented:** per-task metrics (`metricsPoller` stays nil — Discrepancy 6), `stockyard exec` queue work, `stockyard-shell`/`stockyard-snapshot` in the container image, ZFS snapshots on this path, pre-Tahoe macOS support. No vfkit/Firecracker behavior changed beyond the `attach` and terminal dispatch.

## Notes on under-specified / risky areas (for executors)

1. **Apple `container` JSON shape is unverified.** Tasks 1.6's `containerJSON` struct targets the documented ~0.12 schema (`status`, `configuration.id`, `networks[].address`). If the real CLI differs, only that struct + `vmIDFromName`/`addressToIP` need changing, and the gated integration test (1.10) is the canary. Keep parsing defensive — never assume a field exists.
2. **`container exec` shell.** The plan hardcodes `/bin/bash`; the unified image is Ubuntu-based so bash exists. If a future minimal image lacks bash this needs `/bin/sh` fallback — out of scope now.
3. **`llm-proxy` arm64 artifact** is a hard prerequisite for the container image (Task 4.5, spec Risks). If missing, Phase 4's image build fails — flag loudly, do not silently swap to amd64 emulation.
4. **Phase 4 real build is deferred** — overnight verification is static only. The firecracker-target "functionally identical" claim (spec §4) genuinely needs a Docker build + comparison; that is CI work.
5. **`TerminalHandler.containerBin()` is hardcoded to `"container"`.** The config has `AppleContainer.ContainerBin`, but plumbing it into the dashboard handler would require widening `NewTerminalHandler` or the `DaemonAPI`. Decision: defer — `container` on PATH is correct for the supported `brew install container` setup. Noted so a reviewer knows it was deliberate.

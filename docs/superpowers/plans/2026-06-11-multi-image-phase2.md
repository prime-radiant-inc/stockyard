# Multi-Image Phase 2 (Firecracker image registry) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Named, ZFS-backed Firecracker images behind the existing `stockyard image` surface: `import` ingests, `rm` destroys, `ls` lists, `run --image <name>` clones the named base.

**Architecture:** A daemon-side `imageRegistry` (SQLite `images` table + `zfs.Manager`) implements `vmbackend.ImageValidator` + `ImageLister` for the Firecracker path. At create time the daemon resolves name → `pool/imagesPath/<dataset>@base` and threads it via new `VMConfig.RootfsSnapshot` (empty = today's hardcoded fallback); per-image kernels ride the existing `VMConfig.KernelPath` empty-fallback. Replace/remove destroy that image's tasks through the TaskManager first (orderly scoped scorched-earth), then the dataset.

**Spec:** `docs/superpowers/specs/2026-06-10-multi-image-design.md`, "Phase 2 — Firecracker image registry". Branch: `matt/pri-2150-firecracker-registry` (stacked on `matt/pri-2150-image-cli-surface`, PR #9).

**Conventions:** Sign commits `Co-Authored-By: <YourName>@impl (<model>)`. ZFS exec calls are untestable on macOS — isolate them behind the `registryZFS` interface and unit-test the registry with fakes; `pkg/zfs` additions get unit tests only for pure logic (sanitization). **The live Linux smoke is OUT OF SCOPE for implementers — the controller coordinates it with Matt.**

**Verified ground truth** (scouted at these locations; re-verify before editing):
- `pkg/zfs/zfs.go:207-247` `ImportRootfsImage(ctx, imagesPath, srcPath)` — creates `pool/<imagesPath>/rootfs`, copies the file in as `rootfs.ext4`, snapshots `@base`. `runZFS` helper + `copyFile` exist in the file.
- `pkg/daemon/daemon.go:549-566` `ensureBaseImage()` — raw `zfs list` check + `ImportRootfsImage`; called at startup (line ~282) only for the firecracker backend.
- `pkg/firecracker/client.go:152-156` — hardcoded `snapshotPath := fmt.Sprintf("%s/%s/rootfs@base", c.zfs.PoolName, c.config.ImagesPath)` inside CreateVM's `if c.zfs != nil` branch.
- `pkg/vmbackend/firecracker.go` — `FirecrackerBackend{client *firecracker.Client}`; maps `vmbackend.VMConfig` → `firecracker.VMConfig` field-by-field.
- `pkg/daemon/state.go:119-170` — `migrate()` with CREATE TABLE IF NOT EXISTS + error-swallowed ALTER list; `SnapshotRecord` pattern at :593.
- `pkg/daemon/grpc.go` — phase-1.5 handlers: `ListImages` (ImageLister type-assert on `s.daemon.tasks.backend`), `ImportImage`/`RemoveImage` → `imageMutationUnsupported`; `backendName()` helper.
- `pkg/daemon/tasks.go:47-62` `resolveTaskImage(ctx, requested, backendName, defaultImage, validator)`; caller at :73-88 does `validator, _ := tm.backend.(vmbackend.ImageValidator)`.

---

### Task 1: zfs — sanitizer + parameterized import + helpers

**Files:**
- Modify: `pkg/zfs/zfs.go`
- Test: `pkg/zfs/zfs_test.go`

- [ ] **Step 1 (TDD): sanitizer tests** — add to zfs_test.go:

```go
func TestSanitizeDatasetComponent(t *testing.T) {
	cases := map[string]string{
		"prudence-vm:1.2":             "prudence-vm-1.2",
		"docker.io/library/foo:dev":   "docker.io-library-foo-dev",
		"simple":                      "simple",
		"UPPER_ok.too":                "UPPER_ok.too",
	}
	for in, want := range cases {
		if got := SanitizeDatasetComponent(in); got != want {
			t.Errorf("SanitizeDatasetComponent(%q) = %q, want %q", in, got, want)
		}
	}
}
```

Run `go test ./pkg/zfs/ -run TestSanitizeDatasetComponent -v` — expect compile error (undefined).

- [ ] **Step 2: implement** in zfs.go (near BuildSnapshotName, reusing its character policy):

```go
// SanitizeDatasetComponent maps an image name (an OCI-style ref) to a single
// safe ZFS dataset component: [a-zA-Z0-9._-] pass through, everything else
// (notably '/' and ':') becomes '-'.
func SanitizeDatasetComponent(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, name)
}
```

- [ ] **Step 3: parameterized import + helpers** — refactor `ImportRootfsImage`'s body into:

```go
// ImportImageRootfs imports a rootfs file into pool/imagesPath/<dataset> as
// rootfs.ext4 and snapshots @base. Generalizes the single-image import for
// the PRI-2150 phase-2 registry.
func (m *Manager) ImportImageRootfs(ctx context.Context, imagesPath, dataset, srcPath string) error
```

(identical body, `dataset` replacing the literal `"rootfs"`), keep `ImportRootfsImage` as a one-line wrapper calling it with `"rootfs"`. Then add:

```go
// SnapshotExists reports whether the named snapshot (full path incl. pool)
// exists.
func (m *Manager) SnapshotExists(ctx context.Context, snapshotPath string) bool {
	return exec.CommandContext(ctx, "zfs", "list", "-t", "snapshot", snapshotPath).Run() == nil
}

// DestroyDatasetRecursive destroys a dataset (full path incl. pool) together
// with its snapshots AND dependent clones (zfs destroy -R).
func (m *Manager) DestroyDatasetRecursive(ctx context.Context, datasetPath string) error {
	return m.runZFS(ctx, "destroy", "-R", datasetPath)
}
```

- [ ] **Step 4:** `go test ./pkg/zfs/ -v` all green; `CGO_ENABLED=0 go build ./...` clean. Commit: `feat(PRI-2150): zfs sanitizer, parameterized image import, registry helpers`

---

### Task 2: state — `images` table + ImageRecord CRUD

**Files:**
- Modify: `pkg/daemon/state.go`
- Test: `pkg/daemon/state_test.go`

- [ ] **Step 1 (TDD): roundtrip test** in state_test.go:

```go
func TestImageRecordRoundtrip(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	rec := &ImageRecord{Name: "prudence-vm:1.2", Dataset: "prudence-vm-1.2", KernelPath: "/var/lib/stockyard/k.bin", SizeBytes: 1234, CreatedAt: time.Now()}
	if err := state.CreateImage(rec); err != nil {
		t.Fatalf("CreateImage: %v", err)
	}
	got, err := state.GetImage("prudence-vm:1.2")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if got.Dataset != "prudence-vm-1.2" || got.KernelPath != "/var/lib/stockyard/k.bin" || got.SizeBytes != 1234 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}

	all, err := state.ListImages()
	if err != nil || len(all) != 1 {
		t.Fatalf("ListImages: %v / %d", err, len(all))
	}

	// Dataset collisions must fail (UNIQUE) — different name, same dataset.
	dup := &ImageRecord{Name: "prudence-vm/1.2", Dataset: "prudence-vm-1.2", CreatedAt: time.Now()}
	if err := state.CreateImage(dup); err == nil {
		t.Error("expected UNIQUE violation on dataset collision")
	}

	if err := state.DeleteImage("prudence-vm:1.2"); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}
	if _, err := state.GetImage("prudence-vm:1.2"); err == nil {
		t.Error("expected error after delete")
	}
}
```

Run — expect compile error (`ImageRecord` undefined).

- [ ] **Step 2: implement** — in `migrate()`'s schema block add:

```sql
CREATE TABLE IF NOT EXISTS images (
	name TEXT PRIMARY KEY,
	dataset TEXT NOT NULL UNIQUE,
	kernel_path TEXT DEFAULT '',
	size_bytes INTEGER DEFAULT 0,
	created_at DATETIME NOT NULL
);
```

and add (mirroring the SnapshotRecord/task CRUD style):

```go
// ImageRecord is one registered Firecracker image (PRI-2150 phase 2).
type ImageRecord struct {
	Name       string
	Dataset    string // ZFS dataset component under images path
	KernelPath string // empty = shared default kernel
	SizeBytes  int64
	CreatedAt  time.Time
}

func (s *State) CreateImage(rec *ImageRecord) error
func (s *State) GetImage(name string) (*ImageRecord, error)   // error when absent
func (s *State) ListImages() ([]*ImageRecord, error)          // ordered by name
func (s *State) DeleteImage(name string) error
```

Implement with the file's existing query/Scan conventions (INSERT 5 cols; SELECT name, dataset, kernel_path, size_bytes, created_at).

- [ ] **Step 3:** `go test ./pkg/daemon/ -v` green. Commit: `feat(PRI-2150): images table and ImageRecord store`

---

### Task 3: daemon — imageRegistry (the core)

**Files:**
- Create: `pkg/daemon/image_registry.go`
- Test: `pkg/daemon/image_registry_test.go`

The registry owns name→dataset resolution and the orderly scorched-earth sequence. ZFS and task-destruction go behind small interfaces so the whole thing unit-tests on macOS.

- [ ] **Step 1: write image_registry.go**:

```go
package daemon

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/obra/stockyard/pkg/vmbackend"
	"github.com/obra/stockyard/pkg/zfs"
)

// registryZFS is the slice of zfs.Manager the registry needs; faked in tests.
type registryZFS interface {
	ImportImageRootfs(ctx context.Context, imagesPath, dataset, srcPath string) error
	DestroyDatasetRecursive(ctx context.Context, datasetPath string) error
	SnapshotExists(ctx context.Context, snapshotPath string) bool
}

// imageTaskDestroyer destroys all tasks running a given image (orderly
// teardown via the TaskManager); faked in tests.
type imageTaskDestroyer interface {
	DestroyTasksByImage(ctx context.Context, image string) error
}

// imageRegistry is the Firecracker-side image store: SQLite metadata + ZFS
// base datasets. It implements vmbackend.ImageValidator and ImageLister, so
// resolveTaskImage and the ListImages RPC work identically to apple-container.
type imageRegistry struct {
	state      *State
	zfs        registryZFS
	destroyer  imageTaskDestroyer
	pool       string // e.g. "tank"
	imagesPath string // e.g. "stockyard/images"
}

// snapshotPathFor returns the full clone source for a registered image.
func (r *imageRegistry) snapshotPathFor(rec *ImageRecord) string {
	return fmt.Sprintf("%s/%s/%s@base", r.pool, r.imagesPath, rec.Dataset)
}

func (r *imageRegistry) datasetPathFor(rec *ImageRecord) string {
	return fmt.Sprintf("%s/%s/%s", r.pool, r.imagesPath, rec.Dataset)
}

// ValidateImage implements vmbackend.ImageValidator: a name is valid iff
// registered. The miss error lists registered names, one per line (the
// cross-backend error contract).
func (r *imageRegistry) ValidateImage(ctx context.Context, ref string) error {
	if _, err := r.state.GetImage(ref); err == nil {
		return nil
	}
	recs, lsErr := r.state.ListImages()
	available := "(could not list registered images)"
	if lsErr == nil {
		names := make([]string, len(recs))
		for i, rec := range recs {
			names[i] = rec.Name
		}
		sort.Strings(names)
		available = strings.Join(names, "\n")
	}
	return fmt.Errorf("image %q not found on host; available images:\n%s", ref, available)
}

// ListImages implements vmbackend.ImageLister.
func (r *imageRegistry) ListImages(ctx context.Context) ([]vmbackend.ImageInfo, error) {
	recs, err := r.state.ListImages()
	if err != nil {
		return nil, err
	}
	infos := make([]vmbackend.ImageInfo, len(recs))
	for i, rec := range recs {
		infos[i] = vmbackend.ImageInfo{
			Reference: rec.Name,
			Digest:    "", // FC images carry no digest in v1
			Size:      humanBytes(rec.SizeBytes),
			CreatedAt: rec.CreatedAt.UTC().Format(time.RFC3339),
		}
	}
	return infos, nil
}

// Import registers (or replaces) an image from a rootfs file on this host.
// Replacement is orderly scoped scorched-earth: tasks on the image die via
// the TaskManager, then the dataset, then the row is rewritten.
func (r *imageRegistry) Import(ctx context.Context, name, rootfsPath, kernelPath string) error {
	if name == "" {
		return fmt.Errorf("image name is required")
	}
	st, err := os.Stat(rootfsPath)
	if err != nil {
		return fmt.Errorf("rootfs %q: %w", rootfsPath, err)
	}
	if kernelPath != "" {
		if _, err := os.Stat(kernelPath); err != nil {
			return fmt.Errorf("kernel %q: %w", kernelPath, err)
		}
	}

	dataset := zfs.SanitizeDatasetComponent(name)
	if name == "default" {
		dataset = "rootfs" // the pre-registry location; zero migration
	}

	if existing, err := r.state.GetImage(name); err == nil {
		if err := r.destroyer.DestroyTasksByImage(ctx, name); err != nil {
			return fmt.Errorf("destroy tasks on %q: %w", name, err)
		}
		if err := r.zfs.DestroyDatasetRecursive(ctx, r.datasetPathFor(existing)); err != nil {
			return fmt.Errorf("destroy dataset for %q: %w", name, err)
		}
		if err := r.state.DeleteImage(name); err != nil {
			return err
		}
	}

	if err := r.zfs.ImportImageRootfs(ctx, r.imagesPath, dataset, rootfsPath); err != nil {
		return fmt.Errorf("import rootfs: %w", err)
	}
	rec := &ImageRecord{
		Name: name, Dataset: dataset, KernelPath: kernelPath,
		SizeBytes: st.Size(), CreatedAt: time.Now(),
	}
	if err := r.state.CreateImage(rec); err != nil {
		// Likely a sanitization collision (UNIQUE dataset). Clean up the
		// dataset we just made so the store and ZFS stay consistent.
		r.zfs.DestroyDatasetRecursive(ctx, r.datasetPathFor(rec))
		return fmt.Errorf("register image %q (dataset %q): %w", name, dataset, err)
	}
	return nil
}

// Remove unregisters an image and destroys its dataset and dependents.
func (r *imageRegistry) Remove(ctx context.Context, name string) error {
	if name == "default" {
		return fmt.Errorf("the default image is seeded from daemon config and cannot be removed")
	}
	rec, err := r.state.GetImage(name)
	if err != nil {
		return r.ValidateImage(ctx, name) // reuse the not-found shape
	}
	if err := r.destroyer.DestroyTasksByImage(ctx, name); err != nil {
		return fmt.Errorf("destroy tasks on %q: %w", name, err)
	}
	if err := r.zfs.DestroyDatasetRecursive(ctx, r.datasetPathFor(rec)); err != nil {
		return fmt.Errorf("destroy dataset for %q: %w", name, err)
	}
	return r.state.DeleteImage(name)
}

// EnsureDefault seeds/heals the `default` image from the configured rootfs.
// Generalizes the old ensureBaseImage: row missing → create it; snapshot
// missing → re-import from rootfsPath.
func (r *imageRegistry) EnsureDefault(ctx context.Context, rootfsPath string) error {
	rec, err := r.state.GetImage("default")
	if err != nil {
		rec = &ImageRecord{Name: "default", Dataset: "rootfs", CreatedAt: time.Now()}
		if st, serr := os.Stat(rootfsPath); serr == nil {
			rec.SizeBytes = st.Size()
		}
		if err := r.state.CreateImage(rec); err != nil {
			return fmt.Errorf("seed default image: %w", err)
		}
	}
	if !r.zfs.SnapshotExists(ctx, r.snapshotPathFor(rec)) {
		fmt.Printf("Importing base rootfs image from %s...\n", rootfsPath)
		if err := r.zfs.ImportImageRootfs(ctx, r.imagesPath, rec.Dataset, rootfsPath); err != nil {
			return fmt.Errorf("failed to import base image: %w", err)
		}
		fmt.Println("Base image imported successfully")
	}
	return nil
}

// humanBytes formats like the `container` CLI ("4 MB", "5.6 GB").
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/float64(div)), ".0") + " " + []string{"kB", "MB", "GB", "TB"}[exp]
}
```

- [ ] **Step 2 (TDD-ish, tests after the skeleton given above is typed but BEFORE wiring): image_registry_test.go** — fakes + the behavior matrix:

```go
package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/obra/stockyard/pkg/vmbackend"
)

type fakeRegistryZFS struct {
	imported  [][3]string // imagesPath, dataset, srcPath
	destroyed []string
	snapshots map[string]bool
	importErr error
}

func (f *fakeRegistryZFS) ImportImageRootfs(ctx context.Context, imagesPath, dataset, srcPath string) error {
	if f.importErr != nil {
		return f.importErr
	}
	f.imported = append(f.imported, [3]string{imagesPath, dataset, srcPath})
	return nil
}
func (f *fakeRegistryZFS) DestroyDatasetRecursive(ctx context.Context, p string) error {
	f.destroyed = append(f.destroyed, p)
	return nil
}
func (f *fakeRegistryZFS) SnapshotExists(ctx context.Context, p string) bool { return f.snapshots[p] }

type fakeDestroyer struct{ destroyed []string }

func (f *fakeDestroyer) DestroyTasksByImage(ctx context.Context, image string) error {
	f.destroyed = append(f.destroyed, image)
	return nil
}

func newTestRegistry(t *testing.T) (*imageRegistry, *fakeRegistryZFS, *fakeDestroyer) {
	t.Helper()
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	t.Cleanup(func() { state.Close() })
	fz := &fakeRegistryZFS{snapshots: map[string]bool{}}
	fd := &fakeDestroyer{}
	return &imageRegistry{state: state, zfs: fz, destroyer: fd, pool: "tank", imagesPath: "stockyard/images"}, fz, fd
}

func tempRootfs(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(p, []byte("0123456789"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRegistry_ImportAndResolve(t *testing.T) {
	r, fz, _ := newTestRegistry(t)
	if err := r.Import(context.Background(), "prudence-vm:1.2", tempRootfs(t), ""); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(fz.imported) != 1 || fz.imported[0][1] != "prudence-vm-1.2" {
		t.Errorf("unexpected zfs import calls: %v", fz.imported)
	}
	if err := r.ValidateImage(context.Background(), "prudence-vm:1.2"); err != nil {
		t.Errorf("ValidateImage after import: %v", err)
	}
	rec, _ := r.state.GetImage("prudence-vm:1.2")
	if got := r.snapshotPathFor(rec); got != "tank/stockyard/images/prudence-vm-1.2@base" {
		t.Errorf("snapshotPathFor = %q", got)
	}
	if rec.SizeBytes != 10 {
		t.Errorf("SizeBytes = %d, want 10", rec.SizeBytes)
	}
}

func TestRegistry_ValidateMissListsRegistered(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	r.Import(context.Background(), "a:1", tempRootfs(t), "")
	err := r.ValidateImage(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), `image "nope" not found on host`) || !strings.Contains(err.Error(), "a:1") {
		t.Errorf("miss error wrong: %v", err)
	}
}

func TestRegistry_ReimportScorchesScoped(t *testing.T) {
	r, fz, fd := newTestRegistry(t)
	r.Import(context.Background(), "x:1", tempRootfs(t), "")
	if err := r.Import(context.Background(), "x:1", tempRootfs(t), ""); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if len(fd.destroyed) != 1 || fd.destroyed[0] != "x:1" {
		t.Errorf("tasks not destroyed first: %v", fd.destroyed)
	}
	if len(fz.destroyed) != 1 || fz.destroyed[0] != "tank/stockyard/images/x-1" {
		t.Errorf("dataset not destroyed: %v", fz.destroyed)
	}
}

func TestRegistry_DatasetCollisionFailsAndCleansUp(t *testing.T) {
	r, fz, _ := newTestRegistry(t)
	r.Import(context.Background(), "a:b", tempRootfs(t), "") // dataset a-b
	err := r.Import(context.Background(), "a/b", tempRootfs(t), "") // also a-b
	if err == nil {
		t.Fatal("expected collision error")
	}
	// The just-created dataset for the failed import must be destroyed again.
	if len(fz.destroyed) != 1 {
		t.Errorf("expected cleanup destroy, got %v", fz.destroyed)
	}
}

func TestRegistry_RemoveDefaultRefused(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	if err := r.Remove(context.Background(), "default"); err == nil || !strings.Contains(err.Error(), "cannot be removed") {
		t.Errorf("expected refusal: %v", err)
	}
}

func TestRegistry_RemoveDestroysInOrder(t *testing.T) {
	r, fz, fd := newTestRegistry(t)
	r.Import(context.Background(), "y:1", tempRootfs(t), "")
	if err := r.Remove(context.Background(), "y:1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(fd.destroyed) != 1 || len(fz.destroyed) != 1 {
		t.Errorf("destroy sequence wrong: tasks=%v datasets=%v", fd.destroyed, fz.destroyed)
	}
	if err := r.ValidateImage(context.Background(), "y:1"); err == nil {
		t.Error("expected validation miss after remove")
	}
}

func TestRegistry_EnsureDefault(t *testing.T) {
	r, fz, _ := newTestRegistry(t)
	rf := tempRootfs(t)
	if err := r.EnsureDefault(context.Background(), rf); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	rec, err := r.state.GetImage("default")
	if err != nil || rec.Dataset != "rootfs" {
		t.Fatalf("default not seeded with rootfs dataset: %+v %v", rec, err)
	}
	if len(fz.imported) != 1 {
		t.Errorf("expected import when snapshot missing: %v", fz.imported)
	}
	// Second call with snapshot now present: no re-import.
	fz.snapshots["tank/stockyard/images/rootfs@base"] = true
	if err := r.EnsureDefault(context.Background(), rf); err != nil {
		t.Fatal(err)
	}
	if len(fz.imported) != 1 {
		t.Errorf("re-imported despite existing snapshot: %v", fz.imported)
	}
}

func TestRegistry_ListImages(t *testing.T) {
	r, _, _ := newTestRegistry(t)
	r.Import(context.Background(), "z:1", tempRootfs(t), "/k.bin")
	// Stat of /k.bin would fail; bypass: import with empty kernel then test list.
	infos, err := r.ListImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = infos
}

var _ vmbackend.ImageValidator = (*imageRegistry)(nil)
var _ vmbackend.ImageLister = (*imageRegistry)(nil)
var _ = fmt.Sprintf // keep fmt if otherwise unused
```

NOTE for implementer: the `TestRegistry_ListImages` sketch above is sloppy (kernel stat will fail) — fix it properly: import with empty kernel path, then assert `infos[0].Reference == "z:1"`, `infos[0].Size == "10 B"`, `infos[0].Digest == ""`, CreatedAt parses as RFC3339. Drop the `var _ = fmt.Sprintf` line if fmt is used.

- [ ] **Step 3:** `go test ./pkg/daemon/ -run TestRegistry -v` — all green; full package green. Commit: `feat(PRI-2150): imageRegistry — ZFS-backed named images with orderly replace`

---

### Task 4: TaskManager.DestroyTasksByImage

**Files:**
- Modify: `pkg/daemon/tasks.go`
- Test: extend `pkg/daemon/tasks_image_test.go`

- [ ] Implement on TaskManager. `DestroyTask(ctx context.Context, taskID string) error` exists at tasks.go:559 and removes the task row entirely, so any row returned by ListTasks is destroyable — no status filter needed:

```go
// DestroyTasksByImage destroys every task whose resolved image is name.
// Used by the image registry's scoped scorched-earth replace/remove.
func (tm *TaskManager) DestroyTasksByImage(ctx context.Context, image string) error {
	tasks, err := tm.daemon.state.ListTasks("")
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Image != image {
			continue
		}
		if err := tm.DestroyTask(ctx, t.ID); err != nil {
			return fmt.Errorf("destroy task %s: %w", t.ID, err)
		}
	}
	return nil
}
```

- [ ] Test with the in-memory state: create two task rows with different images via `state.CreateTask`, a TaskManager with nil backend, call DestroyTasksByImage, assert only the matching task got destroyed (status updated). Read how DestroyTask behaves with nil backend first — if it requires a backend, fake the minimal backend the same way grpc_test.go does.
- [ ] Full `go test ./pkg/daemon/` green. Commit: `feat(PRI-2150): TaskManager.DestroyTasksByImage for scoped image replacement`

---

### Task 5: VMConfig.RootfsSnapshot threading

**Files:**
- Modify: `pkg/vmbackend/backend.go` (VMConfig), `pkg/vmbackend/firecracker.go` (mapping), `pkg/firecracker/client.go` (VMConfig struct + CreateVM), test `pkg/firecracker/client_test.go` if a VMConfig-mapping test exists (check)

- [ ] `vmbackend.VMConfig` gains:

```go
	RootfsSnapshot    string // Full ZFS snapshot to clone (PRI-2150 phase 2); empty = backend default
```

- [ ] `firecracker.VMConfig` gains the same field; `vmbackend/firecracker.go`'s CreateVM mapping copies it.
- [ ] `pkg/firecracker/client.go` CreateVM: replace the hardcoded line with:

```go
		snapshotPath := config.RootfsSnapshot
		if snapshotPath == "" {
			// Pre-registry default: the single base image location.
			snapshotPath = fmt.Sprintf("%s/%s/rootfs@base", c.zfs.PoolName, c.config.ImagesPath)
		}
```

- [ ] Build + full tests green; linux cross-compile clean. Commit: `feat(PRI-2150): thread RootfsSnapshot through VMConfig to the clone source`

---

### Task 6: Daemon wiring — registry construction, resolution, RPC routing

**Files:**
- Modify: `pkg/daemon/daemon.go`, `pkg/daemon/tasks.go`, `pkg/daemon/grpc.go`
- Test: extend `pkg/daemon/grpc_test.go`

- [ ] **daemon.go:** in the firecracker branch of backend construction, after the zfs manager exists, build `d.images = &imageRegistry{state: d.state, zfs: d.zfs, destroyer: <set after NewTaskManager>, pool: cfg.ZFS.Pool, imagesPath: cfg.ZFS.ImagesPath}` (add field `images *imageRegistry` to Daemon; set `d.images.destroyer = d.tasks` right after `d.tasks = NewTaskManager(...)`). Replace `ensureBaseImage(ctx)` call with `d.images.EnsureDefault(ctx, d.cfg.Firecracker.RootfsPath)` (delete the old ensureBaseImage function). Guard: only when `d.zfs != nil` (mirror the existing ensureBaseImage call-site condition).
- [ ] **tasks.go CreateTask:** validator selection becomes registry-aware, and the resolved image maps to snapshot/kernel:

Replace

```go
	validator, _ := tm.backend.(vmbackend.ImageValidator)
```

with

```go
	var validator vmbackend.ImageValidator
	if tm.daemon.images != nil {
		validator = tm.daemon.images // Firecracker: the registry validates
	} else {
		validator, _ = tm.backend.(vmbackend.ImageValidator)
	}
```

and after `resolvedImage` is computed, before the vmCfg literal:

```go
	var rootfsSnapshot, imageKernel string
	if tm.daemon.images != nil {
		if rec, err := tm.daemon.state.GetImage(resolvedImage); err == nil {
			rootfsSnapshot = tm.daemon.images.snapshotPathFor(rec)
			imageKernel = rec.KernelPath
		}
	}
```

then add to the vmCfg literal: `RootfsSnapshot: rootfsSnapshot,` and set `KernelPath: imageKernel,` (KernelPath empty keeps the client-side fallback to the configured kernel — verify the literal doesn't already set KernelPath; it doesn't today).

- [ ] **grpc.go:** route the mutation RPCs to the registry when present:

```go
func (s *grpcServer) ImportImage(ctx context.Context, req *pb.ImportImageRequest) (*pb.ImportImageResponse, error) {
	if s.daemon.images != nil {
		if err := s.daemon.images.Import(ctx, req.Name, req.RootfsPath, req.KernelPath); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		return &pb.ImportImageResponse{}, nil
	}
	return nil, s.imageMutationUnsupported("import", "container image load` or `container image pull")
}
```

(RemoveImage symmetric, mapping registry errors with `status.Errorf(codes.InvalidArgument, ...)` except keep the "cannot be removed"/not-found shapes legible.) `ListImages` — replace the type-assert line with explicit lister selection:

```go
	var lister vmbackend.ImageLister
	if s.daemon.images != nil {
		lister = s.daemon.images
	} else {
		lister, _ = s.daemon.tasks.backend.(vmbackend.ImageLister)
	}
	if lister == nil {
		return nil, status.Errorf(codes.Unimplemented,
			"image listing is not supported by the %s backend (PRI-2150 phase 2)", s.backendName())
	}
	images, err := lister.ListImages(ctx)
```

(the rest of the handler is unchanged).

- [ ] **grpc_test.go:** the phase-1.5 firecracker tests change meaning: `TestGRPCServer_RemoveImage_FirecrackerCitesPhase2` is now WRONG — replace it with registry-backed tests: build the server fixture, attach an imageRegistry with the fakes from image_registry_test.go, assert ImportImage/RemoveImage/ListImages round-trip through the registry, and that apple-container (no registry) still redirects. Keep a no-registry-no-lister case asserting Unimplemented.
- [ ] Full `go test ./pkg/...` green; linux cross-compile. Commit: `feat(PRI-2150): wire image registry into resolution, startup, and image RPCs`

---

### Task 7: vm-image deploy uses the registry

**Files:**
- Modify: `vm-image/Makefile` (the `deploy` target, lines ~28-72, and `deploy-alpine` ~88-128)

Per spec: "the deploy target shrinks to build → copy artifact to host → `stockyard image import`. The hand-rolled ZFS surgery leaves the Makefile; the daemon owns its store." Cannot be executed on macOS — make the edits, verify `make -n deploy IMAGE_NAME=foo` renders sensible commands, leave runtime proof to the Linux smoke.

- [ ] Rework `deploy`:

```makefile
# Deployment paths
INSTALL_DIR := /var/lib/stockyard
IMAGE_NAME ?= default
STOCKYARD_BIN ?= $(abspath ..)/bin/stockyard

deploy: rootfs
	@echo "=== Deploying image '$(IMAGE_NAME)' ==="
	sudo cp output/vmlinux.bin $(INSTALL_DIR)/vmlinux.bin
	sudo cp output/rootfs.ext4 $(INSTALL_DIR)/rootfs-$(IMAGE_NAME).ext4
	sudo $(STOCKYARD_BIN) image import $(IMAGE_NAME) --rootfs $(INSTALL_DIR)/rootfs-$(IMAGE_NAME).ext4
	@echo "=== Deployment Complete ==="
```

Notes for the implementer: keep the existing `rootfs` build dependency; DELETE the systemctl stop/start, pkill, and `zfs destroy -R` steps (the daemon now does orderly scoped replacement while running — that's the point); for `IMAGE_NAME=default` ALSO keep copying to the legacy `$(INSTALL_DIR)/rootfs.ext4` (the daemon config's `rootfs_path` points there for startup self-heal). `deploy-alpine` becomes a thin alias: `$(MAKE) deploy IMAGE_NAME=alpine` after its variant build (keep `rootfs-alpine` as its dependency). Update the help text accordingly (remove the "destroys all existing VMs" warning; replacement now only destroys tasks on the replaced image).

- [ ] Verify with `make -n -C vm-image deploy` and `make -n -C vm-image deploy IMAGE_NAME=alpine` (dry-run renders; no sudo executed). Commit: `feat(PRI-2150): vm-image deploy registers images via stockyard image import`

---

### Task 8: Verification + docs touch-up

- [ ] `make test` green; `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` clean; `CGO_ENABLED=0 go build ./...` clean; `make build`.
- [ ] Update `docs/image-contract.md`: the Linux paragraph's "registered-image store (phase 2, PRI-2150)" parenthetical is now reality — reword to present tense and mention `stockyard image import`. Commit: `docs(PRI-2150): image contract reflects the live Firecracker registry`

---

### Task 9: Linux e2e smoke — CONTROLLER + MATT, not implementers

Needs a Linux host with ZFS + Firecracker + the stockyard daemon. Sketch (finalize against the host's real instance config; **do not run against a production daemon without Matt**):

1. Build linux binaries; deliver to host. Restart daemon (or use an isolated instance config with a distinct `images_path`/`vms_path`/socket).
2. `stockyard image ls` → shows `default` seeded from config.
3. `stockyard image import test-img --rootfs <copy of rootfs.ext4>` → `image ls` shows both; `stockyard run --image test-img --name p2smoke` boots; `list` shows IMAGE=test-img.
4. `stockyard run --image nope` → miss error listing `default` and `test-img` one per line.
5. Re-import `test-img` while p2smoke runs → p2smoke is destroyed (orderly), import succeeds.
6. `stockyard image rm test-img` → gone from ls; `image rm default` → refused.
7. Regression: plain `stockyard run` (no --image) still boots from `default`.

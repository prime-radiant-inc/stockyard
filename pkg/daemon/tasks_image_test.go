package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/secrets"
	"github.com/obra/stockyard/pkg/vmbackend"
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

// TestDestroyTasksByImage verifies that DestroyTasksByImage removes only the
// tasks whose Image matches, leaves others intact, and is safe with a nil
// backend (DestroyTask nil-guards all backend/zfs/feed access and ends in
// state.DeleteTask).
func TestDestroyTasksByImage(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	d := &Daemon{state: state}
	tm := NewTaskManager(d, nil) // nil backend: safe per DestroyTask nil-guards

	// Create two tasks with different images.
	taskTarget := &Task{
		ID: "task-target", Name: "target", Command: "sh", Status: "running",
		Image: "my-image", CreatedAt: time.Now(),
	}
	taskOther := &Task{
		ID: "task-other", Name: "other", Command: "sh", Status: "running",
		Image: "other-image", CreatedAt: time.Now(),
	}
	if err := state.CreateTask(taskTarget); err != nil {
		t.Fatalf("CreateTask target: %v", err)
	}
	if err := state.CreateTask(taskOther); err != nil {
		t.Fatalf("CreateTask other: %v", err)
	}

	if err := tm.DestroyTasksByImage(context.Background(), "my-image"); err != nil {
		t.Fatalf("DestroyTasksByImage: %v", err)
	}

	// Matching task's row must be gone.
	if _, err := state.GetTask("task-target"); err == nil {
		t.Error("expected task-target to be deleted, but GetTask succeeded")
	}

	// Other task's row must survive.
	if _, err := state.GetTask("task-other"); err != nil {
		t.Errorf("task-other should survive: %v", err)
	}
}

// capturingBackend records the VMConfig passed to CreateVM so tests can assert
// that registry fields are correctly threaded through.
type capturingBackend struct {
	captured *vmbackend.VMConfig
}

func (b *capturingBackend) CreateVM(_ context.Context, cfg *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	b.captured = cfg
	return &vmbackend.VMInfo{ID: cfg.ID}, nil
}
func (b *capturingBackend) StartVM(_ context.Context, cfg *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return &vmbackend.VMInfo{ID: cfg.ID}, nil
}
func (b *capturingBackend) StopVM(_ context.Context, _ string) error   { return nil }
func (b *capturingBackend) DeleteVM(_ context.Context, _ string) error { return nil }
func (b *capturingBackend) GetVM(_ context.Context, _ string) (*vmbackend.VMState, error) {
	return nil, nil
}
func (b *capturingBackend) ListVMs(_ context.Context) ([]*vmbackend.VMState, error) {
	return nil, nil
}
func (b *capturingBackend) Close() error { return nil }

// newTestDaemonWithRegistry builds a minimal Daemon fixture for VMConfig threading
// tests. zfs is nil so the ZFS workspace-dataset branch in CreateTask is skipped;
// the imageRegistry's own zfs field uses fakeRegistryZFS, which is independent.
func newTestDaemonWithRegistry(t *testing.T) (*Daemon, *capturingBackend) {
	t.Helper()
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	t.Cleanup(func() { state.Close() })

	cb := &capturingBackend{}
	fz := &fakeRegistryZFS{snapshots: map[string]bool{}}
	fd := &fakeDestroyer{}

	d := &Daemon{
		cfg: &config.Config{
			Backend: "firecracker",
			ZFS: config.ZFSConfig{
				Pool:       "tank",
				BasePath:   "stockyard/workspaces",
				ImagesPath: "stockyard/images",
			},
		},
		secrets: &secrets.MockProvider{Secrets: map[string]string{}},
		state:   state,
		// d.zfs intentionally nil: skips ZFS workspace-dataset branch in CreateTask.
	}
	d.images = &imageRegistry{
		state:      state,
		zfs:        fz,
		destroyer:  fd,
		pool:       "tank",
		imagesPath: "stockyard/images",
	}
	d.tasks = NewTaskManager(d, cb)
	d.images.destroyer = d.tasks
	return d, cb
}

// TestCreateTask_VMConfigThreading proves that CreateTask threads the image
// registry record (RootfsSnapshot, KernelPath, Image) into the VMConfig passed
// to the backend, and that an empty Image field resolves to "default".
func TestCreateTask_VMConfigThreading(t *testing.T) {
	d, cb := newTestDaemonWithRegistry(t)

	// Create a temp kernel file so os.Stat in Import passes.
	kernelPath := filepath.Join(t.TempDir(), "vmlinux.bin")
	if err := os.WriteFile(kernelPath, []byte("kernel"), 0644); err != nil {
		t.Fatalf("write kernel: %v", err)
	}

	ctx := context.Background()

	// Import image "t:1" with a kernel.
	if err := d.images.Import(ctx, "t:1", tempRootfs(t), kernelPath); err != nil {
		t.Fatalf("Import t:1: %v", err)
	}

	// CreateTask with Image "t:1".
	task, err := d.tasks.CreateTask(ctx, &CreateTaskRequest{
		Name:        "threading-test",
		Image:       "t:1",
		NoTailscale: true,
	})
	if err != nil {
		t.Fatalf("CreateTask t:1: %v", err)
	}
	if task == nil {
		t.Fatal("CreateTask returned nil task")
	}

	cfg := cb.captured
	if cfg == nil {
		t.Fatal("capturingBackend.captured is nil — CreateVM was not called")
	}
	wantSnapshot := "tank/stockyard/images/t-1@base"
	if cfg.RootfsSnapshot != wantSnapshot {
		t.Errorf("RootfsSnapshot = %q, want %q", cfg.RootfsSnapshot, wantSnapshot)
	}
	if cfg.KernelPath != kernelPath {
		t.Errorf("KernelPath = %q, want %q", cfg.KernelPath, kernelPath)
	}
	if cfg.Image != "t:1" {
		t.Errorf("Image = %q, want %q", cfg.Image, "t:1")
	}

	// Second CreateTask with empty Image resolves to "default" via EnsureDefault.
	if err := d.images.EnsureDefault(ctx, tempRootfs(t)); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	_, err = d.tasks.CreateTask(ctx, &CreateTaskRequest{
		Name:        "default-test",
		Image:       "", // should resolve to "default"
		NoTailscale: true,
	})
	if err != nil {
		t.Fatalf("CreateTask default: %v", err)
	}

	cfg2 := cb.captured
	if !strings.HasSuffix(cfg2.RootfsSnapshot, "/rootfs@base") {
		t.Errorf("default RootfsSnapshot = %q, want suffix /rootfs@base", cfg2.RootfsSnapshot)
	}
	if cfg2.Image != "default" {
		t.Errorf("default Image = %q, want \"default\"", cfg2.Image)
	}
}

// TestCreateTask_ImageRaceGuard proves that CreateTask returns a clear error
// (not a silent rootfsSnapshot="" fallback) when the image record is absent
// at the snapshot-resolution step. We exercise the guard by using an empty
// Image (resolves to "default") while the state has no "default" row — the
// equivalent of the TOCTOU race where the row existed at ValidateImage time
// but was gone by the time CreateTask calls GetImage.
func TestCreateTask_ImageRaceGuard(t *testing.T) {
	d, _ := newTestDaemonWithRegistry(t)
	ctx := context.Background()

	// The state has no "default" row, so GetImage("default") fails.
	// resolveTaskImage resolves "" → "default" without calling ValidateImage
	// (empty string path). The race guard at the GetImage call must fire.
	_, err := d.tasks.CreateTask(ctx, &CreateTaskRequest{
		Name:        "race-test",
		Image:       "", // resolves to "default" — no row exists in state
		NoTailscale: true,
	})
	if err == nil {
		t.Fatal("expected error when image record is missing at resolution time")
	}
	if !strings.Contains(err.Error(), "disappeared during task creation") {
		t.Errorf("error should mention disappearance, got: %v", err)
	}
}

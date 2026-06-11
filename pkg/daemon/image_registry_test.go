package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestRegistry_DatasetCollisionFailsBeforeZFS(t *testing.T) {
	r, fz, _ := newTestRegistry(t)
	r.Import(context.Background(), "a:b", tempRootfs(t), "") // dataset a-b
	err := r.Import(context.Background(), "a/b", tempRootfs(t), "") // also sanitizes to a-b
	if err == nil || !strings.Contains(err.Error(), `already used by image "a:b"`) {
		t.Fatalf("expected legible collision error, got %v", err)
	}
	// The collision must be caught BEFORE any ZFS mutation: exactly the
	// first import's calls, zero destroys.
	if len(fz.imported) != 1 || len(fz.destroyed) != 0 {
		t.Errorf("ZFS touched on collision: imported=%v destroyed=%v", fz.imported, fz.destroyed)
	}
}

func TestRegistry_ImportNameCollidingWithDefaultDataset(t *testing.T) {
	r, fz, _ := newTestRegistry(t)
	if err := r.EnsureDefault(context.Background(), tempRootfs(t)); err != nil {
		t.Fatal(err)
	}
	preImports := len(fz.imported)
	// "rootfs" sanitizes to dataset "rootfs" — owned by "default".
	err := r.Import(context.Background(), "rootfs", tempRootfs(t), "")
	if err == nil || !strings.Contains(err.Error(), `already used by image "default"`) {
		t.Fatalf("expected default-dataset collision error, got %v", err)
	}
	if len(fz.imported) != preImports || len(fz.destroyed) != 0 {
		t.Errorf("ZFS touched on default collision: imported=%v destroyed=%v", fz.imported, fz.destroyed)
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
	if err := r.Import(context.Background(), "z:1", tempRootfs(t), ""); err != nil {
		t.Fatal(err)
	}
	infos, err := r.ListImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 image, got %d", len(infos))
	}
	got := infos[0]
	if got.Reference != "z:1" || got.Size != "10 B" || got.Digest != "" {
		t.Errorf("unexpected ImageInfo: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.CreatedAt); err != nil {
		t.Errorf("CreatedAt not RFC3339: %q", got.CreatedAt)
	}
}

var _ vmbackend.ImageValidator = (*imageRegistry)(nil)
var _ vmbackend.ImageLister = (*imageRegistry)(nil)

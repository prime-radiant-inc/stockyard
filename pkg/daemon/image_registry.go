package daemon

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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
	// mu serializes Import, Remove, and EnsureDefault. Each of those is a
	// multi-statement read-modify-write across SQLite AND ZFS; the collision
	// pre-check is only sound if no other mutator runs between check and create.
	// ValidateImage and ListImages are intentionally left unlocked (reads; a
	// stale listing is acceptable).
	mu         sync.Mutex
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
		if len(recs) == 0 {
			available = "(none registered)"
		} else {
			names := make([]string, len(recs))
			for i, rec := range recs {
				names[i] = rec.Name
			}
			sort.Strings(names)
			available = strings.Join(names, "\n")
		}
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
	r.mu.Lock()
	defer r.mu.Unlock()

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

	// Collision check BEFORE any ZFS mutation. `zfs create -p` succeeds
	// silently on an existing dataset, so without this a sanitization
	// collision (e.g. importing "rootfs" while "default" owns dataset
	// "rootfs") would overwrite the other image's live rootfs.ext4 and the
	// import's failure cleanup would then destroy it. Data loss; never
	// touch ZFS until the name→dataset mapping is known-free.
	if other, err := r.state.GetImageByDataset(dataset); err == nil && other.Name != name {
		return fmt.Errorf("image name %q sanitizes to dataset %q, already used by image %q; choose a different name", name, dataset, other.Name)
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
		// Unexpected (collisions are pre-checked above): keep store and ZFS
		// consistent by removing the dataset this import just created.
		r.zfs.DestroyDatasetRecursive(ctx, r.datasetPathFor(rec))
		return fmt.Errorf("register image %q (dataset %q): %w", name, dataset, err)
	}
	return nil
}

// Remove unregisters an image and destroys its dataset and dependents.
func (r *imageRegistry) Remove(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

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
	r.mu.Lock()
	defer r.mu.Unlock()

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
		// Restat and update the row so SizeBytes reflects the actual file that
		// was imported. A half-completed prior replace can leave a stale size.
		if st, serr := os.Stat(rootfsPath); serr == nil {
			r.state.UpdateImageSize("default", st.Size())
		}
		fmt.Println("Base image imported successfully")
	}
	return nil
}

// humanBytes formats like the `container` CLI ("4 MB", "5.6 GB").
// The unit slice tops out at TB; inputs >= 10^15 (petabyte range) are clamped
// and rendered as large TB values rather than panicking with an out-of-range
// index.
func humanBytes(n int64) string {
	const unit = 1000
	units := []string{"kB", "MB", "GB", "TB"}
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < len(units)-1; v /= unit {
		div *= unit
		exp++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/float64(div)), ".0") + " " + units[exp]
}

// pkg/rootfs/provisioner.go
package rootfs

import "context"

// Provisioner abstracts rootfs image management across different storage backends.
// Each VM gets its own writable copy of a base image. Implementations handle
// the copy-on-write mechanism (ZFS clones, APFS clonefile, or plain file copy).
type Provisioner interface {
	// Clone creates a writable rootfs for the given VM ID.
	// Returns the filesystem path to the new rootfs image.
	Clone(ctx context.Context, vmID string) (string, error)

	// Destroy removes the rootfs for the given VM ID.
	Destroy(ctx context.Context, vmID string) error

	// EnsureBase verifies the base image is ready for cloning.
	EnsureBase(ctx context.Context) error
}

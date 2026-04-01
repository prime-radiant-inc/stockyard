// pkg/vmbackend/backend.go
package vmbackend

import (
	"context"
	"time"
)

// Backend abstracts VM lifecycle management across different hypervisors.
type Backend interface {
	// CreateVM provisions and starts a new VM. Returns info needed to reach it.
	CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error)

	// StartVM restarts a previously stopped VM using its existing rootfs.
	StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error)

	// StopVM gracefully stops a running VM.
	StopVM(ctx context.Context, id string) error

	// DeleteVM stops (if running) and removes all resources for a VM.
	DeleteVM(ctx context.Context, id string) error

	// GetVM returns the current state of a VM.
	GetVM(ctx context.Context, id string) (*VMState, error)

	// ListVMs returns all known VMs.
	ListVMs(ctx context.Context) ([]*VMState, error)

	// Close releases any resources held by the backend.
	Close() error
}

// VMConfig specifies what the daemon needs to create a VM.
// Backend-specific concerns (TAP devices, MMDS, CID allocation) are internal
// to each implementation — they do not appear here.
type VMConfig struct {
	ID                string
	VCPU              int32
	MemoryMB          int32
	KernelPath        string
	RootfsPath        string            // Path to this VM's writable rootfs image
	SSHAuthorizedKeys []string
	CloudInitData     string            // Base64-encoded cloud-init user-data
	DotEnv            []byte
	Env               map[string]string
	Metadata          map[string]string // Labels (task-id, task-name, etc.)
}

// VMInfo is returned after a VM is created or started.
type VMInfo struct {
	ID        string
	PID       int       // OS process ID of the hypervisor
	IP        string    // VM's IP address (may be empty if not yet known)
	CID       uint32    // vsock Context ID (Firecracker-specific, 0 if unused)
	VsockPath string    // Path to vsock UDS (Firecracker-specific, empty if unused)
	StateDir  string    // Directory containing VM state files
	State     string
	CreatedAt time.Time
}

// VMState is a lightweight status check for an existing VM.
type VMState struct {
	ID     string
	PID    int
	Status string // "running", "stopped", "unknown"
}

// Package firecracker provides direct Firecracker microVM management.
package firecracker

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// VMStatus represents the current state of a VM.
type VMStatus int

const (
	VMStatusUnknown VMStatus = iota
	VMStatusPending
	VMStatusRunning
	VMStatusStopped
	VMStatusFailed
)

// String returns a human-readable status string.
func (s VMStatus) String() string {
	switch s {
	case VMStatusPending:
		return "pending"
	case VMStatusRunning:
		return "running"
	case VMStatusStopped:
		return "stopped"
	case VMStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// NetworkConfig specifies network settings for a VM.
type NetworkConfig struct {
	BridgeName string // Bridge to attach tap device to (optional)
}

// VMConfig specifies the configuration for creating a new VM.
type VMConfig struct {
	ID            string
	Namespace     string
	VCPU          int32
	MemoryMB      int32
	RootfsPath    string            // Path to rootfs.ext4
	KernelPath    string            // Path to vmlinux kernel
	KernelArgs    string            // Boot arguments (optional, has defaults)
	CloudInitData string            // Base64-encoded cloud-init user-data
	Network       NetworkConfig
	Metadata      map[string]string // Labels for the VM
}

// Validate checks that the VMConfig has all required fields.
func (c *VMConfig) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("VM ID is required")
	}
	if c.VCPU <= 0 {
		return fmt.Errorf("VCPU must be greater than zero")
	}
	if c.MemoryMB <= 0 {
		return fmt.Errorf("MemoryMB must be greater than zero")
	}
	if c.RootfsPath == "" {
		return fmt.Errorf("RootfsPath is required")
	}
	if c.KernelPath == "" {
		return fmt.Errorf("KernelPath is required")
	}
	return nil
}

// VM represents a Firecracker microVM.
type VM struct {
	ID        string
	Namespace string
	Status    VMStatus
	PID       int      // Process ID of Firecracker
	TapDevice string   // Name of the tap device
	MAC       string   // MAC address
	StateDir  string   // Directory containing VM state
}

// VMInfo represents detailed information about a VM for API mode.
type VMInfo struct {
	ID            string
	Namespace     string
	PID           int
	SocketPath    string // Console socket path
	APISocketPath string // HTTP API socket path
	RootfsPath    string
	State         string
	CreatedAt     time.Time
}

// GenerateVMID creates a unique identifier for a new VM.
func GenerateVMID() string {
	return uuid.New().String()[:8]
}

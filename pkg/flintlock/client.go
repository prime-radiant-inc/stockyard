// Package flintlock provides a client wrapper for the Flintlock microVM service.
package flintlock

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	flintlockv1 "github.com/liquidmetal-dev/flintlock/api/services/microvm/v1alpha1"
	flintlocktypes "github.com/liquidmetal-dev/flintlock/api/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// VMStatus represents the current state of a VM.
type VMStatus int

const (
	VMStatusUnknown VMStatus = iota
	VMStatusPending
	VMStatusCreated
	VMStatusFailed
	VMStatusDeleting
)

// String returns a human-readable status string.
func (s VMStatus) String() string {
	switch s {
	case VMStatusPending:
		return "pending"
	case VMStatusCreated:
		return "created"
	case VMStatusFailed:
		return "failed"
	case VMStatusDeleting:
		return "deleting"
	default:
		return "unknown"
	}
}

// NetworkConfig specifies network settings for a VM.
type NetworkConfig struct {
	EnableTailscale bool
	StaticIP        string
	GatewayIP       string
}

// VMConfig specifies the configuration for creating a new VM.
type VMConfig struct {
	ID            string
	Namespace     string
	VCPU          int32
	MemoryMB      int32
	Image         string
	KernelImage   string
	WorkspacePath string
	CloudInitData string
	Network       NetworkConfig
	Metadata      map[string]string
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
	if c.Image == "" {
		return fmt.Errorf("Image is required")
	}
	return nil
}

// VM represents a running or stopped microVM.
type VM struct {
	ID        string
	UID       string
	Namespace string
	Status    VMStatus
	IP        string
}

// GenerateVMID creates a unique identifier for a new VM.
func GenerateVMID() string {
	return uuid.New().String()[:8]
}

// Client wraps the Flintlock gRPC client.
type Client struct {
	conn   *grpc.ClientConn
	client flintlockv1.MicroVMClient
}

// NewClient creates a new Flintlock client connected to the specified endpoint.
func NewClient(endpoint string) (*Client, error) {
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to flintlock: %w", err)
	}

	return &Client{
		conn:   conn,
		client: flintlockv1.NewMicroVMClient(conn),
	}, nil
}

// Close closes the connection to the Flintlock server.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// CreateVM creates a new microVM with the given configuration.
func (c *Client) CreateVM(ctx context.Context, config *VMConfig) (*VM, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid VM config: %w", err)
	}

	namespace := config.Namespace
	if namespace == "" {
		namespace = "default"
	}

	spec := &flintlocktypes.MicroVMSpec{
		Id:         config.ID,
		Namespace:  namespace,
		Vcpu:       config.VCPU,
		MemoryInMb: config.MemoryMB,
		Kernel: &flintlocktypes.Kernel{
			Image: config.KernelImage,
		},
		RootVolume: &flintlocktypes.Volume{
			Id:         "root",
			IsReadOnly: false,
			Source: &flintlocktypes.VolumeSource{
				ContainerSource: &config.Image,
			},
		},
		Labels:   config.Metadata,
		Metadata: make(map[string]string),
	}

	if config.CloudInitData != "" {
		spec.Metadata["user-data"] = config.CloudInitData
	}

	req := &flintlockv1.CreateMicroVMRequest{
		Microvm: spec,
	}

	resp, err := c.client.CreateMicroVM(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create VM: %w", err)
	}

	return vmFromProto(resp.Microvm), nil
}

// GetVM retrieves information about a VM by its UID.
func (c *Client) GetVM(ctx context.Context, uid string) (*VM, error) {
	req := &flintlockv1.GetMicroVMRequest{
		Uid: uid,
	}

	resp, err := c.client.GetMicroVM(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get VM: %w", err)
	}

	return vmFromProto(resp.Microvm), nil
}

// DeleteVM deletes a VM by its UID.
func (c *Client) DeleteVM(ctx context.Context, uid string) error {
	req := &flintlockv1.DeleteMicroVMRequest{
		Uid: uid,
	}

	_, err := c.client.DeleteMicroVM(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete VM: %w", err)
	}

	return nil
}

// ListVMs returns all VMs in the specified namespace.
func (c *Client) ListVMs(ctx context.Context, namespace string) ([]*VM, error) {
	if namespace == "" {
		namespace = "default"
	}

	req := &flintlockv1.ListMicroVMsRequest{
		Namespace: namespace,
	}

	resp, err := c.client.ListMicroVMs(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	vms := make([]*VM, 0, len(resp.Microvm))
	for _, m := range resp.Microvm {
		vms = append(vms, vmFromProto(m))
	}

	return vms, nil
}

// vmFromProto converts a Flintlock MicroVM to our VM type.
func vmFromProto(m *flintlocktypes.MicroVM) *VM {
	if m == nil {
		return nil
	}

	vm := &VM{}

	if m.Spec != nil {
		vm.ID = m.Spec.Id
		vm.Namespace = m.Spec.Namespace
		if m.Spec.Uid != nil {
			vm.UID = *m.Spec.Uid
		}
	}

	if m.Status != nil {
		vm.Status = statusFromProto(m.Status.State)
	}

	return vm
}

// statusFromProto converts a Flintlock status to our VMStatus type.
func statusFromProto(s flintlocktypes.MicroVMStatus_MicroVMState) VMStatus {
	switch s {
	case flintlocktypes.MicroVMStatus_PENDING:
		return VMStatusPending
	case flintlocktypes.MicroVMStatus_CREATED:
		return VMStatusCreated
	case flintlocktypes.MicroVMStatus_FAILED:
		return VMStatusFailed
	case flintlocktypes.MicroVMStatus_DELETING:
		return VMStatusDeleting
	default:
		return VMStatusUnknown
	}
}

package vmbackend

import (
	"context"

	"github.com/obra/stockyard/pkg/firecracker"
)

// FirecrackerBackend adapts a firecracker.Client to the Backend interface.
type FirecrackerBackend struct {
	client *firecracker.Client
}

// NewFirecrackerBackend wraps an existing firecracker.Client.
func NewFirecrackerBackend(client *firecracker.Client) *FirecrackerBackend {
	return &FirecrackerBackend{client: client}
}

func (b *FirecrackerBackend) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	fcCfg := &firecracker.VMConfig{
		ID:                cfg.ID,
		Namespace:         "stockyard",
		VCPU:              cfg.VCPU,
		MemoryMB:          cfg.MemoryMB,
		KernelPath:        cfg.KernelPath,
		RootfsPath:        cfg.RootfsPath,
		CloudInitData:     cfg.CloudInitData,
		SSHAuthorizedKeys: cfg.SSHAuthorizedKeys,
		DotEnv:            cfg.DotEnv,
		Metadata:          cfg.Metadata,
	}

	// Firecracker-specific fields passed via Env map
	if cfg.Env != nil {
		if v, ok := cfg.Env["_tailscale_auth_key"]; ok {
			fcCfg.TailscaleAuthKey = v
		}
		if v, ok := cfg.Env["_static_ip_args"]; ok {
			fcCfg.StaticIPArgs = v
		}
	}

	// MMDS network config from Metadata
	if cfg.Metadata != nil {
		if ip, ok := cfg.Metadata["_network_ip"]; ok {
			fcCfg.NetworkMMDS = &firecracker.MMDSNetworkConfig{
				IP:      ip,
				Netmask: cfg.Metadata["_network_netmask"],
				Gateway: cfg.Metadata["_network_gateway"],
				DNS:     cfg.Metadata["_network_dns"],
			}
		}
	}

	vm, err := b.client.CreateVM(ctx, fcCfg)
	if err != nil {
		return nil, err
	}

	return &VMInfo{
		ID:        vm.ID,
		PID:       vm.PID,
		CID:       vm.CID,
		VsockPath: vm.VsockPath,
		State:     vm.State,
		CreatedAt: vm.CreatedAt,
	}, nil
}

func (b *FirecrackerBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	fcCfg := &firecracker.VMConfig{
		ID:        cfg.ID,
		Namespace: "stockyard",
		VCPU:      cfg.VCPU,
		MemoryMB:  cfg.MemoryMB,
	}

	vm, err := b.client.StartVM(ctx, fcCfg)
	if err != nil {
		return nil, err
	}

	return &VMInfo{
		ID:        vm.ID,
		PID:       vm.PID,
		CID:       vm.CID,
		VsockPath: vm.VsockPath,
		State:     vm.State,
		CreatedAt: vm.CreatedAt,
	}, nil
}

func (b *FirecrackerBackend) StopVM(ctx context.Context, id string) error {
	return b.client.StopVM(ctx, "stockyard", id)
}

func (b *FirecrackerBackend) DeleteVM(ctx context.Context, id string) error {
	return b.client.DeleteVM(ctx, "stockyard", id)
}

func (b *FirecrackerBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	vm, err := b.client.GetVM(ctx, "stockyard", id)
	if err != nil {
		return nil, err
	}
	return &VMState{
		ID:     vm.ID,
		PID:    vm.PID,
		Status: vm.Status.String(),
	}, nil
}

func (b *FirecrackerBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	vms, err := b.client.ListVMs(ctx, "stockyard")
	if err != nil {
		return nil, err
	}
	var states []*VMState
	for _, vm := range vms {
		states = append(states, &VMState{
			ID:     vm.ID,
			PID:    vm.PID,
			Status: vm.Status.String(),
		})
	}
	return states, nil
}

func (b *FirecrackerBackend) Close() error {
	if b.client == nil {
		return nil
	}
	return b.client.Close()
}

package flintlock

import (
	"testing"
)

func TestVMConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  VMConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: VMConfig{
				ID:       "task-123",
				VCPU:     2,
				MemoryMB: 4096,
				Image:    "ghcr.io/obra/stockyard-vm:latest",
			},
			wantErr: false,
		},
		{
			name: "missing ID",
			config: VMConfig{
				VCPU:     2,
				MemoryMB: 4096,
				Image:    "ghcr.io/obra/stockyard-vm:latest",
			},
			wantErr: true,
		},
		{
			name: "zero VCPU",
			config: VMConfig{
				ID:       "task-123",
				VCPU:     0,
				MemoryMB: 4096,
				Image:    "ghcr.io/obra/stockyard-vm:latest",
			},
			wantErr: true,
		},
		{
			name: "zero memory",
			config: VMConfig{
				ID:       "task-123",
				VCPU:     2,
				MemoryMB: 0,
				Image:    "ghcr.io/obra/stockyard-vm:latest",
			},
			wantErr: true,
		},
		{
			name: "missing image",
			config: VMConfig{
				ID:       "task-123",
				VCPU:     2,
				MemoryMB: 4096,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGenerateVMID(t *testing.T) {
	id1 := GenerateVMID()
	id2 := GenerateVMID()

	if id1 == id2 {
		t.Error("generated IDs should be unique")
	}

	if len(id1) < 8 {
		t.Error("ID should be at least 8 characters")
	}
}

func TestVMStatus_String(t *testing.T) {
	tests := []struct {
		status VMStatus
		want   string
	}{
		{VMStatusPending, "pending"},
		{VMStatusCreated, "created"},
		{VMStatusFailed, "failed"},
		{VMStatusDeleting, "deleting"},
		{VMStatusUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("VMStatus.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNetworkConfig_Defaults(t *testing.T) {
	cfg := NetworkConfig{}

	// EnableTailscale should default to false
	if cfg.EnableTailscale {
		t.Error("EnableTailscale should default to false")
	}
}

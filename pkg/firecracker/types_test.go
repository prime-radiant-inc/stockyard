package firecracker

import (
	"testing"
)

func TestVMStatus_String(t *testing.T) {
	tests := []struct {
		status   VMStatus
		expected string
	}{
		{VMStatusUnknown, "unknown"},
		{VMStatusPending, "pending"},
		{VMStatusRunning, "running"},
		{VMStatusStopped, "stopped"},
		{VMStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("VMStatus.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestVMConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  VMConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: VMConfig{
				ID:         "test-vm",
				VCPU:       2,
				MemoryMB:   1024,
				RootfsPath: "/path/to/rootfs.ext4",
				KernelPath: "/path/to/vmlinux",
			},
			wantErr: false,
		},
		{
			name: "missing ID",
			config: VMConfig{
				VCPU:       2,
				MemoryMB:   1024,
				RootfsPath: "/path/to/rootfs.ext4",
				KernelPath: "/path/to/vmlinux",
			},
			wantErr: true,
		},
		{
			name: "zero VCPU",
			config: VMConfig{
				ID:         "test-vm",
				VCPU:       0,
				MemoryMB:   1024,
				RootfsPath: "/path/to/rootfs.ext4",
				KernelPath: "/path/to/vmlinux",
			},
			wantErr: true,
		},
		{
			name: "zero memory",
			config: VMConfig{
				ID:         "test-vm",
				VCPU:       2,
				MemoryMB:   0,
				RootfsPath: "/path/to/rootfs.ext4",
				KernelPath: "/path/to/vmlinux",
			},
			wantErr: true,
		},
		{
			name: "missing rootfs",
			config: VMConfig{
				ID:         "test-vm",
				VCPU:       2,
				MemoryMB:   1024,
				KernelPath: "/path/to/vmlinux",
			},
			wantErr: true,
		},
		{
			name: "missing kernel",
			config: VMConfig{
				ID:         "test-vm",
				VCPU:       2,
				MemoryMB:   1024,
				RootfsPath: "/path/to/rootfs.ext4",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("VMConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGenerateVMID(t *testing.T) {
	id1 := GenerateVMID()
	id2 := GenerateVMID()

	if id1 == "" {
		t.Error("GenerateVMID() returned empty string")
	}
	if len(id1) != 8 {
		t.Errorf("GenerateVMID() returned ID of length %d, want 8", len(id1))
	}
	if id1 == id2 {
		t.Error("GenerateVMID() returned duplicate IDs")
	}
}

func TestVMInfo_HasAPISocketPath(t *testing.T) {
	vm := &VMInfo{
		ID:            "test123",
		Namespace:     "stockyard",
		APISocketPath: "/var/lib/stockyard/vms/stockyard/test123/api.sock",
	}

	if vm.APISocketPath == "" {
		t.Error("APISocketPath should be set")
	}
}

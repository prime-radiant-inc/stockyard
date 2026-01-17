// pkg/tailscale/tailscale_test.go
package tailscale

import (
	"testing"
)

func TestBuildHostname(t *testing.T) {
	tests := []struct {
		taskID   string
		expected string
	}{
		{"task-abc123", "stockyard-task-abc123"},
		{"vm-xyz", "stockyard-vm-xyz"},
	}

	for _, tt := range tests {
		got := BuildHostname(tt.taskID)
		if got != tt.expected {
			t.Errorf("BuildHostname(%q) = %q, want %q", tt.taskID, got, tt.expected)
		}
	}
}

func TestValidateAuthKey(t *testing.T) {
	tests := []struct {
		key     string
		wantErr bool
	}{
		{"tskey-auth-xxx", false},
		{"tskey-xxx", false},
		{"", true},
		{"invalid", true},
	}

	for _, tt := range tests {
		err := ValidateAuthKey(tt.key)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateAuthKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
		}
	}
}

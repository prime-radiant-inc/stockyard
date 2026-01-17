package firecracker

import (
	"strings"
	"testing"
)

func TestGenerateMAC(t *testing.T) {
	mac1 := GenerateMAC()
	mac2 := GenerateMAC()

	// Check format: 02:xx:xx:xx:xx:xx
	if !strings.HasPrefix(mac1, "02:") {
		t.Errorf("GenerateMAC() = %v, want prefix '02:'", mac1)
	}

	parts := strings.Split(mac1, ":")
	if len(parts) != 6 {
		t.Errorf("GenerateMAC() = %v, want 6 parts", mac1)
	}

	// Check uniqueness
	if mac1 == mac2 {
		t.Error("GenerateMAC() returned duplicate MACs")
	}
}

func TestTapNameForVM(t *testing.T) {
	tests := []struct {
		vmID     string
		expected string
	}{
		{"abc123", "tap-abc123"},
		{"abcdefghij", "tap-abcdefgh"}, // Truncated to 8 chars
		{"ab", "tap-ab"},
	}

	for _, tt := range tests {
		t.Run(tt.vmID, func(t *testing.T) {
			got := TapNameForVM(tt.vmID)
			if got != tt.expected {
				t.Errorf("TapNameForVM(%q) = %v, want %v", tt.vmID, got, tt.expected)
			}
		})
	}
}

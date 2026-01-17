// pkg/vsock/protocol_test.go
package vsock

import (
	"bytes"
	"testing"
)

func TestProtocol_EncodeDecodeRequest(t *testing.T) {
	label := "edit-main.py"

	// Encode
	var buf bytes.Buffer
	if err := EncodeSnapshotRequest(&buf, label); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Decode
	decoded, err := DecodeSnapshotRequest(&buf)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if decoded != label {
		t.Errorf("got %q, want %q", decoded, label)
	}
}

func TestProtocol_EncodeDecodeResponse(t *testing.T) {
	tests := []struct {
		name    string
		success bool
		message string
	}{
		{"success", true, "snapshot-name"},
		{"failure", false, "disk full"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := EncodeSnapshotResponse(&buf, tt.success, tt.message); err != nil {
				t.Fatalf("encode failed: %v", err)
			}

			success, message, err := DecodeSnapshotResponse(&buf)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}

			if success != tt.success {
				t.Errorf("success: got %v, want %v", success, tt.success)
			}
			if message != tt.message {
				t.Errorf("message: got %q, want %q", message, tt.message)
			}
		})
	}
}

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

func TestProtocol_LabelTooLong(t *testing.T) {
	// Create a label > 1024 bytes
	longLabel := make([]byte, 1025)
	for i := range longLabel {
		longLabel[i] = 'x'
	}

	// Encode it (should succeed)
	var buf bytes.Buffer
	if err := EncodeSnapshotRequest(&buf, string(longLabel)); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Decode should fail
	_, err := DecodeSnapshotRequest(&buf)
	if err == nil {
		t.Error("expected error for label > 1024 bytes, got nil")
	}
}

func TestProtocol_MessageTooLong(t *testing.T) {
	// Create a message > 4096 bytes
	longMsg := make([]byte, 4097)
	for i := range longMsg {
		longMsg[i] = 'y'
	}

	// Encode it
	var buf bytes.Buffer
	if err := EncodeSnapshotResponse(&buf, true, string(longMsg)); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Decode should fail
	_, _, err := DecodeSnapshotResponse(&buf)
	if err == nil {
		t.Error("expected error for message > 4096 bytes, got nil")
	}
}

func TestProtocol_EmptyLabel(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeSnapshotRequest(&buf, ""); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	decoded, err := DecodeSnapshotRequest(&buf)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if decoded != "" {
		t.Errorf("got %q, want empty string", decoded)
	}
}

func TestProtocol_EmptyMessage(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeSnapshotResponse(&buf, true, ""); err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	success, message, err := DecodeSnapshotResponse(&buf)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if !success {
		t.Error("expected success=true")
	}
	if message != "" {
		t.Errorf("got %q, want empty string", message)
	}
}

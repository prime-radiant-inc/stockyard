// api_test.go
package firecracker

import (
	"testing"
)

func TestNewAPIClient(t *testing.T) {
	client := NewAPIClient("/tmp/test.sock")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.socketPath != "/tmp/test.sock" {
		t.Errorf("expected socket path /tmp/test.sock, got %s", client.socketPath)
	}
}

//go:build !linux

package firecracker

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteVMFailsClosedWithoutStableProcessHandles(t *testing.T) {
	client, err := NewClient(ClientConfig{StateDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.DeleteVM(context.Background(), "stockyard", "abc12345")
	if !errors.Is(err, errStableProcessHandlesUnsupported) {
		t.Fatalf("DeleteVM error = %v, want unsupported stable process handles", err)
	}
}

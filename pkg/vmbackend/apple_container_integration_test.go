//go:build darwin && container_integration

package vmbackend

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run with: go test -tags container_integration ./pkg/vmbackend/
// Requires macOS 26+, `container` installed, and the service started
// (`container system start`). Set STOCKYARD_TEST_IMAGE to an OCI image
// that runs `sleep infinity` (or similar long-lived entrypoint).
func TestAppleContainerBackend_Integration_Lifecycle(t *testing.T) {
	image := getTestImage(t)
	b := NewAppleContainerBackend(AppleContainerConfig{
		Image:    image,
		StateDir: t.TempDir(),
	})
	defer b.Close()

	ctx := context.Background()
	id := GenerateVMID()
	cfg := &VMConfig{ID: id, VCPU: 2, MemoryMB: 1024, Metadata: map[string]string{"task-id": id}}

	if _, err := b.CreateVM(ctx, cfg); err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	defer b.DeleteVM(ctx, id)

	st, err := b.GetVM(ctx, id)
	if err != nil || st.Status != "running" {
		t.Fatalf("GetVM after create: %+v err=%v", st, err)
	}

	if err := b.StopVM(ctx, id); err != nil {
		t.Fatalf("StopVM: %v", err)
	}
	time.Sleep(2 * time.Second)

	// Writable-layer persistence: a stopped (not deleted) container must restart.
	if _, err := b.StartVM(ctx, cfg); err != nil {
		t.Fatalf("StartVM after stop: %v", err)
	}
	st, err = b.GetVM(ctx, id)
	if err != nil || st.Status != "running" {
		t.Fatalf("GetVM after restart: %+v err=%v", st, err)
	}
}

func getTestImage(t *testing.T) string {
	t.Helper()
	img := envOrSkip(t, "STOCKYARD_TEST_IMAGE")
	return img
}

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := getenv(key)
	if v == "" {
		t.Skipf("%s not set; skipping integration test", key)
	}
	return v
}

func getenv(k string) string { return os.Getenv(k) }

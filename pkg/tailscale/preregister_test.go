// pkg/tailscale/preregister_test.go
package tailscale

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPreRegistrar(t *testing.T) {
	// Skip if no auth key available
	authKey := os.Getenv("TAILSCALE_AUTH_KEY")
	if authKey == "" {
		t.Skip("TAILSCALE_AUTH_KEY not set")
	}

	tempDir := t.TempDir()
	pr := NewPreRegistrar(authKey, tempDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	node, err := pr.PreRegister(ctx, "test-prereg-"+time.Now().Format("150405"))
	if err != nil {
		t.Fatalf("PreRegister failed: %v", err)
	}

	if node.Hostname == "" {
		t.Error("expected non-empty hostname")
	}
	if len(node.State) == 0 {
		t.Error("expected non-empty state")
	}
	if node.IP == "" {
		t.Error("expected non-empty IP")
	}

	t.Logf("Pre-registered node: hostname=%s ip=%s state_size=%d",
		node.Hostname, node.IP, len(node.State))
}

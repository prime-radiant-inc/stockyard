// pkg/vsock/listener_test.go
package vsock

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotServer_UnixFallback(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	// Track snapshot requests
	var receivedLabel string
	handler := func(vmID, label string) error {
		receivedLabel = label
		return nil
	}

	// Create server with Unix socket fallback
	server := NewSnapshotServer(handler)
	server.UnixSocketPath = sockPath

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server
	go func() {
		if err := server.ListenUnix(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("server error: %v", err)
		}
	}()

	// Wait for socket
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Connect and send request
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Send request
	if err := EncodeSnapshotRequest(conn, "test-label"); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	// Read response
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	success, msg, err := DecodeSnapshotResponse(conn)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	if !success {
		t.Errorf("expected success, got failure: %s", msg)
	}

	if receivedLabel != "test-label" {
		t.Errorf("label: got %q, want %q", receivedLabel, "test-label")
	}
}

// pkg/vsock/integration_test.go
//go:build integration

package vsock

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegration_SnapshotRequest(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") == "" {
		t.Skip("skipping integration test; set INTEGRATION_TEST=1")
	}

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")

	snapshots := make(chan string, 10)
	handler := func(vmID, label string) error {
		snapshots <- label
		return nil
	}

	server := NewSnapshotServer(handler)
	server.UnixSocketPath = sockPath

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go server.ListenUnix(ctx)

	// Wait for server
	time.Sleep(100 * time.Millisecond)

	// Simulate multiple snapshot requests
	labels := []string{"edit-main.py", "bash-npm-test", "write-config.json"}

	for _, label := range labels {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}

		if err := EncodeSnapshotRequest(conn, label); err != nil {
			conn.Close()
			t.Fatalf("encode: %v", err)
		}

		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		success, _, err := DecodeSnapshotResponse(conn)
		conn.Close()

		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !success {
			t.Errorf("expected success for %q", label)
		}
	}

	// Verify all snapshots received
	for _, expected := range labels {
		select {
		case got := <-snapshots:
			if got != expected {
				t.Errorf("snapshot: got %q, want %q", got, expected)
			}
		case <-time.After(time.Second):
			t.Errorf("timeout waiting for snapshot %q", expected)
		}
	}
}

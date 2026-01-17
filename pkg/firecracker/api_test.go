// api_test.go
package firecracker

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
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

func TestAPIClient_put(t *testing.T) {
	// Create a test Unix socket server
	socketPath := filepath.Join(t.TempDir(), "test.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()

	// Track what the server receives
	var receivedPath string
	var receivedBody []byte

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			receivedBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go server.Serve(listener)
	defer server.Close()

	client := NewAPIClient(socketPath)
	err = client.put(context.Background(), "/test-endpoint", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("put failed: %v", err)
	}

	if receivedPath != "/test-endpoint" {
		t.Errorf("expected path /test-endpoint, got %s", receivedPath)
	}
	if !strings.Contains(string(receivedBody), `"key":"value"`) {
		t.Errorf("expected body to contain key:value, got %s", receivedBody)
	}
}

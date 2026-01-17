// api_test.go
package firecracker

import (
	"context"
	"encoding/json"
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

func TestAPIClient_SetBootSource(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	var receivedPath string
	var receivedBody map[string]interface{}

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go server.Serve(listener)
	defer server.Close()

	client := NewAPIClient(socketPath)
	err := client.SetBootSource(context.Background(), "/path/to/kernel", "console=ttyS0")
	if err != nil {
		t.Fatalf("SetBootSource failed: %v", err)
	}

	if receivedPath != "/boot-source" {
		t.Errorf("expected path /boot-source, got %s", receivedPath)
	}
	if receivedBody["kernel_image_path"] != "/path/to/kernel" {
		t.Errorf("wrong kernel path: %v", receivedBody)
	}
	if receivedBody["boot_args"] != "console=ttyS0" {
		t.Errorf("wrong boot args: %v", receivedBody)
	}
}

func TestAPIClient_SetDrive(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	var receivedPath string
	var receivedBody map[string]interface{}

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go server.Serve(listener)
	defer server.Close()

	client := NewAPIClient(socketPath)
	err := client.SetDrive(context.Background(), "rootfs", "/path/to/rootfs.ext4", true, false)
	if err != nil {
		t.Fatalf("SetDrive failed: %v", err)
	}

	if receivedPath != "/drives/rootfs" {
		t.Errorf("expected path /drives/rootfs, got %s", receivedPath)
	}
	if receivedBody["drive_id"] != "rootfs" {
		t.Errorf("wrong drive_id: %v", receivedBody)
	}
	if receivedBody["path_on_host"] != "/path/to/rootfs.ext4" {
		t.Errorf("wrong path_on_host: %v", receivedBody)
	}
	if receivedBody["is_root_device"] != true {
		t.Errorf("wrong is_root_device: %v", receivedBody)
	}
	if receivedBody["is_read_only"] != false {
		t.Errorf("wrong is_read_only: %v", receivedBody)
	}
}

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

func TestAPIClient_SetNetworkInterface(t *testing.T) {
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
	err := client.SetNetworkInterface(context.Background(), "eth0", "02:FC:00:00:00:01", "tap-abc123")
	if err != nil {
		t.Fatalf("SetNetworkInterface failed: %v", err)
	}

	if receivedPath != "/network-interfaces/eth0" {
		t.Errorf("expected path /network-interfaces/eth0, got %s", receivedPath)
	}
	if receivedBody["iface_id"] != "eth0" {
		t.Errorf("wrong iface_id: %v", receivedBody)
	}
	if receivedBody["guest_mac"] != "02:FC:00:00:00:01" {
		t.Errorf("wrong guest_mac: %v", receivedBody)
	}
	if receivedBody["host_dev_name"] != "tap-abc123" {
		t.Errorf("wrong host_dev_name: %v", receivedBody)
	}
}

func TestAPIClient_SetMachineConfig(t *testing.T) {
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
	err := client.SetMachineConfig(context.Background(), 2, 1024)
	if err != nil {
		t.Fatalf("SetMachineConfig failed: %v", err)
	}

	if receivedPath != "/machine-config" {
		t.Errorf("expected path /machine-config, got %s", receivedPath)
	}
	if int(receivedBody["vcpu_count"].(float64)) != 2 {
		t.Errorf("wrong vcpu_count: %v", receivedBody)
	}
	if int(receivedBody["mem_size_mib"].(float64)) != 1024 {
		t.Errorf("wrong mem_size_mib: %v", receivedBody)
	}
}

func TestAPIClient_SetMMDSConfig(t *testing.T) {
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
	err := client.SetMMDSConfig(context.Background(), []string{"eth0"})
	if err != nil {
		t.Fatalf("SetMMDSConfig failed: %v", err)
	}

	if receivedPath != "/mmds/config" {
		t.Errorf("expected path /mmds/config, got %s", receivedPath)
	}
	ifaces := receivedBody["network_interfaces"].([]interface{})
	if len(ifaces) != 1 || ifaces[0] != "eth0" {
		t.Errorf("wrong network_interfaces: %v", receivedBody)
	}
	if receivedBody["version"] != "V2" {
		t.Errorf("wrong version: %v", receivedBody)
	}
}

func TestAPIClient_SetMMDSData(t *testing.T) {
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
	data := map[string]interface{}{
		"latest": map[string]interface{}{
			"meta-data": map[string]string{
				"instance-id":    "i-test123",
				"local-hostname": "stockyard-test123",
			},
			"user-data": "#cloud-config\nhostname: test\n",
		},
	}
	err := client.SetMMDSData(context.Background(), data)
	if err != nil {
		t.Fatalf("SetMMDSData failed: %v", err)
	}

	if receivedPath != "/mmds" {
		t.Errorf("expected path /mmds, got %s", receivedPath)
	}
	latest := receivedBody["latest"].(map[string]interface{})
	metadata := latest["meta-data"].(map[string]interface{})
	if metadata["instance-id"] != "i-test123" {
		t.Errorf("wrong instance-id: %v", receivedBody)
	}
}

func TestAPIClient_StartInstance(t *testing.T) {
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
	err := client.StartInstance(context.Background())
	if err != nil {
		t.Fatalf("StartInstance failed: %v", err)
	}

	if receivedPath != "/actions" {
		t.Errorf("expected path /actions, got %s", receivedPath)
	}
	if receivedBody["action_type"] != "InstanceStart" {
		t.Errorf("wrong action_type: %v", receivedBody)
	}
}

func TestAPIClient_SendCtrlAltDel(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	var receivedBody map[string]interface{}

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go server.Serve(listener)
	defer server.Close()

	client := NewAPIClient(socketPath)
	err := client.SendCtrlAltDel(context.Background())
	if err != nil {
		t.Fatalf("SendCtrlAltDel failed: %v", err)
	}

	if receivedBody["action_type"] != "SendCtrlAltDel" {
		t.Errorf("wrong action_type: %v", receivedBody)
	}
}

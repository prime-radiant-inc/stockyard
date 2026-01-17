# Firecracker API Mode Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor Firecracker client from `--no-api` mode to full API mode, enabling MMDS for cloud-init data delivery to VMs.

**Architecture:** Start Firecracker with `--api-sock` instead of `--no-api --config-file`. Configure VM via HTTP PUT requests to Unix socket. Use MMDS to deliver cloud-init data that guest retrieves via 169.254.169.254.

**Tech Stack:** Go stdlib `net/http` with Unix socket transport, Firecracker HTTP API, MMDS V2, cloud-init IMDS datasource.

**Spec:** See `docs/specs/firecracker-api-mode.md` for detailed API workflow and data formats.

---

## Task 1: Create API Client Foundation

**Files:**
- Create: `pkg/firecracker/api.go`
- Test: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test for APIClient construction

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestNewAPIClient -v`
Expected: FAIL with "undefined: NewAPIClient"

### Step 3: Write minimal implementation

```go
// api.go
package firecracker

import (
	"context"
	"net"
	"net/http"
	"time"
)

// APIClient handles HTTP communication with Firecracker API socket.
type APIClient struct {
	socketPath string
	httpClient *http.Client
}

// NewAPIClient creates a client for the Firecracker HTTP API.
func NewAPIClient(socketPath string) *APIClient {
	return &APIClient{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
			Timeout: 30 * time.Second,
		},
	}
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestNewAPIClient -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add APIClient foundation for Unix socket HTTP"
```

---

## Task 2: Add PUT Request Helper

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test for put method

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_put -v`
Expected: FAIL with "client.put undefined"

### Step 3: Write minimal implementation

Add to `api.go`:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// put sends a PUT request to the Firecracker API.
func (a *APIClient) put(ctx context.Context, path string, body interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://localhost"+path, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_put -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add put helper for API requests"
```

---

## Task 3: Add SetBootSource API Method

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_SetBootSource -v`
Expected: FAIL with "SetBootSource undefined"

### Step 3: Write minimal implementation

```go
// SetBootSource configures the kernel and boot arguments.
func (a *APIClient) SetBootSource(ctx context.Context, kernelPath, bootArgs string) error {
	return a.put(ctx, "/boot-source", map[string]string{
		"kernel_image_path": kernelPath,
		"boot_args":         bootArgs,
	})
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_SetBootSource -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SetBootSource API method"
```

---

## Task 4: Add SetDrive API Method

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_SetDrive -v`
Expected: FAIL with "SetDrive undefined"

### Step 3: Write minimal implementation

```go
// SetDrive configures a block device.
func (a *APIClient) SetDrive(ctx context.Context, driveID, path string, isRoot, isReadOnly bool) error {
	return a.put(ctx, "/drives/"+driveID, map[string]interface{}{
		"drive_id":       driveID,
		"path_on_host":   path,
		"is_root_device": isRoot,
		"is_read_only":   isReadOnly,
	})
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_SetDrive -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SetDrive API method"
```

---

## Task 5: Add SetNetworkInterface API Method

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_SetNetworkInterface -v`
Expected: FAIL with "SetNetworkInterface undefined"

### Step 3: Write minimal implementation

```go
// SetNetworkInterface configures a network interface.
func (a *APIClient) SetNetworkInterface(ctx context.Context, ifaceID, guestMAC, hostDevName string) error {
	return a.put(ctx, "/network-interfaces/"+ifaceID, map[string]interface{}{
		"iface_id":      ifaceID,
		"guest_mac":     guestMAC,
		"host_dev_name": hostDevName,
	})
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_SetNetworkInterface -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SetNetworkInterface API method"
```

---

## Task 6: Add SetMachineConfig API Method

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_SetMachineConfig -v`
Expected: FAIL with "SetMachineConfig undefined"

### Step 3: Write minimal implementation

```go
// SetMachineConfig configures vCPUs and memory.
func (a *APIClient) SetMachineConfig(ctx context.Context, vcpus, memMB int32) error {
	return a.put(ctx, "/machine-config", map[string]interface{}{
		"vcpu_count":   vcpus,
		"mem_size_mib": memMB,
		"smt":          false,
	})
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_SetMachineConfig -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SetMachineConfig API method"
```

---

## Task 7: Add MMDS Configuration Methods

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test for SetMMDSConfig

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_SetMMDSConfig -v`
Expected: FAIL with "SetMMDSConfig undefined"

### Step 3: Write minimal implementation

```go
// SetMMDSConfig enables MMDS on the specified network interfaces.
func (a *APIClient) SetMMDSConfig(ctx context.Context, networkInterfaces []string) error {
	return a.put(ctx, "/mmds/config", map[string]interface{}{
		"network_interfaces": networkInterfaces,
		"version":            "V2",
	})
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_SetMMDSConfig -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SetMMDSConfig API method"
```

---

## Task 8: Add SetMMDSData Method

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_SetMMDSData -v`
Expected: FAIL with "SetMMDSData undefined"

### Step 3: Write minimal implementation

```go
// SetMMDSData sets the MMDS data that will be available to the guest.
func (a *APIClient) SetMMDSData(ctx context.Context, data map[string]interface{}) error {
	return a.put(ctx, "/mmds", data)
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_SetMMDSData -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SetMMDSData API method"
```

---

## Task 9: Add StartInstance Method

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_StartInstance -v`
Expected: FAIL with "StartInstance undefined"

### Step 3: Write minimal implementation

```go
// StartInstance boots the configured VM.
func (a *APIClient) StartInstance(ctx context.Context) error {
	return a.put(ctx, "/actions", map[string]string{
		"action_type": "InstanceStart",
	})
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_StartInstance -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add StartInstance API method"
```

---

## Task 10: Add SendCtrlAltDel Method for Graceful Shutdown

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_SendCtrlAltDel -v`
Expected: FAIL with "SendCtrlAltDel undefined"

### Step 3: Write minimal implementation

```go
// SendCtrlAltDel sends Ctrl+Alt+Del for graceful shutdown.
func (a *APIClient) SendCtrlAltDel(ctx context.Context) error {
	return a.put(ctx, "/actions", map[string]string{
		"action_type": "SendCtrlAltDel",
	})
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_SendCtrlAltDel -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SendCtrlAltDel for graceful shutdown"
```

---

## Task 11: Add WaitForSocket Helper

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

### Step 1: Write the failing test

```go
func TestAPIClient_WaitForSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "delayed.sock")

	// Start listener after delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		listener, _ := net.Listen("unix", socketPath)
		defer listener.Close()
		time.Sleep(2 * time.Second) // Keep alive
	}()

	client := NewAPIClient(socketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.WaitForSocket(ctx)
	if err != nil {
		t.Fatalf("WaitForSocket failed: %v", err)
	}
}

func TestAPIClient_WaitForSocket_Timeout(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "never.sock")
	client := NewAPIClient(socketPath)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := client.WaitForSocket(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestAPIClient_WaitForSocket -v`
Expected: FAIL with "WaitForSocket undefined"

### Step 3: Write minimal implementation

```go
// WaitForSocket waits for the API socket to become available.
func (a *APIClient) WaitForSocket(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for socket %s: %w", a.socketPath, ctx.Err())
		case <-ticker.C:
			conn, err := net.Dial("unix", a.socketPath)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestAPIClient_WaitForSocket -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add WaitForSocket with retry logic"
```

---

## Task 12: Add BuildMMDSData Helper

**Files:**
- Modify: `pkg/firecracker/cloudinit.go`
- Modify: `pkg/firecracker/cloudinit_test.go` (create if needed)

### Step 1: Write the failing test

```go
// cloudinit_test.go
package firecracker

import (
	"strings"
	"testing"
)

func TestBuildMMDSData(t *testing.T) {
	cloudInitYAML := "#cloud-config\nhostname: test-vm\n"

	data := BuildMMDSData("i-abc123", "stockyard-abc123", cloudInitYAML)

	latest, ok := data["latest"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'latest' key")
	}

	metadata, ok := latest["meta-data"].(map[string]string)
	if !ok {
		t.Fatal("missing 'meta-data' key")
	}

	if metadata["instance-id"] != "i-abc123" {
		t.Errorf("wrong instance-id: %s", metadata["instance-id"])
	}
	if metadata["local-hostname"] != "stockyard-abc123" {
		t.Errorf("wrong local-hostname: %s", metadata["local-hostname"])
	}

	userData, ok := latest["user-data"].(string)
	if !ok {
		t.Fatal("missing 'user-data' key")
	}
	if !strings.HasPrefix(userData, "#cloud-config") {
		t.Errorf("user-data should start with #cloud-config: %s", userData)
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestBuildMMDSData -v`
Expected: FAIL with "undefined: BuildMMDSData"

### Step 3: Write minimal implementation

Add to `cloudinit.go`:

```go
// BuildMMDSData constructs the MMDS data structure for cloud-init.
func BuildMMDSData(instanceID, hostname, cloudInitYAML string) map[string]interface{} {
	return map[string]interface{}{
		"latest": map[string]interface{}{
			"meta-data": map[string]string{
				"instance-id":    instanceID,
				"local-hostname": hostname,
			},
			"user-data": cloudInitYAML,
		},
	}
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestBuildMMDSData -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/cloudinit.go pkg/firecracker/cloudinit_test.go
git commit -m "feat(firecracker): add BuildMMDSData helper for MMDS format"
```

---

## Task 13: Refactor Client to Store API Socket Path

**Files:**
- Modify: `pkg/firecracker/types.go`
- Modify: `pkg/firecracker/client.go`

### Step 1: Write the failing test

```go
// Add to types_test.go or client_test.go
func TestVMInfo_HasAPISocketPath(t *testing.T) {
	vm := &VMInfo{
		ID:            "test123",
		Namespace:     "stockyard",
		APISocketPath: "/var/lib/stockyard/vms/stockyard/test123/api.sock",
	}

	if vm.APISocketPath == "" {
		t.Error("APISocketPath should be set")
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./pkg/firecracker -run TestVMInfo_HasAPISocketPath -v`
Expected: FAIL with "unknown field 'APISocketPath'"

### Step 3: Write minimal implementation

Update `types.go`:

```go
type VMInfo struct {
	ID            string
	Namespace     string
	PID           int
	SocketPath    string // Console socket path
	APISocketPath string // HTTP API socket path
	RootfsPath    string
	State         string
	CreatedAt     time.Time
}
```

### Step 4: Run test to verify it passes

Run: `go test ./pkg/firecracker -run TestVMInfo_HasAPISocketPath -v`
Expected: PASS

### Step 5: Commit

```bash
git add pkg/firecracker/types.go pkg/firecracker/types_test.go
git commit -m "feat(firecracker): add APISocketPath to VMInfo"
```

---

## Task 14: Refactor CreateVM to Use API Mode

**Files:**
- Modify: `pkg/firecracker/client.go`

This is the main refactoring task. The current implementation uses `--no-api --config-file`. We'll change it to:
1. Start firecracker with `--api-sock`
2. Wait for socket
3. Configure via API calls
4. Set MMDS data
5. Start instance

### Step 1: Write the failing test

```go
// client_test.go - add integration test marker
func TestCreateVM_APIMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	// This test requires firecracker binary and root privileges
	// It will be tested manually first, then can be added to CI
	t.Skip("manual integration test")
}
```

### Step 2: Implement the refactored CreateVM

Replace the CreateVM implementation in `client.go`. Key changes:

```go
func (c *Client) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	// ... (keep existing setup: generate ID, create dirs, copy rootfs, setup TAP)

	// Change: use API socket instead of config file
	apiSocketPath := filepath.Join(vmDir, "api.sock")

	// Start firecracker with API socket (not --no-api)
	cmd := exec.Command(c.firecrackerPath,
		"--api-sock", apiSocketPath,
	)
	// ... (keep stdout/stderr redirection)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start firecracker: %w", err)
	}

	// Create API client and wait for socket
	apiClient := NewAPIClient(apiSocketPath)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := apiClient.WaitForSocket(waitCtx); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("wait for API socket: %w", err)
	}

	// Configure via API
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
	if err := apiClient.SetBootSource(ctx, c.kernelPath, bootArgs); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("set boot source: %w", err)
	}

	if err := apiClient.SetDrive(ctx, "rootfs", rootfsPath, true, false); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("set drive: %w", err)
	}

	if err := apiClient.SetNetworkInterface(ctx, "eth0", macAddr, tapName); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("set network interface: %w", err)
	}

	if err := apiClient.SetMachineConfig(ctx, cfg.VCPU, cfg.MemoryMB); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("set machine config: %w", err)
	}

	// Configure MMDS with cloud-init data
	if err := apiClient.SetMMDSConfig(ctx, []string{"eth0"}); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("set MMDS config: %w", err)
	}

	hostname := fmt.Sprintf("stockyard-%s", cfg.ID)
	mmdsData := BuildMMDSData("i-"+cfg.ID, hostname, cfg.CloudInitData)
	if err := apiClient.SetMMDSData(ctx, mmdsData); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("set MMDS data: %w", err)
	}

	// Start the instance
	if err := apiClient.StartInstance(ctx); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("start instance: %w", err)
	}

	// Return VM info with API socket path
	return &VMInfo{
		ID:            cfg.ID,
		Namespace:     cfg.Namespace,
		PID:           cmd.Process.Pid,
		APISocketPath: apiSocketPath,
		RootfsPath:    rootfsPath,
		State:         "running",
		CreatedAt:     time.Now(),
	}, nil
}
```

### Step 3: Run existing tests

Run: `go test ./pkg/firecracker -v`
Expected: Tests pass (unit tests use mocks, integration requires manual testing)

### Step 4: Commit

```bash
git add pkg/firecracker/client.go
git commit -m "refactor(firecracker): switch CreateVM from --no-api to API mode"
```

---

## Task 15: Update StopVM to Use Graceful Shutdown

**Files:**
- Modify: `pkg/firecracker/client.go`

### Step 1: Document the change (no separate test needed for process management)

Update StopVM to try SendCtrlAltDel first via API socket if available.

### Step 2: Implement graceful shutdown

```go
func (c *Client) StopVM(ctx context.Context, namespace, vmID string) error {
	vmDir := filepath.Join(c.stateDir, namespace, vmID)

	// Try graceful shutdown via API if socket exists
	apiSocketPath := filepath.Join(vmDir, "api.sock")
	if _, err := os.Stat(apiSocketPath); err == nil {
		apiClient := NewAPIClient(apiSocketPath)
		if err := apiClient.SendCtrlAltDel(ctx); err == nil {
			// Wait briefly for graceful shutdown
			time.Sleep(2 * time.Second)
		}
	}

	// Read PID and send SIGTERM
	pidFile := filepath.Join(vmDir, "firecracker.pid")
	// ... (keep existing SIGTERM/SIGKILL logic as fallback)
}
```

### Step 3: Run tests

Run: `go test ./pkg/firecracker -v`
Expected: PASS

### Step 4: Commit

```bash
git add pkg/firecracker/client.go
git commit -m "feat(firecracker): add graceful shutdown via SendCtrlAltDel"
```

---

## Task 16: Remove Config File Generation

**Files:**
- Modify: `pkg/firecracker/config.go`
- Modify: `pkg/firecracker/client.go`

### Step 1: Clean up unused config file code

The `GenerateConfig` function that writes JSON config is no longer needed. We can either:
- Remove it entirely
- Keep it for debugging/reference

Decision: Remove to keep codebase clean.

### Step 2: Remove GenerateConfig calls from client.go

Remove any code that calls `GenerateConfig` or writes `config.json`.

### Step 3: Run tests

Run: `go test ./pkg/firecracker -v`
Expected: PASS (may need to update some tests)

### Step 4: Commit

```bash
git add pkg/firecracker/config.go pkg/firecracker/client.go
git commit -m "refactor(firecracker): remove config file generation, now using API"
```

---

## Task 17: Run All Tests

**Files:**
- All test files

### Step 1: Run full test suite

Run: `go test ./... -v`
Expected: All tests pass

### Step 2: Fix any failures

Address any test failures discovered.

### Step 3: Commit any fixes

```bash
git add -A
git commit -m "fix: address test failures from API mode refactor"
```

---

## Task 18: Manual Integration Test

**Files:**
- None (manual testing)

### Step 1: Build and run daemon

```bash
go build -o stockyardd ./cmd/stockyardd
sudo ./stockyardd
```

### Step 2: Create a VM with Tailscale

```bash
stockyard run --repo github.com/test/repo --tailscale-auth-key tskey-auth-xxx
```

### Step 3: Verify cloud-init worked

```bash
# Check VM console output for cloud-init success
# Verify Tailscale connected
# Verify hostname is set correctly
```

### Step 4: Document any issues found

Create issues or fix immediately if simple.

---

## Task 19: Final Cleanup and Documentation

**Files:**
- `docs/specs/firecracker-api-mode.md` - Mark as implemented
- `README.md` or other docs if needed

### Step 1: Update spec with implementation notes

Add a section noting the implementation is complete.

### Step 2: Commit

```bash
git add docs/
git commit -m "docs: mark firecracker API mode spec as implemented"
```

---

## Summary

This plan converts the Firecracker client from `--no-api` mode to full API mode in 19 tasks:

1. **Tasks 1-11**: Build the APIClient with all necessary methods
2. **Task 12**: Add MMDS data format helper
3. **Task 13**: Update types to support API socket path
4. **Task 14**: Main refactor of CreateVM (biggest change)
5. **Task 15**: Add graceful shutdown
6. **Task 16**: Remove old config file approach
7. **Tasks 17-19**: Testing and cleanup

Each task follows TDD where applicable and includes frequent commits.

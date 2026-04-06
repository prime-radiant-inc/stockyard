// api.go
package firecracker

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

// SetMetrics configures where Firecracker writes metrics.
// The path should be a file or FIFO that will receive NDJSON metrics.
func (a *APIClient) SetMetrics(ctx context.Context, metricsPath string) error {
	return a.put(ctx, "/metrics", map[string]string{
		"metrics_path": metricsPath,
	})
}

// SetBootSource configures the kernel and boot arguments.
func (a *APIClient) SetBootSource(ctx context.Context, kernelPath, bootArgs string) error {
	return a.put(ctx, "/boot-source", map[string]string{
		"kernel_image_path": kernelPath,
		"boot_args":         bootArgs,
	})
}

// SetDrive configures a block device.
func (a *APIClient) SetDrive(ctx context.Context, driveID, path string, isRoot, isReadOnly bool) error {
	return a.put(ctx, "/drives/"+driveID, map[string]interface{}{
		"drive_id":       driveID,
		"path_on_host":   path,
		"is_root_device": isRoot,
		"is_read_only":   isReadOnly,
	})
}

// SetNetworkInterface configures a network interface.
func (a *APIClient) SetNetworkInterface(ctx context.Context, ifaceID, guestMAC, hostDevName string) error {
	return a.put(ctx, "/network-interfaces/"+ifaceID, map[string]interface{}{
		"iface_id":      ifaceID,
		"guest_mac":     guestMAC,
		"host_dev_name": hostDevName,
	})
}

// SetMachineConfig configures vCPUs and memory.
func (a *APIClient) SetMachineConfig(ctx context.Context, vcpus, memMB int32) error {
	return a.put(ctx, "/machine-config", map[string]interface{}{
		"vcpu_count":   vcpus,
		"mem_size_mib": memMB,
		"smt":          false,
	})
}

// SetVsock configures the vsock device for host-guest communication.
// The guest_cid must be unique across all VMs (valid range: 3-4294967294).
// The uds_path is where Firecracker creates the Unix socket for host access.
// Host connects to UDS, sends "CONNECT <port>\n", reads "OK <port>\n".
func (a *APIClient) SetVsock(ctx context.Context, guestCID uint32, udsPath string) error {
	return a.put(ctx, "/vsock", map[string]interface{}{
		"guest_cid": guestCID,
		"uds_path":  udsPath,
	})
}

// SetMMDSConfig enables MMDS on the specified network interfaces.
// Uses V1 for compatibility with cloud-init's IMDS datasource (V2 requires token auth).
func (a *APIClient) SetMMDSConfig(ctx context.Context, networkInterfaces []string) error {
	return a.put(ctx, "/mmds/config", map[string]interface{}{
		"network_interfaces": networkInterfaces,
		"version":            "V1",
	})
}

// SetMMDSData sets the MMDS data that will be available to the guest.
func (a *APIClient) SetMMDSData(ctx context.Context, data map[string]interface{}) error {
	return a.put(ctx, "/mmds", data)
}

// StartInstance boots the configured VM.
func (a *APIClient) StartInstance(ctx context.Context) error {
	return a.put(ctx, "/actions", map[string]string{
		"action_type": "InstanceStart",
	})
}

// SendCtrlAltDel sends Ctrl+Alt+Del for graceful shutdown.
func (a *APIClient) SendCtrlAltDel(ctx context.Context) error {
	return a.put(ctx, "/actions", map[string]string{
		"action_type": "SendCtrlAltDel",
	})
}

// WaitForSocket waits for the API socket to become available.
func (a *APIClient) WaitForSocket(ctx context.Context) error {
	return a.WaitForSocketOrDeath(ctx, nil)
}

// errProcessDied is returned when the Firecracker process exits before the
// API socket becomes available.
var errProcessDied = fmt.Errorf("firecracker process died")

// isProcessDeath checks whether an error is due to the Firecracker process dying.
func isProcessDeath(err error) bool {
	return err == errProcessDied
}

// WaitForSocketOrDeath waits for the API socket to become available, but
// returns immediately if the Firecracker process dies (procDone closes).
// This replaces the old sleep-then-check pattern with a proper race.
func (a *APIClient) WaitForSocketOrDeath(ctx context.Context, procDone <-chan struct{}) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for socket %s: %w", a.socketPath, ctx.Err())
		case <-procDone:
			return errProcessDied
		case <-ticker.C:
			conn, err := net.Dial("unix", a.socketPath)
			if err == nil {
				conn.Close()
				return nil
			}
		}
	}
}

// GetMetrics fetches metrics from the Firecracker API.
func (a *APIClient) GetMetrics(ctx context.Context) (*FirecrackerMetrics, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/metrics", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get metrics failed: %s - %s", resp.Status, string(body))
	}

	var metrics FirecrackerMetrics
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		return nil, fmt.Errorf("decode metrics: %w", err)
	}

	return &metrics, nil
}

// MachineConfig represents the VM's machine configuration.
type MachineConfig struct {
	VCPUCount  int32 `json:"vcpu_count"`
	MemSizeMib int32 `json:"mem_size_mib"`
	SMT        bool  `json:"smt"`
}

// GetMachineConfig fetches the machine configuration from the Firecracker API.
func (a *APIClient) GetMachineConfig(ctx context.Context) (*MachineConfig, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/machine-config", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get machine-config failed: %s - %s", resp.Status, string(body))
	}

	var config MachineConfig
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, fmt.Errorf("decode machine-config: %w", err)
	}

	return &config, nil
}

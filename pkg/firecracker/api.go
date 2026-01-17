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

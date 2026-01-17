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

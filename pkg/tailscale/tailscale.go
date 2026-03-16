// Package tailscale provides Tailscale integration for VM networking
package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"

	"github.com/obra/stockyard/pkg/dashboard"
)

// BuildHostname generates a Tailscale hostname for a task
func BuildHostname(taskID string) string {
	return fmt.Sprintf("stockyard-%s", taskID)
}

// ValidateAuthKey validates a Tailscale auth key format
func ValidateAuthKey(key string) error {
	if key == "" {
		return fmt.Errorf("auth key is empty")
	}
	if !strings.HasPrefix(key, "tskey-") {
		return fmt.Errorf("invalid auth key format: should start with 'tskey-'")
	}
	return nil
}

// Manager handles Tailscale operations
type Manager struct {
	authKey string
}

// NewManager creates a new Tailscale manager
func NewManager(authKey string) (*Manager, error) {
	if err := ValidateAuthKey(authKey); err != nil {
		return nil, err
	}
	return &Manager{authKey: authKey}, nil
}


// GetAuthKey returns the auth key (for cloud-init injection)
func (m *Manager) GetAuthKey() string {
	return m.authKey
}

// Status represents Tailscale status
type Status struct {
	BackendState string
	Self         *Peer
	Peers        []Peer
}

// Peer represents a Tailscale peer
type Peer struct {
	HostName     string
	DNSName      string
	TailscaleIPs []string
	Online       bool
}

// GetStatus gets the status of Tailscale (from host perspective)
func GetStatus(ctx context.Context) (*Status, error) {
	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tailscale status: %w: %s", err, stderr.String())
	}

	// Note: Full implementation would parse the JSON
	// For now, return basic status
	return &Status{
		BackendState: "Running",
	}, nil
}

// WaitForNode waits for a node to appear in Tailscale
func WaitForNode(ctx context.Context, hostname string, timeout int) error {
	// In practice, check tailscale status --json for the node
	// Placeholder for now
	return nil
}

// RemoveNode removes a node from the Tailscale network
// Requires admin API access
func RemoveNode(ctx context.Context, hostname string) error {
	// This would use the Tailscale admin API
	// Placeholder for now
	return nil
}

// RemoveDevice attempts to remove a device from the tailnet.
// This is best-effort - if it fails, ephemeral keys will expire the device.
// Note: Proper removal would require Tailscale API access, which we don't have.
// For now, we rely on using ephemeral auth keys that auto-expire.
func RemoveDevice(ctx context.Context, hostname string) error {
	// With ephemeral keys, devices are automatically removed when they
	// disconnect and the key expires. No action needed here.
	log.Printf("Tailscale device %s will be cleaned up by ephemeral key expiration", hostname)
	return nil
}

// LocalClient provides access to the Tailscale local API.
type LocalClient struct {
	socket string
}

// Verify LocalClient implements dashboard.TailscaleClient
var _ dashboard.TailscaleClient = (*LocalClient)(nil)

// NewLocalClient creates a client for the Tailscale local API.
func NewLocalClient() *LocalClient {
	return &LocalClient{
		socket: "/var/run/tailscale/tailscaled.sock",
	}
}

// WhoIs identifies who is connecting from the given remote address.
// Implements dashboard.TailscaleClient.
func (c *LocalClient) WhoIs(ctx context.Context, remoteAddr string) (*dashboard.User, error) {
	// Create HTTP client that uses Unix socket
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", c.socket)
			},
		},
	}

	// Call the local API whois endpoint
	url := fmt.Sprintf("http://local-tailscaled.sock/localapi/v0/whois?addr=%s", remoteAddr)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("whois failed: %s", resp.Status)
	}

	// Parse response - the Tailscale API returns UserProfile info
	var result struct {
		UserProfile struct {
			LoginName     string `json:"LoginName"`
			DisplayName   string `json:"DisplayName"`
			ProfilePicURL string `json:"ProfilePicURL"`
		} `json:"UserProfile"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &dashboard.User{
		Login:         result.UserProfile.LoginName,
		Name:          result.UserProfile.DisplayName,
		ProfilePicURL: result.UserProfile.ProfilePicURL,
	}, nil
}

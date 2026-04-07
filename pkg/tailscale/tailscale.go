// Package tailscale provides Tailscale integration for VM networking
package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

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

// tailscaleStatus is the subset of `tailscale status --json` we need.
type tailscaleStatus struct {
	Peer map[string]struct {
		HostName     string   `json:"HostName"`
		Online       bool     `json:"Online"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Peer"`
}

// WaitForPeer blocks until the given hostname is reachable via Tailscale.
// It polls `tailscale status --json` for the peer, then probes SSH on the
// peer's Tailscale IP (not the MagicDNS hostname, which has propagation delay).
func WaitForPeer(ctx context.Context, hostname string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var peerIP string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				if peerIP == "" {
					return fmt.Errorf("timeout waiting for Tailscale peer %s after %v", hostname, timeout)
				}
				return fmt.Errorf("peer %s (%s) online but SSH not reachable after %v", hostname, peerIP, timeout)
			}
			if peerIP == "" {
				peerIP = getPeerIP(ctx, hostname)
				if peerIP != "" {
					log.Printf("Tailscale peer %s has IP %s, waiting for SSH...", hostname, peerIP)
				}
			} else {
				// Peer is online — probe SSH on the Tailscale IP directly
				conn, err := net.DialTimeout("tcp", peerIP+":22", 500*time.Millisecond)
				if err == nil {
					conn.Close()
					return nil
				}
			}
		}
	}
}

// getPeerIP returns the Tailscale IPv4 address for hostname, or "" if not found/online.
func getPeerIP(ctx context.Context, hostname string) string {
	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	var status tailscaleStatus
	if json.Unmarshal(output, &status) != nil {
		return ""
	}
	for _, peer := range status.Peer {
		if peer.HostName == hostname && peer.Online && len(peer.TailscaleIPs) > 0 {
			return peer.TailscaleIPs[0]
		}
	}
	return ""
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

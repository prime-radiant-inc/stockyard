// Package tailscale provides Tailscale integration for VM networking
package tailscale

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
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

// GenerateCloudInitScript generates the cloud-init script for Tailscale setup
func (m *Manager) GenerateCloudInitScript(hostname string) string {
	return fmt.Sprintf(`tailscale up --authkey=%s --hostname=%s --accept-routes --ssh`, m.authKey, hostname)
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

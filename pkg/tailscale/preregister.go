// pkg/tailscale/preregister.go
package tailscale

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PreRegistrar handles pre-registering Tailscale nodes for VMs.
type PreRegistrar struct {
	authKey  string
	stateDir string
}

// PreRegisteredNode contains the result of pre-registration.
type PreRegisteredNode struct {
	Hostname string
	State    []byte
	IP       string
}

// NewPreRegistrar creates a new pre-registrar.
func NewPreRegistrar(authKey, stateDir string) *PreRegistrar {
	return &PreRegistrar{
		authKey:  authKey,
		stateDir: stateDir,
	}
}

// PreRegister creates a new Tailscale identity and registers it.
func (p *PreRegistrar) PreRegister(ctx context.Context, hostname string) (*PreRegisteredNode, error) {
	// Create isolated state directory
	nodeDir := filepath.Join(p.stateDir, hostname)
	if err := os.MkdirAll(nodeDir, 0700); err != nil {
		return nil, fmt.Errorf("create node dir: %w", err)
	}

	statePath := filepath.Join(nodeDir, "tailscaled.state")
	socketPath := filepath.Join(nodeDir, "tailscaled.sock")

	// Start tailscaled in userspace mode with isolated state
	tailscaled := exec.CommandContext(ctx,
		"tailscaled",
		"--state="+statePath,
		"--socket="+socketPath,
		"--tun=userspace-networking",
		"--statedir="+nodeDir,
	)
	tailscaled.Env = os.Environ()

	if err := tailscaled.Start(); err != nil {
		os.RemoveAll(nodeDir)
		return nil, fmt.Errorf("start tailscaled: %w", err)
	}

	// Ensure cleanup on any exit path
	cleanup := func() {
		if tailscaled.Process != nil {
			tailscaled.Process.Kill()
			tailscaled.Wait()
		}
		os.RemoveAll(nodeDir)
	}

	// Wait for socket
	if err := waitForSocket(ctx, socketPath, 15*time.Second); err != nil {
		cleanup()
		return nil, fmt.Errorf("wait for socket: %w", err)
	}

	// Write auth key to temp file (avoids exposing in process arguments)
	authKeyPath := filepath.Join(nodeDir, "authkey")
	if err := os.WriteFile(authKeyPath, []byte(p.authKey), 0600); err != nil {
		cleanup()
		return nil, fmt.Errorf("write auth key file: %w", err)
	}

	// Run tailscale up with auth key from file
	up := exec.CommandContext(ctx,
		"tailscale",
		"--socket="+socketPath,
		"up",
		"--authkey=file:"+authKeyPath,
		"--hostname="+hostname,
		"--accept-routes",
		"--ssh",
		"--timeout=30s",
	)

	if output, err := up.CombinedOutput(); err != nil {
		cleanup()
		return nil, fmt.Errorf("tailscale up: %w: %s", err, output)
	}

	// Get assigned IP
	ipCmd := exec.CommandContext(ctx,
		"tailscale",
		"--socket="+socketPath,
		"ip", "-4",
	)
	ipOutput, err := ipCmd.Output()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("get IP: %w", err)
	}
	ip := strings.TrimSpace(string(ipOutput))

	// Read the state file before cleanup
	state, err := os.ReadFile(statePath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("read state: %w", err)
	}

	cleanup()

	return &PreRegisteredNode{
		Hostname: hostname,
		State:    state,
		IP:       ip,
	}, nil
}

// waitForSocket waits for a Unix socket to become available.
func waitForSocket(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("socket not available after %v", timeout)
}

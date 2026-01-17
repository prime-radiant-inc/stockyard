// Package firecracker provides direct Firecracker microVM management.
package firecracker

import (
	"crypto/rand"
	"fmt"
	"os/exec"
	"strings"
)

// NetworkManager handles TAP device creation and management.
type NetworkManager struct {
	bridgeName string
}

// NewNetworkManager creates a new network manager.
func NewNetworkManager(bridgeName string) *NetworkManager {
	return &NetworkManager{
		bridgeName: bridgeName,
	}
}

// CreateTap creates a TAP device and optionally attaches it to a bridge.
func (nm *NetworkManager) CreateTap(tapName string) error {
	// Create tap device using ip command
	if err := runCmd("ip", "tuntap", "add", "dev", tapName, "mode", "tap"); err != nil {
		return fmt.Errorf("failed to create tap device %s: %w", tapName, err)
	}

	// Bring it up
	if err := runCmd("ip", "link", "set", tapName, "up"); err != nil {
		// Try to clean up
		_ = runCmd("ip", "link", "delete", tapName)
		return fmt.Errorf("failed to bring up tap device %s: %w", tapName, err)
	}

	// Attach to bridge if configured
	if nm.bridgeName != "" {
		if bridgeExists(nm.bridgeName) {
			if err := runCmd("ip", "link", "set", tapName, "master", nm.bridgeName); err != nil {
				// Try to clean up
				_ = runCmd("ip", "link", "delete", tapName)
				return fmt.Errorf("failed to attach tap %s to bridge %s: %w", tapName, nm.bridgeName, err)
			}
		}
	}

	return nil
}

// DeleteTap removes a TAP device.
func (nm *NetworkManager) DeleteTap(tapName string) error {
	if !tapExists(tapName) {
		return nil // Already gone
	}
	if err := runCmd("ip", "link", "delete", tapName); err != nil {
		return fmt.Errorf("failed to delete tap device %s: %w", tapName, err)
	}
	return nil
}

// GenerateMAC creates a random MAC address with a locally administered prefix.
func GenerateMAC() string {
	buf := make([]byte, 5)
	_, _ = rand.Read(buf)
	// 02: prefix indicates locally administered unicast MAC
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", buf[0], buf[1], buf[2], buf[3], buf[4])
}

// TapNameForVM generates a consistent TAP device name for a VM.
func TapNameForVM(vmID string) string {
	// Truncate to fit Linux interface name limit (15 chars)
	if len(vmID) > 8 {
		vmID = vmID[:8]
	}
	return "tap-" + vmID
}

// bridgeExists checks if a bridge interface exists.
func bridgeExists(name string) bool {
	err := runCmd("ip", "link", "show", name)
	return err == nil
}

// tapExists checks if a TAP interface exists.
func tapExists(name string) bool {
	err := runCmd("ip", "link", "show", name)
	return err == nil
}

// runCmd executes a command and returns any error.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, string(output))
	}
	return nil
}

package flintlock

import (
	"fmt"
	"net"
	"os/exec"
)

// NetworkManager handles TAP device and network configuration.
type NetworkManager struct {
	bridgeName string
	subnet     *net.IPNet
	nextIP     net.IP
}

// NewNetworkManager creates a network manager.
func NewNetworkManager(bridgeName string, subnet string) (*NetworkManager, error) {
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet: %w", err)
	}

	// Start from .2 (assuming .1 is the gateway)
	nextIP := make(net.IP, len(ipnet.IP))
	copy(nextIP, ipnet.IP)
	nextIP[3] = 2

	return &NetworkManager{
		bridgeName: bridgeName,
		subnet:     ipnet,
		nextIP:     nextIP,
	}, nil
}

// AllocateIP allocates the next available IP.
func (nm *NetworkManager) AllocateIP() (string, error) {
	ip := nm.nextIP.String()

	// Increment for next allocation
	nm.nextIP[3]++
	if nm.nextIP[3] == 0 {
		nm.nextIP[2]++
	}

	return ip, nil
}

// CreateTAPDevice creates a TAP device for a VM.
func (nm *NetworkManager) CreateTAPDevice(name string) error {
	cmd := exec.Command("ip", "tuntap", "add", "dev", name, "mode", "tap")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create TAP device: %w", err)
	}

	cmd = exec.Command("ip", "link", "set", "dev", name, "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to bring up TAP device: %w", err)
	}

	// Add to bridge
	cmd = exec.Command("ip", "link", "set", "dev", name, "master", nm.bridgeName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to add TAP to bridge: %w", err)
	}

	return nil
}

// DeleteTAPDevice removes a TAP device.
func (nm *NetworkManager) DeleteTAPDevice(name string) error {
	cmd := exec.Command("ip", "link", "delete", name)
	return cmd.Run()
}

// SetupBridge creates the bridge if it doesn't exist.
func (nm *NetworkManager) SetupBridge() error {
	// Check if bridge exists
	cmd := exec.Command("ip", "link", "show", nm.bridgeName)
	if cmd.Run() == nil {
		return nil // Bridge exists
	}

	// Create bridge
	cmd = exec.Command("ip", "link", "add", "name", nm.bridgeName, "type", "bridge")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create bridge: %w", err)
	}

	// Assign IP to bridge (gateway)
	gatewayIP := nm.subnet.IP.String()
	gatewayIP = gatewayIP[:len(gatewayIP)-1] + "1" // .1 for gateway
	maskSize, _ := nm.subnet.Mask.Size()
	cmd = exec.Command("ip", "addr", "add", fmt.Sprintf("%s/%d", gatewayIP, maskSize), "dev", nm.bridgeName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to assign IP to bridge: %w", err)
	}

	// Bring up bridge
	cmd = exec.Command("ip", "link", "set", "dev", nm.bridgeName, "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to bring up bridge: %w", err)
	}

	// Enable IP forwarding
	cmd = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1")
	cmd.Run()

	// Setup NAT
	cmd = exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", nm.subnet.String(), "-j", "MASQUERADE")
	cmd.Run()

	return nil
}

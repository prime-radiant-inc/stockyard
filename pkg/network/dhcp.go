// Package network provides network management for Stockyard VMs.
package network

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
)

// DHCPConfig holds configuration for the DHCP server.
type DHCPConfig struct {
	Bridge     string // Network bridge interface (e.g., "flbr0")
	Gateway    string // Gateway IP address (e.g., "192.168.64.1")
	RangeStart string // Start of DHCP range (e.g., "192.168.64.2")
	RangeEnd   string // End of DHCP range (e.g., "192.168.127.254")
	Netmask    string // Network mask (e.g., "255.255.192.0")
	LeaseTime  string // DHCP lease duration (e.g., "12h")
	DNS        string // DNS server (e.g., "8.8.8.8"), optional
}

// DHCPServer manages a dnsmasq instance for DHCP.
type DHCPServer struct {
	config     DHCPConfig
	configPath string
	leasePath  string
	logPath    string
	dataDir    string
	cmd        *exec.Cmd
	mu         sync.Mutex
}

// NewDHCPServer creates a new DHCP server with the given configuration.
// It validates required fields and sets up file paths for dnsmasq.
func NewDHCPServer(config DHCPConfig, dataDir string) (*DHCPServer, error) {
	if err := validateDHCPConfig(config); err != nil {
		return nil, err
	}

	return &DHCPServer{
		config:     config,
		configPath: filepath.Join(dataDir, "dnsmasq.conf"),
		leasePath:  filepath.Join(dataDir, "dnsmasq.leases"),
		logPath:    filepath.Join(dataDir, "dnsmasq.log"),
		dataDir:    dataDir,
	}, nil
}

// validateDHCPConfig checks that all required fields are present.
func validateDHCPConfig(config DHCPConfig) error {
	if config.Bridge == "" {
		return fmt.Errorf("dhcp: bridge is required")
	}
	if config.Gateway == "" {
		return fmt.Errorf("dhcp: gateway is required")
	}
	if config.RangeStart == "" {
		return fmt.Errorf("dhcp: range start is required")
	}
	if config.RangeEnd == "" {
		return fmt.Errorf("dhcp: range end is required")
	}
	if config.Netmask == "" {
		return fmt.Errorf("dhcp: netmask is required")
	}
	return nil
}

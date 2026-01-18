package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewDHCPServer(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if srv.configPath != filepath.Join(dataDir, "dnsmasq.conf") {
		t.Errorf("unexpected configPath: %s", srv.configPath)
	}
	if srv.leasePath != filepath.Join(dataDir, "dnsmasq.leases") {
		t.Errorf("unexpected leasePath: %s", srv.leasePath)
	}
}

func TestNewDHCPServer_ValidationErrors(t *testing.T) {
	dataDir := t.TempDir()

	tests := []struct {
		name   string
		config DHCPConfig
	}{
		{"missing bridge", DHCPConfig{Gateway: "192.168.64.1", RangeStart: "192.168.64.2", RangeEnd: "192.168.127.254", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing gateway", DHCPConfig{Bridge: "flbr0", RangeStart: "192.168.64.2", RangeEnd: "192.168.127.254", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing range start", DHCPConfig{Bridge: "flbr0", Gateway: "192.168.64.1", RangeEnd: "192.168.127.254", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing range end", DHCPConfig{Bridge: "flbr0", Gateway: "192.168.64.1", RangeStart: "192.168.64.2", Netmask: "255.255.192.0", LeaseTime: "12h"}},
		{"missing netmask", DHCPConfig{Bridge: "flbr0", Gateway: "192.168.64.1", RangeStart: "192.168.64.2", RangeEnd: "192.168.127.254", LeaseTime: "12h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewDHCPServer(tt.config, dataDir)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestDHCPServer_WriteConfig(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "192.168.64.1",
		RangeStart: "192.168.64.2",
		RangeEnd:   "192.168.127.254",
		Netmask:    "255.255.192.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := srv.WriteConfig(); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}

	// Read and verify config
	data, err := os.ReadFile(srv.configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	config := string(data)
	expectedLines := []string{
		"interface=flbr0",
		"bind-interfaces",
		"dhcp-range=192.168.64.2,192.168.127.254,255.255.192.0,12h",
		"dhcp-option=option:router,192.168.64.1",
		"dhcp-option=option:dns-server,8.8.8.8",
		"dhcp-authoritative",
	}

	for _, line := range expectedLines {
		if !strings.Contains(config, line) {
			t.Errorf("config missing expected line: %s", line)
		}
	}
}

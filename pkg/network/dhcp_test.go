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
		Gateway:    "10.0.100.1",
		RangeStart: "10.0.100.2",
		RangeEnd:   "10.0.100.254",
		Netmask:    "255.255.255.0",
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
		{"missing bridge", DHCPConfig{Gateway: "10.0.100.1", RangeStart: "10.0.100.2", RangeEnd: "10.0.100.254", Netmask: "255.255.255.0", LeaseTime: "12h"}},
		{"missing gateway", DHCPConfig{Bridge: "flbr0", RangeStart: "10.0.100.2", RangeEnd: "10.0.100.254", Netmask: "255.255.255.0", LeaseTime: "12h"}},
		{"missing range start", DHCPConfig{Bridge: "flbr0", Gateway: "10.0.100.1", RangeEnd: "10.0.100.254", Netmask: "255.255.255.0", LeaseTime: "12h"}},
		{"missing range end", DHCPConfig{Bridge: "flbr0", Gateway: "10.0.100.1", RangeStart: "10.0.100.2", Netmask: "255.255.255.0", LeaseTime: "12h"}},
		{"missing netmask", DHCPConfig{Bridge: "flbr0", Gateway: "10.0.100.1", RangeStart: "10.0.100.2", RangeEnd: "10.0.100.254", LeaseTime: "12h"}},
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
		Gateway:    "10.0.100.1",
		RangeStart: "10.0.100.2",
		RangeEnd:   "10.0.100.254",
		Netmask:    "255.255.255.0",
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
		"dhcp-range=10.0.100.2,10.0.100.254,255.255.255.0,12h",
		"dhcp-option=option:router,10.0.100.1",
		"dhcp-option=option:dns-server,8.8.8.8",
		"dhcp-authoritative",
	}

	for _, line := range expectedLines {
		if !strings.Contains(config, line) {
			t.Errorf("config missing expected line: %s", line)
		}
	}
}

func TestDHCPServer_StartStop_NoBinary(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "10.0.100.1",
		RangeStart: "10.0.100.2",
		RangeEnd:   "10.0.100.254",
		Netmask:    "255.255.255.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Use a non-existent binary path
	srv.SetBinaryPath("/nonexistent/dnsmasq")

	err = srv.Start()
	if err == nil {
		t.Error("expected error for missing binary")
	}
}

func TestDHCPServer_IsRunning(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "10.0.100.1",
		RangeStart: "10.0.100.2",
		RangeEnd:   "10.0.100.254",
		Netmask:    "255.255.255.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if srv.IsRunning() {
		t.Error("expected not running before start")
	}
}

func TestDHCPServer_GetIPForMAC(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "10.0.100.1",
		RangeStart: "10.0.100.2",
		RangeEnd:   "10.0.100.254",
		Netmask:    "255.255.255.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Create a fake lease file
	// Format: <expiry> <MAC> <IP> <hostname> <client-id>
	leaseContent := `1737200000 02:7a:77:e8:87:9e 10.0.100.2 stockyard-abc123 *
1737200000 02:8d:3f:70:39:a9 10.0.100.3 stockyard-def456 *
`
	if err := os.WriteFile(srv.leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatalf("failed to write lease file: %v", err)
	}

	tests := []struct {
		mac      string
		wantIP   string
		wantFind bool
	}{
		{"02:7a:77:e8:87:9e", "10.0.100.2", true},
		{"02:8d:3f:70:39:a9", "10.0.100.3", true},
		{"02:00:00:00:00:00", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.mac, func(t *testing.T) {
			ip, found := srv.GetIPForMAC(tt.mac)
			if found != tt.wantFind {
				t.Errorf("GetIPForMAC(%s) found = %v, want %v", tt.mac, found, tt.wantFind)
			}
			if ip != tt.wantIP {
				t.Errorf("GetIPForMAC(%s) = %s, want %s", tt.mac, ip, tt.wantIP)
			}
		})
	}
}

func TestDHCPServer_GetIPForMAC_CaseInsensitive(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "10.0.100.1",
		RangeStart: "10.0.100.2",
		RangeEnd:   "10.0.100.254",
		Netmask:    "255.255.255.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	leaseContent := `1737200000 02:7a:77:e8:87:9e 10.0.100.2 vm1 *
`
	if err := os.WriteFile(srv.leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatalf("failed to write lease file: %v", err)
	}

	// Test uppercase lookup
	ip, found := srv.GetIPForMAC("02:7A:77:E8:87:9E")
	if !found {
		t.Error("expected to find MAC with uppercase")
	}
	if ip != "10.0.100.2" {
		t.Errorf("got IP %s, want 10.0.100.2", ip)
	}
}

func TestDHCPServer_ListLeases(t *testing.T) {
	dataDir := t.TempDir()

	srv, err := NewDHCPServer(DHCPConfig{
		Bridge:     "flbr0",
		Gateway:    "10.0.100.1",
		RangeStart: "10.0.100.2",
		RangeEnd:   "10.0.100.254",
		Netmask:    "255.255.255.0",
		LeaseTime:  "12h",
		DNS:        "8.8.8.8",
	}, dataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	leaseContent := `1737200000 02:7a:77:e8:87:9e 10.0.100.2 stockyard-abc123 *
1737200000 02:8d:3f:70:39:a9 10.0.100.3 stockyard-def456 *
`
	if err := os.WriteFile(srv.leasePath, []byte(leaseContent), 0644); err != nil {
		t.Fatalf("failed to write lease file: %v", err)
	}

	leases, err := srv.ListLeases()
	if err != nil {
		t.Fatalf("ListLeases failed: %v", err)
	}

	if len(leases) != 2 {
		t.Fatalf("expected 2 leases, got %d", len(leases))
	}

	if leases[0].MAC != "02:7a:77:e8:87:9e" {
		t.Errorf("unexpected MAC: %s", leases[0].MAC)
	}
	if leases[0].IP != "10.0.100.2" {
		t.Errorf("unexpected IP: %s", leases[0].IP)
	}
	if leases[0].Hostname != "stockyard-abc123" {
		t.Errorf("unexpected hostname: %s", leases[0].Hostname)
	}
}

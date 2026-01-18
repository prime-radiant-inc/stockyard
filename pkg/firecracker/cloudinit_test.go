// Package firecracker provides direct Firecracker microVM management.
package firecracker

import (
	"strings"
	"testing"
)

func TestBuildMMDSData(t *testing.T) {
	cloudInitYAML := "#cloud-config\nhostname: test-vm\n"

	data := BuildMMDSData(MMDSMetadata{
		InstanceID: "i-abc123",
		Hostname:   "stockyard-abc123",
		UserData:   cloudInitYAML,
	})

	latest, ok := data["latest"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'latest' key")
	}

	metadata, ok := latest["meta-data"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'meta-data' key")
	}

	if metadata["instance-id"] != "i-abc123" {
		t.Errorf("wrong instance-id: %v", metadata["instance-id"])
	}
	if metadata["local-hostname"] != "stockyard-abc123" {
		t.Errorf("wrong local-hostname: %v", metadata["local-hostname"])
	}

	userData, ok := latest["user-data"].(string)
	if !ok {
		t.Fatal("missing 'user-data' key")
	}
	if !strings.HasPrefix(userData, "#cloud-config") {
		t.Errorf("user-data should start with #cloud-config: %s", userData)
	}
}

func TestBuildMMDSDataWithNetworkConfig(t *testing.T) {
	metadata := MMDSMetadata{
		InstanceID: "i-test",
		Hostname:   "test-vm",
		NetworkConfig: &MMDSNetworkConfig{
			IP:      "10.0.100.50",
			Netmask: "255.255.255.0",
			Gateway: "10.0.100.1",
			DNS:     "8.8.8.8",
		},
	}

	data := BuildMMDSData(metadata)

	// Verify network-config is present
	latest, ok := data["latest"].(map[string]interface{})
	if !ok {
		t.Fatal("expected latest in MMDS")
	}
	metaData, ok := latest["meta-data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected meta-data in MMDS")
	}

	netCfg, ok := metaData["network-config"].(map[string]interface{})
	if !ok {
		t.Fatal("expected network-config in meta-data")
	}

	if netCfg["ip"] != "10.0.100.50" {
		t.Errorf("expected IP 10.0.100.50, got %v", netCfg["ip"])
	}
	if netCfg["gateway"] != "10.0.100.1" {
		t.Errorf("expected gateway 10.0.100.1, got %v", netCfg["gateway"])
	}
}

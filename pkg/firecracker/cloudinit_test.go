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

	metadata, ok := latest["meta-data"].(map[string]string)
	if !ok {
		t.Fatal("missing 'meta-data' key")
	}

	if metadata["instance-id"] != "i-abc123" {
		t.Errorf("wrong instance-id: %s", metadata["instance-id"])
	}
	if metadata["local-hostname"] != "stockyard-abc123" {
		t.Errorf("wrong local-hostname: %s", metadata["local-hostname"])
	}

	userData, ok := latest["user-data"].(string)
	if !ok {
		t.Fatal("missing 'user-data' key")
	}
	if !strings.HasPrefix(userData, "#cloud-config") {
		t.Errorf("user-data should start with #cloud-config: %s", userData)
	}
}

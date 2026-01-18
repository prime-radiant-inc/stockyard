package firecracker

import (
	"testing"
)

func TestParseFirecrackerMetrics(t *testing.T) {
	// Sample Firecracker metrics JSON
	metricsJSON := `{
		"utc_timestamp_ms": 1234567890,
		"vcpu": {
			"exit_io_in": 100,
			"exit_io_out": 50
		},
		"net": {
			"rx_bytes": 1024,
			"tx_bytes": 512
		}
	}`

	metrics, err := ParseMetrics([]byte(metricsJSON))
	if err != nil {
		t.Fatalf("failed to parse metrics: %v", err)
	}

	if metrics.Net.RxBytes != 1024 {
		t.Errorf("expected rx_bytes 1024, got %d", metrics.Net.RxBytes)
	}
}

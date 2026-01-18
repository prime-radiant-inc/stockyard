package firecracker

import (
	"encoding/json"
)

// FirecrackerMetrics represents the metrics returned by Firecracker.
type FirecrackerMetrics struct {
	UTCTimestampMs int64 `json:"utc_timestamp_ms"`
	VCPU           struct {
		ExitIOIn  int64 `json:"exit_io_in"`
		ExitIOOut int64 `json:"exit_io_out"`
	} `json:"vcpu"`
	Net struct {
		RxBytes  int64 `json:"rx_bytes"`
		TxBytes  int64 `json:"tx_bytes"`
		RxFrames int64 `json:"rx_frames"`
		TxFrames int64 `json:"tx_frames"`
	} `json:"net"`
	Block struct {
		ReadBytes  int64 `json:"read_bytes"`
		WriteBytes int64 `json:"write_bytes"`
	} `json:"block"`
}

// ParseMetrics parses Firecracker metrics JSON.
func ParseMetrics(data []byte) (*FirecrackerMetrics, error) {
	var metrics FirecrackerMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, err
	}
	return &metrics, nil
}

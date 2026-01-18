package dashboard

import (
	"encoding/json"
	"fmt"
	"time"
)

// VMMetrics represents resource metrics for a VM.
type VMMetrics struct {
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryBytes    int64   `json:"memory_bytes"`
	MemoryMaxBytes int64   `json:"memory_max_bytes"`
	NetworkRxBytes int64   `json:"network_rx_bytes"`
	NetworkTxBytes int64   `json:"network_tx_bytes"`
	DiskUsedBytes  int64   `json:"disk_used_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
}

// MetricsMessage is the WebSocket message for metrics updates.
type MetricsMessage struct {
	Type      string    `json:"type"` // "metrics"
	TaskID    string    `json:"task_id"`
	Metrics   VMMetrics `json:"metrics"`
	Timestamp time.Time `json:"timestamp"`
}

// AlertsMessage is the WebSocket message for alert updates.
type AlertsMessage struct {
	Type   string  `json:"type"` // "alerts"
	TaskID string  `json:"task_id"`
	Alerts []Alert `json:"alerts"`
}

// MetricsCollector manages metrics collection and broadcasting.
type MetricsCollector struct {
	hub          *Hub
	alertChecker *AlertChecker
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector(hub *Hub, alertChecker *AlertChecker) *MetricsCollector {
	return &MetricsCollector{hub: hub, alertChecker: alertChecker}
}

// SendMetrics broadcasts metrics to subscribed clients.
func (m *MetricsCollector) SendMetrics(taskID string, metrics VMMetrics) {
	msg := MetricsMessage{
		Type:      "metrics",
		TaskID:    taskID,
		Metrics:   metrics,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.hub.Broadcast(taskID, data)

	// Check for alerts and broadcast them if alertChecker is configured
	if m.alertChecker != nil {
		alerts := m.alertChecker.Check(taskID, metrics)
		m.sendAlerts(taskID, alerts)
	}
}

// sendAlerts broadcasts alerts to subscribed clients.
func (m *MetricsCollector) sendAlerts(taskID string, alerts []Alert) {
	msg := AlertsMessage{
		Type:   "alerts",
		TaskID: taskID,
		Alerts: alerts,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.hub.Broadcast(taskID, data)
}

// HostMetrics represents host system metrics.
type HostMetrics struct {
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryUsedBytes  int64   `json:"memory_used_bytes"`
	MemoryTotalBytes int64   `json:"memory_total_bytes"`
	NetworkRxBytes   int64   `json:"network_rx_bytes"`
	NetworkTxBytes   int64   `json:"network_tx_bytes"`
	DiskReadBytes    int64   `json:"disk_read_bytes"`
	DiskWriteBytes   int64   `json:"disk_write_bytes"`
}

// HostMetricsMessage is the WebSocket message for host metrics.
type HostMetricsMessage struct {
	Type      string      `json:"type"` // "host_metrics"
	Metrics   HostMetrics `json:"metrics"`
	Timestamp time.Time   `json:"timestamp"`
}

// SendHostMetrics broadcasts host metrics to all clients.
func (m *MetricsCollector) SendHostMetrics(metrics HostMetrics) {
	msg := HostMetricsMessage{
		Type:      "host_metrics",
		Metrics:   metrics,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	m.hub.BroadcastAll(data)
}

// FormatBytes formats bytes as human-readable string.
func FormatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

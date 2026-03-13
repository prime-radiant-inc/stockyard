package daemon

import (
	"testing"
	"time"
)

func TestHostMetricsCollector_CollectsMetrics(t *testing.T) {
	collector := NewHostMetricsCollector()
	metrics, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// CPU should be between 0-100 per core
	if metrics.CPUPercent < 0 {
		t.Errorf("CPU percent should be >= 0, got %f", metrics.CPUPercent)
	}

	// Memory should be positive
	if metrics.MemoryUsedBytes <= 0 {
		t.Errorf("memory used should be > 0, got %d", metrics.MemoryUsedBytes)
	}
	if metrics.MemoryTotalBytes <= 0 {
		t.Errorf("memory total should be > 0, got %d", metrics.MemoryTotalBytes)
	}
}

func TestHostMetricsCollector_CPUDeltaCalculation(t *testing.T) {
	collector := NewHostMetricsCollector()

	// First call establishes baseline
	metrics1, err := collector.Collect()
	if err != nil {
		t.Fatalf("first Collect failed: %v", err)
	}
	// First call should return 0% as there's no previous measurement
	if metrics1.CPUPercent != 0 {
		t.Errorf("first CPU reading should be 0, got %f", metrics1.CPUPercent)
	}

	// Wait a bit for CPU activity
	time.Sleep(100 * time.Millisecond)

	// Second call should calculate delta
	metrics2, err := collector.Collect()
	if err != nil {
		t.Fatalf("second Collect failed: %v", err)
	}

	// CPU should be valid percentage
	if metrics2.CPUPercent < 0 || metrics2.CPUPercent > 100 {
		t.Errorf("CPU percent should be 0-100, got %f", metrics2.CPUPercent)
	}
}

func TestHostMetricsCollector_NetworkBytes(t *testing.T) {
	collector := NewHostMetricsCollector()
	metrics, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Network bytes should be non-negative (could be 0 if no network interfaces)
	if metrics.NetworkRxBytes < 0 {
		t.Errorf("network RX bytes should be >= 0, got %d", metrics.NetworkRxBytes)
	}
	if metrics.NetworkTxBytes < 0 {
		t.Errorf("network TX bytes should be >= 0, got %d", metrics.NetworkTxBytes)
	}
}

func TestHostMetricsCollector_DiskIOBytes(t *testing.T) {
	collector := NewHostMetricsCollector()
	metrics, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Disk I/O bytes should be non-negative
	if metrics.DiskReadBytes < 0 {
		t.Errorf("disk read bytes should be >= 0, got %d", metrics.DiskReadBytes)
	}
	if metrics.DiskWriteBytes < 0 {
		t.Errorf("disk write bytes should be >= 0, got %d", metrics.DiskWriteBytes)
	}
}

func TestHostMetricsCollector_MemoryUsedLessThanTotal(t *testing.T) {
	collector := NewHostMetricsCollector()
	metrics, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if metrics.MemoryUsedBytes > metrics.MemoryTotalBytes {
		t.Errorf("memory used (%d) should be <= total (%d)",
			metrics.MemoryUsedBytes, metrics.MemoryTotalBytes)
	}
}

func TestIsPhysicalDisk(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"sda", true},
		{"sdb", true},
		{"sda1", false},
		{"sda12", false},
		{"nvme0n1", true},
		{"nvme0n1p1", false},
		{"nvme1n1", true},
		{"vda", true},
		{"vda1", false},
		{"xvda", true},
		{"xvda1", false},
		{"loop0", false},
		{"loop1", false},
		{"dm-0", false},
		{"dm-1", false},
		{"ram0", false},
		{"zram0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPhysicalDisk(tt.name)
			if got != tt.expected {
				t.Errorf("isPhysicalDisk(%q) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

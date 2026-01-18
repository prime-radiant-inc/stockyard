package dashboard

import (
	"testing"
)

func TestAlertChecker_DetectsHighCPU(t *testing.T) {
	checker := NewAlertChecker()

	alerts := checker.Check("task-1", VMMetrics{
		CPUPercent:     95.0,
		MemoryBytes:    1024 * 1024 * 1024,
		MemoryMaxBytes: 4 * 1024 * 1024 * 1024,
	})

	if len(alerts) == 0 {
		t.Error("expected high CPU alert")
	}

	found := false
	for _, a := range alerts {
		if a.Type == "high_cpu" {
			found = true
		}
	}
	if !found {
		t.Error("expected high_cpu alert type")
	}
}

func TestAlertChecker_DetectsHighMemory(t *testing.T) {
	checker := NewAlertChecker()

	alerts := checker.Check("task-1", VMMetrics{
		CPUPercent:     50.0,
		MemoryBytes:    3900 * 1024 * 1024,     // ~3.9GB
		MemoryMaxBytes: 4 * 1024 * 1024 * 1024, // 4GB (97.5% usage)
	})

	found := false
	for _, a := range alerts {
		if a.Type == "high_memory" {
			found = true
		}
	}
	if !found {
		t.Error("expected high_memory alert type")
	}
}

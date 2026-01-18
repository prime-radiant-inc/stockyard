package dashboard

import (
	"sync"
	"time"
)

// Alert represents a problem condition.
type Alert struct {
	Type      string    `json:"type"`      // high_cpu, high_memory, unresponsive
	TaskID    string    `json:"task_id"`
	Severity  string    `json:"severity"` // warning, critical
	Message   string    `json:"message"`
	Value     float64   `json:"value"`
	Threshold float64   `json:"threshold"`
	Since     time.Time `json:"since"`
}

// AlertChecker evaluates metrics and detects problems.
type AlertChecker struct {
	cpuThreshold     float64
	memoryThreshold  float64
	unresponsiveTime time.Duration
	activeAlerts     map[string][]Alert
	lastMetricsTime  map[string]time.Time
	mu               sync.RWMutex
}

// NewAlertChecker creates a new alert checker with default thresholds.
func NewAlertChecker() *AlertChecker {
	return &AlertChecker{
		cpuThreshold:     90.0,              // 90% CPU
		memoryThreshold:  95.0,              // 95% memory
		unresponsiveTime: 60 * time.Second,  // 60s without metrics
		activeAlerts:     make(map[string][]Alert),
		lastMetricsTime:  make(map[string]time.Time),
	}
}

// Check evaluates metrics and returns any active alerts.
func (ac *AlertChecker) Check(taskID string, metrics VMMetrics) []Alert {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	var alerts []Alert
	now := time.Now()

	// Record when metrics were received for unresponsive detection
	ac.lastMetricsTime[taskID] = now

	// Check CPU - trigger immediately for simplicity (duration check can be added later)
	if metrics.CPUPercent >= ac.cpuThreshold {
		alerts = append(alerts, Alert{
			Type:      "high_cpu",
			TaskID:    taskID,
			Severity:  "warning",
			Message:   "CPU usage above 90%",
			Value:     metrics.CPUPercent,
			Threshold: ac.cpuThreshold,
			Since:     now,
		})
	}

	// Check memory
	if metrics.MemoryMaxBytes > 0 {
		memPercent := float64(metrics.MemoryBytes) / float64(metrics.MemoryMaxBytes) * 100
		if memPercent >= ac.memoryThreshold {
			alerts = append(alerts, Alert{
				Type:      "high_memory",
				TaskID:    taskID,
				Severity:  "critical",
				Message:   "Memory usage above 95%",
				Value:     memPercent,
				Threshold: ac.memoryThreshold,
				Since:     now,
			})
		}
	}

	ac.activeAlerts[taskID] = alerts
	return alerts
}

// GetActiveAlerts returns all currently active alerts.
func (ac *AlertChecker) GetActiveAlerts() []Alert {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	var all []Alert
	for _, alerts := range ac.activeAlerts {
		all = append(all, alerts...)
	}
	return all
}

// GetAlertCount returns the total number of active alerts.
func (ac *AlertChecker) GetAlertCount() int {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	count := 0
	for _, alerts := range ac.activeAlerts {
		count += len(alerts)
	}
	return count
}

// ClearAlerts removes alerts for a task.
func (ac *AlertChecker) ClearAlerts(taskID string) {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	delete(ac.activeAlerts, taskID)
}

// CheckUnresponsive checks if a VM has stopped reporting metrics.
func (ac *AlertChecker) CheckUnresponsive(taskID string) []Alert {
	ac.mu.RLock()
	defer ac.mu.RUnlock()

	var alerts []Alert
	now := time.Now()

	if lastTime, ok := ac.lastMetricsTime[taskID]; ok {
		if now.Sub(lastTime) > ac.unresponsiveTime {
			alerts = append(alerts, Alert{
				Type:     "unresponsive",
				TaskID:   taskID,
				Severity: "critical",
				Message:  "VM has not reported metrics recently",
				Since:    lastTime,
			})
		}
	}
	return alerts
}

package daemon

import (
	"sync"
	"time"

	"github.com/obra/stockyard/pkg/dashboard"
	"github.com/obra/stockyard/pkg/firecracker"
)

// MetricsSink receives metrics updates.
type MetricsSink interface {
	SendMetrics(taskID string, metrics dashboard.VMMetrics)
}

// vmMetricsState tracks previous metrics for delta calculations.
type vmMetricsState struct {
	lastVCPUExits int64     // Total vcpu exits (in + out)
	lastTimestamp time.Time // When we last collected
	memoryBytes   int64     // Configured memory (cached)
}

// MetricsPoller manages per-VM FIFO readers for collecting metrics.
type MetricsPoller struct {
	daemon  *Daemon
	sink    MetricsSink
	readers map[string]*MetricsFIFOReader // taskID -> reader
	state   map[string]*vmMetricsState    // taskID -> state
	mu      sync.Mutex
	running bool
	stop    chan struct{}
}

// NewMetricsPoller creates a new metrics poller.
func NewMetricsPoller(daemon *Daemon, sink MetricsSink, interval time.Duration) *MetricsPoller {
	return &MetricsPoller{
		daemon:  daemon,
		sink:    sink,
		readers: make(map[string]*MetricsFIFOReader),
		state:   make(map[string]*vmMetricsState),
		stop:    make(chan struct{}),
	}
}

// Start marks the poller as running (FIFO readers start per-task).
func (p *MetricsPoller) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return
	}
	p.running = true
	p.stop = make(chan struct{})
}

// Stop stops all FIFO readers and marks the poller as not running.
func (p *MetricsPoller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	// Stop all active readers
	for taskID, reader := range p.readers {
		reader.Stop()
		delete(p.readers, taskID)
		delete(p.state, taskID)
	}

	close(p.stop)
	p.running = false
}

// Running returns whether the poller is active.
func (p *MetricsPoller) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// StartTaskMetrics begins reading metrics for a task via its FIFO.
func (p *MetricsPoller) StartTaskMetrics(taskID, metricsPath string, memoryBytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	if _, exists := p.readers[taskID]; exists {
		return
	}

	state := &vmMetricsState{
		memoryBytes: memoryBytes,
	}
	p.state[taskID] = state

	reader := NewMetricsFIFOReader(metricsPath, func(m *firecracker.FirecrackerMetrics) {
		p.handleMetrics(taskID, state, m)
	})
	p.readers[taskID] = reader
	reader.Start()
}

// StopTaskMetrics stops reading metrics for a task.
func (p *MetricsPoller) StopTaskMetrics(taskID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if reader, exists := p.readers[taskID]; exists {
		if reader != nil {
			reader.Stop()
		}
		delete(p.readers, taskID)
		delete(p.state, taskID)
	}
}

// handleMetrics processes metrics from a FIFO and sends them to the sink.
func (p *MetricsPoller) handleMetrics(taskID string, state *vmMetricsState, m *firecracker.FirecrackerMetrics) {
	now := time.Now()

	// Calculate CPU usage from vcpu exit rate
	currentVCPUExits := m.VCPU.ExitIOIn + m.VCPU.ExitIOOut
	var cpuPercent float64
	if !state.lastTimestamp.IsZero() {
		elapsedSec := now.Sub(state.lastTimestamp).Seconds()
		if elapsedSec > 0 {
			exitDelta := currentVCPUExits - state.lastVCPUExits
			// Estimate CPU activity: more exits = more activity
			// Scale factor: assume ~10000 exits/sec = 100%
			exitsPerSec := float64(exitDelta) / elapsedSec
			cpuPercent = (exitsPerSec / 10000.0) * 100.0
			// Clamp to 0-100
			if cpuPercent < 0 {
				cpuPercent = 0
			}
			if cpuPercent > 100 {
				cpuPercent = 100
			}
		}
	}

	state.lastVCPUExits = currentVCPUExits
	state.lastTimestamp = now

	metrics := dashboard.VMMetrics{
		CPUPercent:     cpuPercent,
		MemoryBytes:    state.memoryBytes,
		MemoryMaxBytes: state.memoryBytes,
		NetworkRxBytes: m.Net.RxBytes,
		NetworkTxBytes: m.Net.TxBytes,
		DiskUsedBytes:  m.Block.ReadBytes + m.Block.WriteBytes,
		DiskTotalBytes: 0,
	}

	p.sink.SendMetrics(taskID, metrics)
}

// CleanupTask removes state for a stopped task (alias for StopTaskMetrics).
func (p *MetricsPoller) CleanupTask(taskID string) {
	p.StopTaskMetrics(taskID)
}

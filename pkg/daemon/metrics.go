package daemon

import (
	"context"
	"os"
	"path/filepath"
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
	vcpuCount     int32     // Configured vcpus (cached)
}

// MetricsPoller periodically collects metrics from running VMs.
type MetricsPoller struct {
	daemon    *Daemon
	sink      MetricsSink
	interval  time.Duration
	running   bool
	stop      chan struct{}
	mu        sync.RWMutex
	vmState   map[string]*vmMetricsState // taskID -> state
	vmStateMu sync.Mutex
}

// NewMetricsPoller creates a new metrics poller.
func NewMetricsPoller(daemon *Daemon, sink MetricsSink, interval time.Duration) *MetricsPoller {
	return &MetricsPoller{
		daemon:   daemon,
		sink:     sink,
		interval: interval,
		stop:     make(chan struct{}),
		vmState:  make(map[string]*vmMetricsState),
	}
}

// Start begins polling for metrics.
func (p *MetricsPoller) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.stop = make(chan struct{})
	p.mu.Unlock()

	go p.run()
}

// Stop stops the metrics poller.
func (p *MetricsPoller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}

	close(p.stop)
	p.running = false
}

// Running returns whether the poller is active.
func (p *MetricsPoller) Running() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

func (p *MetricsPoller) run() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.collectMetrics()
		case <-p.stop:
			return
		}
	}
}

func (p *MetricsPoller) collectMetrics() {
	if p.daemon == nil || p.sink == nil {
		return
	}

	ctx := context.Background()
	tasks, err := p.daemon.state.ListTasks("")
	if err != nil {
		return
	}

	now := time.Now()

	for _, task := range tasks {
		if task.Status != "running" || task.VMID == "" {
			continue
		}

		// Get VM directory and API socket path
		vmDir := filepath.Join(p.daemon.cfg.ZFS.VMsPath, task.VMID)
		apiSocketPath := filepath.Join(vmDir, "api.sock")

		// Check if socket exists
		if _, err := os.Stat(apiSocketPath); os.IsNotExist(err) {
			continue
		}

		apiClient := firecracker.NewAPIClient(apiSocketPath)

		// Get or create state for this VM
		p.vmStateMu.Lock()
		state, exists := p.vmState[task.ID]
		if !exists {
			state = &vmMetricsState{}
			p.vmState[task.ID] = state
		}
		p.vmStateMu.Unlock()

		// Fetch machine config if we don't have it cached
		if state.memoryBytes == 0 {
			if machineConfig, err := apiClient.GetMachineConfig(ctx); err == nil {
				state.memoryBytes = int64(machineConfig.MemSizeMib) * 1024 * 1024
				state.vcpuCount = machineConfig.VCPUCount
			}
		}

		// Fetch metrics from Firecracker API
		fcMetrics, err := apiClient.GetMetrics(ctx)
		if err != nil {
			continue // Skip this VM if we can't get metrics
		}

		// Calculate CPU usage from vcpu exit rate
		currentVCPUExits := fcMetrics.VCPU.ExitIOIn + fcMetrics.VCPU.ExitIOOut
		var cpuPercent float64
		if exists && !state.lastTimestamp.IsZero() {
			elapsedSec := now.Sub(state.lastTimestamp).Seconds()
			if elapsedSec > 0 {
				exitDelta := currentVCPUExits - state.lastVCPUExits
				// Estimate CPU activity: more exits = more activity
				// This is a rough approximation - a VM doing heavy I/O will have many exits
				// Scale factor: assume ~10000 exits/sec = 100% for one vcpu
				exitsPerSec := float64(exitDelta) / elapsedSec
				if state.vcpuCount > 0 {
					cpuPercent = (exitsPerSec / 10000.0) * 100.0 / float64(state.vcpuCount)
				} else {
					cpuPercent = (exitsPerSec / 10000.0) * 100.0
				}
				// Clamp to 0-100
				if cpuPercent < 0 {
					cpuPercent = 0
				}
				if cpuPercent > 100 {
					cpuPercent = 100
				}
			}
		}

		// Update state for next calculation
		state.lastVCPUExits = currentVCPUExits
		state.lastTimestamp = now

		// Convert to dashboard metrics format
		metrics := dashboard.VMMetrics{
			CPUPercent:     cpuPercent,
			MemoryBytes:    state.memoryBytes, // Show configured memory as "used" (we can't see inside the VM)
			MemoryMaxBytes: state.memoryBytes, // Same as above - configured max
			NetworkRxBytes: fcMetrics.Net.RxBytes,
			NetworkTxBytes: fcMetrics.Net.TxBytes,
			DiskUsedBytes:  fcMetrics.Block.ReadBytes + fcMetrics.Block.WriteBytes,
			DiskTotalBytes: 0, // Would need to query disk size
		}

		p.sink.SendMetrics(task.ID, metrics)
	}
}

// CleanupTask removes state for a stopped task.
func (p *MetricsPoller) CleanupTask(taskID string) {
	p.vmStateMu.Lock()
	delete(p.vmState, taskID)
	p.vmStateMu.Unlock()
}

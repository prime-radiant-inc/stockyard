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

// MetricsPoller periodically collects metrics from running VMs.
type MetricsPoller struct {
	daemon   *Daemon
	sink     MetricsSink
	interval time.Duration
	running  bool
	stop     chan struct{}
	mu       sync.RWMutex
}

// NewMetricsPoller creates a new metrics poller.
func NewMetricsPoller(daemon *Daemon, sink MetricsSink, interval time.Duration) *MetricsPoller {
	return &MetricsPoller{
		daemon:   daemon,
		sink:     sink,
		interval: interval,
		stop:     make(chan struct{}),
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

		// Fetch metrics from Firecracker API
		apiClient := firecracker.NewAPIClient(apiSocketPath)
		fcMetrics, err := apiClient.GetMetrics(ctx)
		if err != nil {
			continue // Skip this VM if we can't get metrics
		}

		// Convert to dashboard metrics format
		metrics := dashboard.VMMetrics{
			CPUPercent:     0, // CPU % requires calculation from vcpu counters over time
			MemoryBytes:    0, // Memory requires reading from machine-config
			MemoryMaxBytes: 0,
			NetworkRxBytes: fcMetrics.Net.RxBytes,
			NetworkTxBytes: fcMetrics.Net.TxBytes,
			DiskUsedBytes:  fcMetrics.Block.ReadBytes + fcMetrics.Block.WriteBytes,
			DiskTotalBytes: 0, // Would need to query disk size
		}

		p.sink.SendMetrics(task.ID, metrics)
	}
}

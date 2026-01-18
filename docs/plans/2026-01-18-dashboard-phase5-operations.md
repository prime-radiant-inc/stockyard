# Dashboard Phase 5: Operations & Metrics Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add VM creation/restart from dashboard, resources view, and fix metrics collection (both host and VM).

**Architecture:**
- Extend DaemonAPI interface with CreateTask/RestartTask
- Add host metrics collection using `/proc` filesystem
- Fix VM metrics by configuring Firecracker metrics FIFO during VM creation
- Add resources view showing VMs, ZFS datasets, TAP interfaces, DHCP leases

**Tech Stack:** Go, htmx, Alpine.js, Tailwind, gopsutil (or direct /proc reading)

---

## Part 1: Fix VM Metrics (Firecracker Metrics Configuration)

The current metrics implementation is broken because it tries `GET /metrics` which doesn't exist in Firecracker. The correct approach:

1. Configure metrics during VM creation via `PUT /metrics` with a FIFO path
2. Read metrics from that FIFO periodically
3. Parse the NDJSON format Firecracker writes

---

### Task 1: Add SetMetrics API Method

**Files:**
- Modify: `pkg/firecracker/api.go`
- Modify: `pkg/firecracker/api_test.go`

**Step 1: Write the failing test**

Add to `pkg/firecracker/api_test.go`:

```go
func TestAPIClient_SetMetrics(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	var receivedPath string
	var receivedBody map[string]interface{}

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	go server.Serve(listener)
	defer server.Close()

	client := NewAPIClient(socketPath)
	err := client.SetMetrics(context.Background(), "/tmp/metrics.fifo")
	if err != nil {
		t.Fatalf("SetMetrics failed: %v", err)
	}

	if receivedPath != "/metrics" {
		t.Errorf("expected path /metrics, got %s", receivedPath)
	}
	if receivedBody["metrics_path"] != "/tmp/metrics.fifo" {
		t.Errorf("wrong metrics_path: %v", receivedBody)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/firecracker/... -run TestAPIClient_SetMetrics -v`
Expected: FAIL with "SetMetrics undefined"

**Step 3: Write minimal implementation**

Add to `pkg/firecracker/api.go`:

```go
// SetMetrics configures where Firecracker writes metrics.
// The path should be a file or FIFO that will receive NDJSON metrics.
func (a *APIClient) SetMetrics(ctx context.Context, metricsPath string) error {
	return a.put(ctx, "/metrics", map[string]string{
		"metrics_path": metricsPath,
	})
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/firecracker/... -run TestAPIClient_SetMetrics -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/firecracker/api.go pkg/firecracker/api_test.go
git commit -m "feat(firecracker): add SetMetrics API method for metrics configuration"
```

---

### Task 2: Create Metrics FIFO During VM Creation

**Files:**
- Modify: `pkg/firecracker/client.go:74-283` (CreateVM function)

**Step 1: Update CreateVM to create metrics FIFO and configure Firecracker**

In the `CreateVM` function, after the API socket is ready and before `StartInstance`:

```go
// Create metrics FIFO
metricsPath := filepath.Join(vmDir, "metrics.fifo")
if err := syscall.Mkfifo(metricsPath, 0644); err != nil && !os.IsExist(err) {
	cmd.Process.Kill()
	destroyZFSDataset(vmDatasetPath)
	c.network.DeleteTap(tapName)
	return nil, fmt.Errorf("create metrics fifo: %w", err)
}

// Configure metrics before starting instance
if err := apiClient.SetMetrics(ctx, metricsPath); err != nil {
	// Log warning but don't fail - metrics are optional
	fmt.Printf("Warning: failed to configure metrics: %v\n", err)
}
```

**Step 2: Store metrics path in VMInfo**

Add to VMInfo struct in `types.go`:

```go
type VMInfo struct {
	// ... existing fields
	MetricsPath string // Path to metrics FIFO
}
```

Return it from CreateVM:

```go
return &VMInfo{
	// ... existing fields
	MetricsPath: metricsPath,
}, nil
```

**Step 3: Run tests**

Run: `go test ./pkg/firecracker/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/firecracker/client.go pkg/firecracker/types.go
git commit -m "feat(firecracker): create metrics FIFO during VM creation"
```

---

### Task 3: Create Metrics FIFO Reader

**Files:**
- Create: `pkg/daemon/metrics_reader.go`
- Create: `pkg/daemon/metrics_reader_test.go`

**Step 1: Write the failing test**

Create `pkg/daemon/metrics_reader_test.go`:

```go
package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMetricsFIFOReader_ReadsMetrics(t *testing.T) {
	tmpDir := t.TempDir()
	fifoPath := filepath.Join(tmpDir, "metrics.fifo")

	if err := syscall.Mkfifo(fifoPath, 0644); err != nil {
		t.Fatalf("failed to create fifo: %v", err)
	}

	received := make(chan *FirecrackerMetricsData, 10)
	reader := NewMetricsFIFOReader(fifoPath, func(m *FirecrackerMetricsData) {
		received <- m
	})

	if err := reader.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer reader.Stop()

	// Write test metrics to FIFO
	go func() {
		f, _ := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		defer f.Close()
		f.WriteString(`{"utc_timestamp_ms":1234567890,"vcpu":{"exit_io_in":100,"exit_io_out":50},"net":{"rx_bytes":1024,"tx_bytes":512}}` + "\n")
	}()

	select {
	case m := <-received:
		if m.Net.RxBytes != 1024 {
			t.Errorf("expected rx_bytes 1024, got %d", m.Net.RxBytes)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for metrics")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/... -run TestMetricsFIFOReader -v`
Expected: FAIL - MetricsFIFOReader undefined

**Step 3: Write minimal implementation**

Create `pkg/daemon/metrics_reader.go`:

```go
package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
)

// FirecrackerMetricsData matches Firecracker's metrics NDJSON format.
type FirecrackerMetricsData struct {
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

// MetricsCallback is called when new metrics are received.
type MetricsCallback func(*FirecrackerMetricsData)

// MetricsFIFOReader reads metrics from a Firecracker metrics FIFO.
type MetricsFIFOReader struct {
	path     string
	callback MetricsCallback
	stop     chan struct{}
	running  bool
	mu       sync.Mutex
}

// NewMetricsFIFOReader creates a new FIFO reader.
func NewMetricsFIFOReader(path string, callback MetricsCallback) *MetricsFIFOReader {
	return &MetricsFIFOReader{
		path:     path,
		callback: callback,
		stop:     make(chan struct{}),
	}
}

// Start begins reading from the FIFO.
func (r *MetricsFIFOReader) Start() error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return nil
	}
	r.running = true
	r.stop = make(chan struct{})
	r.mu.Unlock()

	go r.readLoop()
	return nil
}

// Stop stops reading from the FIFO.
func (r *MetricsFIFOReader) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	close(r.stop)
	r.running = false
}

func (r *MetricsFIFOReader) readLoop() {
	for {
		select {
		case <-r.stop:
			return
		default:
		}

		// Open FIFO (blocks until writer connects)
		file, err := os.Open(r.path)
		if err != nil {
			select {
			case <-r.stop:
				return
			default:
				continue
			}
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			select {
			case <-r.stop:
				file.Close()
				return
			default:
			}

			var metrics FirecrackerMetricsData
			if err := json.Unmarshal(scanner.Bytes(), &metrics); err != nil {
				continue
			}
			r.callback(&metrics)
		}
		file.Close()
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/... -run TestMetricsFIFOReader -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/daemon/metrics_reader.go pkg/daemon/metrics_reader_test.go
git commit -m "feat(daemon): add FIFO reader for Firecracker metrics"
```

---

### Task 4: Update MetricsPoller to Use FIFO Reader

**Files:**
- Modify: `pkg/daemon/metrics.go`
- Modify: `pkg/daemon/metrics_test.go`

**Step 1: Refactor MetricsPoller to manage FIFO readers per VM**

Replace the `collectMetrics` approach with per-VM FIFO readers:

```go
type MetricsPoller struct {
	daemon   *Daemon
	sink     MetricsSink
	readers  map[string]*MetricsFIFOReader // taskID -> reader
	state    map[string]*vmMetricsState
	mu       sync.Mutex
	running  bool
	stop     chan struct{}
}

// StartTaskMetrics begins reading metrics for a task.
func (p *MetricsPoller) StartTaskMetrics(taskID, metricsPath string, memoryBytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.readers[taskID]; exists {
		return
	}

	state := &vmMetricsState{
		memoryBytes: memoryBytes,
	}
	p.state[taskID] = state

	reader := NewMetricsFIFOReader(metricsPath, func(m *FirecrackerMetricsData) {
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
		reader.Stop()
		delete(p.readers, taskID)
		delete(p.state, taskID)
	}
}

func (p *MetricsPoller) handleMetrics(taskID string, state *vmMetricsState, m *FirecrackerMetricsData) {
	now := time.Now()

	// Calculate CPU usage from vcpu exit rate
	currentVCPUExits := m.VCPU.ExitIOIn + m.VCPU.ExitIOOut
	var cpuPercent float64
	if !state.lastTimestamp.IsZero() {
		elapsedSec := now.Sub(state.lastTimestamp).Seconds()
		if elapsedSec > 0 {
			exitDelta := currentVCPUExits - state.lastVCPUExits
			exitsPerSec := float64(exitDelta) / elapsedSec
			cpuPercent = (exitsPerSec / 10000.0) * 100.0
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
	}

	p.sink.SendMetrics(taskID, metrics)
}
```

**Step 2: Update daemon to call StartTaskMetrics when VMs start**

In `pkg/daemon/tasks.go`, after VM is created:

```go
// Start metrics collection if dashboard is enabled
if tm.daemon.metricsPoller != nil && vmInfo.MetricsPath != "" {
	memoryBytes := int64(config.MemoryMB) * 1024 * 1024
	tm.daemon.metricsPoller.StartTaskMetrics(taskID, vmInfo.MetricsPath, memoryBytes)
}
```

And in StopTask:

```go
// Stop metrics collection
if tm.daemon.metricsPoller != nil {
	tm.daemon.metricsPoller.StopTaskMetrics(taskID)
}
```

**Step 3: Run tests**

Run: `go test ./pkg/daemon/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/daemon/metrics.go pkg/daemon/tasks.go
git commit -m "refactor(daemon): use FIFO reader for VM metrics instead of GET /metrics"
```

---

## Part 2: Add Host Metrics

---

### Task 5: Create Host Metrics Collector

**Files:**
- Create: `pkg/daemon/host_metrics.go`
- Create: `pkg/daemon/host_metrics_test.go`

**Step 1: Write the failing test**

Create `pkg/daemon/host_metrics_test.go`:

```go
package daemon

import (
	"testing"
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
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/daemon/... -run TestHostMetricsCollector -v`
Expected: FAIL - HostMetricsCollector undefined

**Step 3: Write minimal implementation**

Create `pkg/daemon/host_metrics.go`:

```go
package daemon

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// HostMetrics contains host system metrics.
type HostMetrics struct {
	CPUPercent       float64
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	NetworkRxBytes   int64
	NetworkTxBytes   int64
	DiskReadBytes    int64
	DiskWriteBytes   int64
}

// HostMetricsCollector collects host system metrics from /proc.
type HostMetricsCollector struct {
	lastCPUStats  cpuStats
	lastTimestamp time.Time
}

type cpuStats struct {
	user, nice, system, idle, iowait, irq, softirq int64
}

// NewHostMetricsCollector creates a new host metrics collector.
func NewHostMetricsCollector() *HostMetricsCollector {
	return &HostMetricsCollector{}
}

// Collect gathers current host metrics.
func (c *HostMetricsCollector) Collect() (*HostMetrics, error) {
	metrics := &HostMetrics{}

	// CPU from /proc/stat
	cpuPercent, err := c.collectCPU()
	if err == nil {
		metrics.CPUPercent = cpuPercent
	}

	// Memory from /proc/meminfo
	memUsed, memTotal, err := c.collectMemory()
	if err == nil {
		metrics.MemoryUsedBytes = memUsed
		metrics.MemoryTotalBytes = memTotal
	}

	// Network from /proc/net/dev (aggregate all interfaces except lo)
	rxBytes, txBytes, err := c.collectNetwork()
	if err == nil {
		metrics.NetworkRxBytes = rxBytes
		metrics.NetworkTxBytes = txBytes
	}

	// Disk I/O from /proc/diskstats
	readBytes, writeBytes, err := c.collectDiskIO()
	if err == nil {
		metrics.DiskReadBytes = readBytes
		metrics.DiskWriteBytes = writeBytes
	}

	return metrics, nil
}

func (c *HostMetricsCollector) collectCPU() (float64, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}

			user, _ := strconv.ParseInt(fields[1], 10, 64)
			nice, _ := strconv.ParseInt(fields[2], 10, 64)
			system, _ := strconv.ParseInt(fields[3], 10, 64)
			idle, _ := strconv.ParseInt(fields[4], 10, 64)
			iowait, _ := strconv.ParseInt(fields[5], 10, 64)
			irq, _ := strconv.ParseInt(fields[6], 10, 64)
			softirq, _ := strconv.ParseInt(fields[7], 10, 64)

			current := cpuStats{user, nice, system, idle, iowait, irq, softirq}

			if c.lastTimestamp.IsZero() {
				c.lastCPUStats = current
				c.lastTimestamp = time.Now()
				return 0, nil
			}

			// Calculate deltas
			userDelta := current.user - c.lastCPUStats.user
			niceDelta := current.nice - c.lastCPUStats.nice
			systemDelta := current.system - c.lastCPUStats.system
			idleDelta := current.idle - c.lastCPUStats.idle
			iowaitDelta := current.iowait - c.lastCPUStats.iowait
			irqDelta := current.irq - c.lastCPUStats.irq
			softirqDelta := current.softirq - c.lastCPUStats.softirq

			totalDelta := userDelta + niceDelta + systemDelta + idleDelta + iowaitDelta + irqDelta + softirqDelta
			activeDelta := totalDelta - idleDelta - iowaitDelta

			c.lastCPUStats = current
			c.lastTimestamp = time.Now()

			if totalDelta > 0 {
				return float64(activeDelta) / float64(totalDelta) * 100, nil
			}
			return 0, nil
		}
	}
	return 0, nil
}

func (c *HostMetricsCollector) collectMemory() (used, total int64, err error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	var memTotal, memAvailable int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		value *= 1024 // Convert from KB to bytes

		switch fields[0] {
		case "MemTotal:":
			memTotal = value
		case "MemAvailable:":
			memAvailable = value
		}
	}

	return memTotal - memAvailable, memTotal, nil
}

func (c *HostMetricsCollector) collectNetwork() (rx, tx int64, err error) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ":") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		iface := strings.TrimSuffix(fields[0], ":")
		if iface == "lo" {
			continue
		}

		rxBytes, _ := strconv.ParseInt(fields[1], 10, 64)
		txBytes, _ := strconv.ParseInt(fields[9], 10, 64)
		rx += rxBytes
		tx += txBytes
	}

	return rx, tx, nil
}

func (c *HostMetricsCollector) collectDiskIO() (read, write int64, err error) {
	file, err := os.Open("/proc/diskstats")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}

		device := fields[2]
		// Only count physical disks (sd*, nvme*, vd*)
		if !strings.HasPrefix(device, "sd") &&
		   !strings.HasPrefix(device, "nvme") &&
		   !strings.HasPrefix(device, "vd") {
			continue
		}
		// Skip partitions
		if strings.ContainsAny(device[len(device)-1:], "0123456789") &&
		   !strings.Contains(device, "nvme") {
			continue
		}

		// Fields: sectors read (5), sectors written (9) - multiply by 512 for bytes
		sectorsRead, _ := strconv.ParseInt(fields[5], 10, 64)
		sectorsWritten, _ := strconv.ParseInt(fields[9], 10, 64)
		read += sectorsRead * 512
		write += sectorsWritten * 512
	}

	return read, write, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./pkg/daemon/... -run TestHostMetricsCollector -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/daemon/host_metrics.go pkg/daemon/host_metrics_test.go
git commit -m "feat(daemon): add host metrics collector reading from /proc"
```

---

### Task 6: Add Host Metrics to Dashboard

**Files:**
- Modify: `pkg/dashboard/metrics.go`
- Modify: `pkg/dashboard/server.go`
- Modify: `pkg/dashboard/templates/fleet.html`

**Step 1: Add HostMetrics type and broadcast**

Add to `pkg/dashboard/metrics.go`:

```go
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
```

**Step 2: Add host metrics polling to daemon**

In `pkg/daemon/daemon.go`, add a host metrics polling goroutine:

```go
// Start host metrics collection
if d.dashboardServer != nil {
	go d.pollHostMetrics(metricsCollector)
}

func (d *Daemon) pollHostMetrics(collector *dashboard.MetricsCollector) {
	hostCollector := NewHostMetricsCollector()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			metrics, err := hostCollector.Collect()
			if err != nil {
				continue
			}
			collector.SendHostMetrics(dashboard.HostMetrics{
				CPUPercent:       metrics.CPUPercent,
				MemoryUsedBytes:  metrics.MemoryUsedBytes,
				MemoryTotalBytes: metrics.MemoryTotalBytes,
				NetworkRxBytes:   metrics.NetworkRxBytes,
				NetworkTxBytes:   metrics.NetworkTxBytes,
				DiskReadBytes:    metrics.DiskReadBytes,
				DiskWriteBytes:   metrics.DiskWriteBytes,
			})
		case <-d.ctx.Done():
			return
		}
	}
}
```

**Step 3: Update fleet.html to display host metrics**

Add a host metrics bar at the top of the fleet page:

```html
<!-- Host Metrics Bar -->
<div class="bg-gray-800 text-white px-4 py-2 flex items-center justify-between text-sm"
     x-data="{
         cpu: '--',
         memory: '--',
         memoryTotal: '--',
         network: '--',
         disk: '--'
     }"
     x-init="
         const ws = new WebSocket('ws://' + window.location.host + '/ws');
         ws.onmessage = (e) => {
             const data = JSON.parse(e.data);
             if (data.type === 'host_metrics') {
                 cpu = data.metrics.cpu_percent.toFixed(1) + '%';
                 memory = formatBytes(data.metrics.memory_used_bytes);
                 memoryTotal = formatBytes(data.metrics.memory_total_bytes);
                 network = formatBytes(data.metrics.network_rx_bytes + data.metrics.network_tx_bytes);
                 disk = formatBytes(data.metrics.disk_read_bytes + data.metrics.disk_write_bytes);
             }
         };
     ">
    <div class="flex items-center gap-6">
        <span class="font-semibold">Host</span>
        <div class="flex items-center gap-1">
            <span class="text-gray-400">CPU</span>
            <span x-text="cpu">--</span>
        </div>
        <div class="flex items-center gap-1">
            <span class="text-gray-400">Memory</span>
            <span><span x-text="memory">--</span> / <span x-text="memoryTotal">--</span></span>
        </div>
        <div class="flex items-center gap-1">
            <span class="text-gray-400">Network</span>
            <span x-text="network">--</span>
        </div>
        <div class="flex items-center gap-1">
            <span class="text-gray-400">Disk I/O</span>
            <span x-text="disk">--</span>
        </div>
    </div>
</div>
```

**Step 4: Run tests**

Run: `go test ./pkg/dashboard/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/dashboard/metrics.go pkg/daemon/daemon.go pkg/dashboard/templates/fleet.html
git commit -m "feat(dashboard): add host metrics display in fleet view"
```

---

## Part 3: Create VM from Dashboard

---

### Task 7: Add CreateTask to DaemonAPI Interface

**Files:**
- Modify: `pkg/dashboard/daemon.go`
- Modify: `pkg/dashboard/adapter.go`

**Step 1: Extend DaemonAPI interface**

Add to `pkg/dashboard/daemon.go`:

```go
type DaemonAPI interface {
	// ... existing methods
	CreateTask(ctx context.Context, req CreateTaskRequest) (*Task, error)
}

// CreateTaskRequest contains the parameters for creating a new task.
type CreateTaskRequest struct {
	Repo     string
	Ref      string
	Name     string
	CPUs     int32
	MemoryMB int32
	Env      map[string]string
}
```

**Step 2: Implement in adapter**

Add to `pkg/dashboard/adapter.go`:

```go
func (a *DaemonAdapter) CreateTask(ctx context.Context, req CreateTaskRequest) (*Task, error) {
	// Convert to daemon CreateTaskRequest and call
	result, err := a.daemon.CreateTask(ctx, &daemon.CreateTaskParams{
		Repo:     req.Repo,
		Ref:      req.Ref,
		Name:     req.Name,
		CPUs:     req.CPUs,
		MemoryMB: req.MemoryMB,
		Env:      req.Env,
	})
	if err != nil {
		return nil, err
	}
	return convertTask(result), nil
}
```

**Step 3: Commit**

```bash
git add pkg/dashboard/daemon.go pkg/dashboard/adapter.go
git commit -m "feat(dashboard): add CreateTask to DaemonAPI interface"
```

---

### Task 8: Add Create VM API Endpoint

**Files:**
- Modify: `pkg/dashboard/server.go`

**Step 1: Add POST /api/vm handler**

```go
func (s *Server) handleAPIVMCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Repo     string            `json:"repo"`
		Ref      string            `json:"ref"`
		Name     string            `json:"name"`
		CPUs     int32             `json:"cpus"`
		MemoryMB int32             `json:"memory_mb"`
		Env      map[string]string `json:"env"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Repo == "" {
		http.Error(w, "repo is required", http.StatusBadRequest)
		return
	}

	// Defaults
	if req.Ref == "" {
		req.Ref = "main"
	}
	if req.CPUs == 0 {
		req.CPUs = 2
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 4096
	}

	task, err := s.daemon.CreateTask(r.Context(), CreateTaskRequest{
		Repo:     req.Repo,
		Ref:      req.Ref,
		Name:     req.Name,
		CPUs:     req.CPUs,
		MemoryMB: req.MemoryMB,
		Env:      req.Env,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     task.ID,
		"status": task.Status,
	})
}
```

**Step 2: Register route**

In `registerRoutes()`:

```go
s.mux.HandleFunc("/api/vm", s.handleAPIVMCreate) // POST for create
```

**Step 3: Commit**

```bash
git add pkg/dashboard/server.go
git commit -m "feat(dashboard): add POST /api/vm endpoint for creating VMs"
```

---

### Task 9: Add Create VM Modal to Fleet Page

**Files:**
- Modify: `pkg/dashboard/templates/fleet.html`

**Step 1: Add create button and modal**

Add a "New VM" button in the fleet header:

```html
<div class="flex items-center gap-4">
    <h1 class="text-2xl font-bold text-gray-900">Fleet Overview</h1>
    <button @click="showCreateModal = true"
            class="px-3 py-1.5 bg-blue-600 text-white text-sm font-medium rounded-lg hover:bg-blue-700">
        + New VM
    </button>
</div>
```

Add the modal:

```html
<!-- Create VM Modal -->
<div x-show="showCreateModal"
     x-cloak
     class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
     @keydown.escape.window="showCreateModal = false">
    <div class="bg-white rounded-lg shadow-xl w-full max-w-md p-6"
         @click.outside="showCreateModal = false">
        <h2 class="text-lg font-semibold mb-4">Create New VM</h2>

        <form @submit.prevent="createVM()">
            <div class="space-y-4">
                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">Repository URL *</label>
                    <input type="text" x-model="newVM.repo" required
                           placeholder="github.com/owner/repo"
                           class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500">
                </div>

                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">Git Ref</label>
                    <input type="text" x-model="newVM.ref"
                           placeholder="main"
                           class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500">
                </div>

                <div>
                    <label class="block text-sm font-medium text-gray-700 mb-1">Name (optional)</label>
                    <input type="text" x-model="newVM.name"
                           placeholder="my-task"
                           class="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500">
                </div>

                <div class="grid grid-cols-2 gap-4">
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-1">CPUs</label>
                        <input type="number" x-model="newVM.cpus" min="1" max="8"
                               class="w-full px-3 py-2 border border-gray-300 rounded-lg">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-1">Memory (MB)</label>
                        <input type="number" x-model="newVM.memory_mb" min="512" max="16384" step="512"
                               class="w-full px-3 py-2 border border-gray-300 rounded-lg">
                    </div>
                </div>
            </div>

            <div class="flex justify-end gap-3 mt-6">
                <button type="button" @click="showCreateModal = false"
                        class="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg">
                    Cancel
                </button>
                <button type="submit"
                        :disabled="creating"
                        class="px-4 py-2 bg-blue-600 text-white font-medium rounded-lg hover:bg-blue-700 disabled:opacity-50">
                    <span x-show="!creating">Create VM</span>
                    <span x-show="creating">Creating...</span>
                </button>
            </div>
        </form>

        <div x-show="createError" class="mt-4 p-3 bg-red-50 text-red-700 rounded-lg text-sm" x-text="createError"></div>
    </div>
</div>
```

Add Alpine.js data and methods:

```html
<body x-data="{
    showCreateModal: false,
    creating: false,
    createError: '',
    newVM: {
        repo: '',
        ref: 'main',
        name: '',
        cpus: 2,
        memory_mb: 4096
    },
    async createVM() {
        this.creating = true;
        this.createError = '';
        try {
            const resp = await fetch('/api/vm', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify(this.newVM)
            });
            if (!resp.ok) {
                const text = await resp.text();
                throw new Error(text);
            }
            this.showCreateModal = false;
            this.newVM = {repo: '', ref: 'main', name: '', cpus: 2, memory_mb: 4096};
            location.reload();
        } catch (e) {
            this.createError = e.message;
        } finally {
            this.creating = false;
        }
    }
}">
```

**Step 2: Commit**

```bash
git add pkg/dashboard/templates/fleet.html
git commit -m "feat(dashboard): add create VM modal to fleet page"
```

---

## Part 4: Restart VM from Dashboard

---

### Task 10: Add RestartTask to DaemonAPI and Endpoint

**Files:**
- Modify: `pkg/dashboard/daemon.go`
- Modify: `pkg/dashboard/adapter.go`
- Modify: `pkg/dashboard/server.go`

**Step 1: Add RestartTask to interface**

```go
type DaemonAPI interface {
	// ... existing
	RestartTask(ctx context.Context, id string) error
}
```

**Step 2: Implement in adapter**

```go
func (a *DaemonAdapter) RestartTask(ctx context.Context, id string) error {
	return a.daemon.RestartTask(ctx, id)
}
```

**Step 3: Add API endpoint**

In `handleAPIVM`:

```go
case http.MethodPost:
	action := r.URL.Query().Get("action")
	switch action {
	case "stop":
		// existing stop logic
	case "restart":
		if err := s.daemon.RestartTask(r.Context(), taskID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
```

**Step 4: Add restart button to VM detail page**

In `vm_detail.html`, add next to the stop button:

```html
<button hx-post="/api/vm/{{.Task.ID}}?action=restart"
        hx-swap="none"
        hx-on::after-request="if(event.detail.successful) location.reload()"
        class="px-3 py-1.5 bg-green-600 text-white text-sm font-medium rounded hover:bg-green-700"
        {{if eq .Task.Status "running"}}disabled{{end}}>
    Restart
</button>
```

**Step 5: Commit**

```bash
git add pkg/dashboard/daemon.go pkg/dashboard/adapter.go pkg/dashboard/server.go pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add restart VM functionality"
```

---

## Part 5: Resources View

---

### Task 11: Create Resources Page Handler

**Files:**
- Modify: `pkg/dashboard/server.go`
- Create: `pkg/dashboard/resources.go`
- Create: `pkg/dashboard/templates/resources.html`

**Step 1: Create resources collector**

Create `pkg/dashboard/resources.go`:

```go
package dashboard

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Resource represents a system resource.
type Resource struct {
	ID       string
	Type     string // "vm", "zfs_vm", "zfs_workspace", "tap", "dhcp"
	Status   string // "active", "stopped", "orphan"
	TaskID   string
	Details  string
}

// ResourceCollector gathers system resources.
type ResourceCollector struct {
	vmDir     string
	leaseFile string
	zfsPool   string
}

// NewResourceCollector creates a resource collector with the given paths.
func NewResourceCollector(vmDir, leaseFile, zfsPool string) *ResourceCollector {
	return &ResourceCollector{
		vmDir:     vmDir,
		leaseFile: leaseFile,
		zfsPool:   zfsPool,
	}
}

// Collect gathers all system resources.
func (rc *ResourceCollector) Collect(tasks []Task) ([]Resource, error) {
	taskStatus := make(map[string]string)
	for _, t := range tasks {
		taskStatus[t.ID] = t.Status
	}

	var resources []Resource

	// Collect VM directories
	resources = append(resources, rc.collectVMDirs(taskStatus)...)

	// Collect TAP interfaces
	resources = append(resources, rc.collectTapInterfaces(taskStatus)...)

	// Collect ZFS datasets
	resources = append(resources, rc.collectZFSDatasets(taskStatus)...)

	// Collect DHCP leases
	resources = append(resources, rc.collectDHCPLeases(taskStatus)...)

	return resources, nil
}

func (rc *ResourceCollector) collectVMDirs(taskStatus map[string]string) []Resource {
	var resources []Resource
	entries, err := os.ReadDir(rc.vmDir)
	if err != nil {
		return resources
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		status := "orphan"
		if s, ok := taskStatus[id]; ok {
			status = s
		}
		resources = append(resources, Resource{
			ID:     id,
			Type:   "vm",
			Status: status,
			TaskID: id,
		})
	}
	return resources
}

func (rc *ResourceCollector) collectTapInterfaces(taskStatus map[string]string) []Resource {
	var resources []Resource

	// Build tap->task mapping from VM directories
	tapToTask := make(map[string]string)
	entries, _ := os.ReadDir(rc.vmDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		tapFile := filepath.Join(rc.vmDir, entry.Name(), "tap_name")
		if data, err := os.ReadFile(tapFile); err == nil {
			tapToTask[strings.TrimSpace(string(data))] = entry.Name()
		}
	}

	cmd := exec.Command("ip", "-o", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return resources
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSuffix(fields[1], ":")
		if !strings.HasPrefix(name, "tap-") {
			continue
		}

		status := "orphan"
		taskID := ""
		if tid, ok := tapToTask[name]; ok {
			taskID = tid
			if s, ok := taskStatus[tid]; ok {
				status = s
			}
		}

		resources = append(resources, Resource{
			ID:     name,
			Type:   "tap",
			Status: status,
			TaskID: taskID,
		})
	}
	return resources
}

func (rc *ResourceCollector) collectZFSDatasets(taskStatus map[string]string) []Resource {
	var resources []Resource

	for _, base := range []string{"vms", "workspaces"} {
		basePath := rc.zfsPool + "/" + base
		cmd := exec.Command("zfs", "list", "-H", "-r", "-d", "1", "-o", "name,used", basePath)
		output, err := cmd.Output()
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			name := fields[0]
			if name == basePath {
				continue
			}

			id := filepath.Base(name)
			status := "orphan"
			if s, ok := taskStatus[id]; ok {
				status = s
			}

			resources = append(resources, Resource{
				ID:      name,
				Type:    "zfs_" + base[:len(base)-1], // "zfs_vm" or "zfs_workspace"
				Status:  status,
				TaskID:  id,
				Details: fields[1],
			})
		}
	}
	return resources
}

func (rc *ResourceCollector) collectDHCPLeases(taskStatus map[string]string) []Resource {
	var resources []Resource
	file, err := os.Open(rc.leaseFile)
	if err != nil {
		return resources
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}

		mac := fields[1]
		ip := fields[2]
		hostname := fields[3]

		// Try to match to a task by MAC
		taskID := ""
		status := "orphan"
		entries, _ := os.ReadDir(rc.vmDir)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			macFile := filepath.Join(rc.vmDir, entry.Name(), "mac_addr")
			if data, err := os.ReadFile(macFile); err == nil {
				if strings.TrimSpace(string(data)) == mac {
					taskID = entry.Name()
					if s, ok := taskStatus[taskID]; ok {
						status = s
					}
					break
				}
			}
		}

		resources = append(resources, Resource{
			ID:      ip,
			Type:    "dhcp",
			Status:  status,
			TaskID:  taskID,
			Details: hostname + " (" + mac + ")",
		})
	}
	return resources
}
```

**Step 2: Add handler**

In `pkg/dashboard/server.go`:

```go
func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.daemon.ListTasks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get config for paths - these should come from config
	collector := NewResourceCollector(
		"/var/lib/stockyard/vms/stockyard",
		"/var/lib/stockyard/dnsmasq.leases",
		"tank/stockyard",
	)

	resources, err := collector.Collect(tasks)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Group by type
	grouped := make(map[string][]Resource)
	for _, r := range resources {
		grouped[r.Type] = append(grouped[r.Type], r)
	}

	// Count orphans
	orphanCount := 0
	for _, r := range resources {
		if r.Status == "orphan" {
			orphanCount++
		}
	}

	data := map[string]interface{}{
		"Resources":   grouped,
		"OrphanCount": orphanCount,
		"TotalCount":  len(resources),
	}

	s.templates.ExecuteTemplate(w, "resources.html", data)
}
```

Register route:

```go
s.mux.HandleFunc("/resources", s.handleResources)
```

**Step 3: Create template**

Create `pkg/dashboard/templates/resources.html`:

```html
{{template "base" .}}
{{define "content"}}
<div class="p-6 max-w-6xl mx-auto">
    <div class="mb-6 flex items-center justify-between">
        <div>
            <h1 class="text-2xl font-bold text-gray-900">System Resources</h1>
            <p class="text-gray-500 mt-1">
                {{.TotalCount}} resources, {{.OrphanCount}} orphaned
            </p>
        </div>
        <a href="/" class="text-blue-600 hover:underline">&larr; Back to Fleet</a>
    </div>

    {{if gt .OrphanCount 0}}
    <div class="mb-6 p-4 bg-yellow-50 border border-yellow-200 rounded-lg">
        <div class="flex items-center gap-2 text-yellow-800">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z"></path>
            </svg>
            <span class="font-medium">{{.OrphanCount}} orphaned resources detected</span>
        </div>
        <p class="mt-1 text-sm text-yellow-700">
            Run <code class="bg-yellow-100 px-1 rounded">stockyard gc --orphans</code> to clean up.
        </p>
    </div>
    {{end}}

    <div class="space-y-6">
        {{range $type, $resources := .Resources}}
        <div class="bg-white rounded-lg border border-gray-200">
            <div class="px-4 py-3 border-b border-gray-200 bg-gray-50 rounded-t-lg">
                <h2 class="font-semibold text-gray-900">
                    {{if eq $type "vm"}}VM Directories
                    {{else if eq $type "tap"}}TAP Interfaces
                    {{else if eq $type "zfs_vm"}}ZFS VM Datasets
                    {{else if eq $type "zfs_workspace"}}ZFS Workspace Datasets
                    {{else if eq $type "dhcp"}}DHCP Leases
                    {{else}}{{$type}}
                    {{end}}
                    <span class="text-gray-500 font-normal">({{len $resources}})</span>
                </h2>
            </div>
            <div class="divide-y divide-gray-100">
                {{range $resources}}
                <div class="px-4 py-2 flex items-center justify-between text-sm">
                    <div class="flex items-center gap-3">
                        <span class="font-mono">{{.ID}}</span>
                        {{if .Details}}
                        <span class="text-gray-500">{{.Details}}</span>
                        {{end}}
                    </div>
                    <div class="flex items-center gap-2">
                        {{if .TaskID}}
                        <a href="/vm/{{.TaskID}}" class="text-blue-600 hover:underline text-xs">{{.TaskID}}</a>
                        {{end}}
                        {{if eq .Status "running"}}
                        <span class="px-2 py-0.5 bg-green-100 text-green-700 rounded text-xs">running</span>
                        {{else if eq .Status "stopped"}}
                        <span class="px-2 py-0.5 bg-gray-100 text-gray-600 rounded text-xs">stopped</span>
                        {{else if eq .Status "orphan"}}
                        <span class="px-2 py-0.5 bg-yellow-100 text-yellow-700 rounded text-xs">orphan</span>
                        {{else}}
                        <span class="px-2 py-0.5 bg-gray-100 text-gray-600 rounded text-xs">{{.Status}}</span>
                        {{end}}
                    </div>
                </div>
                {{end}}
            </div>
        </div>
        {{end}}
    </div>
</div>
{{end}}
```

**Step 4: Add link to fleet page**

In `fleet.html`, add a resources link:

```html
<a href="/resources" class="text-gray-500 hover:text-gray-700 text-sm">
    System Resources
</a>
```

**Step 5: Commit**

```bash
git add pkg/dashboard/resources.go pkg/dashboard/server.go pkg/dashboard/templates/resources.html pkg/dashboard/templates/fleet.html
git commit -m "feat(dashboard): add resources view showing VMs, ZFS, TAP, DHCP"
```

---

## Summary

| Part | Task | Description |
|------|------|-------------|
| 1 | 1-4 | Fix VM metrics using Firecracker FIFO |
| 2 | 5-6 | Add host metrics from /proc |
| 3 | 7-9 | Create VM from dashboard |
| 4 | 10 | Restart VM from dashboard |
| 5 | 11 | Resources view |

**Key fixes:**
- VM metrics now use proper Firecracker metrics FIFO configuration
- Host metrics read from `/proc/stat`, `/proc/meminfo`, `/proc/net/dev`, `/proc/diskstats`
- Dashboard can create and restart VMs
- Resources view shows all system resources with orphan detection

---

## Execution Notes

- Part 1 (Tasks 1-4) fixes the broken VM metrics - this is the most critical fix
- Part 2 (Tasks 5-6) adds host metrics which Jesse specifically requested
- Parts 3-5 add new dashboard functionality
- All tasks follow TDD pattern with tests before implementation

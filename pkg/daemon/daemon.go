package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/dashboard"
	"github.com/obra/stockyard/pkg/firecracker"
	"github.com/obra/stockyard/pkg/network"
	"github.com/obra/stockyard/pkg/rootfs"
	"github.com/obra/stockyard/pkg/secrets"
	"github.com/obra/stockyard/pkg/tailscale"
	"github.com/obra/stockyard/pkg/vmbackend"
	"github.com/obra/stockyard/pkg/zfs"
)

// Daemon is the core daemon process that manages workspaces and tasks.
type Daemon struct {
	cfg       *config.Config
	secrets   secrets.Provider
	zfs       *zfs.Manager
	state        *State
	tasks        *TaskManager
	queueManager *QueueManager
	snapshots    *SnapshotService
	dhcp      *network.DHCPServer
	ipPool    *network.IPPool
	rootfsProvisioner rootfs.Provisioner

	listener     net.Listener
	grpcListener net.Listener // TCP listener for remote gRPC (optional)
	grpcServer   *grpc.Server
	httpServer   *http.Server
	mu         sync.Mutex
	running    bool

	// Real-time dashboard components
	dashboardServer      *dashboard.Server
	metricsPoller        *MetricsPoller
	logTailer            *LogTailer
	statusBroadcaster    *dashboard.StatusBroadcaster
	metricsCollector     *dashboard.MetricsCollector
	hostMetricsCollector *HostMetricsCollector
	ctx                  context.Context
}

// dashboardLogSink adapts LogStreamer and LogHistory to the LogSink interface.
type dashboardLogSink struct {
	streamer   *dashboard.LogStreamer
	logHistory *dashboard.LogHistory
}

func (s *dashboardLogSink) SendLog(taskID, stream, line string) {
	s.streamer.SendLog(taskID, stream, line)
	if s.logHistory != nil {
		s.logHistory.AddLine(taskID, stream, line)
	}
}

// dashboardMetricsSink adapts MetricsCollector to the MetricsSink interface.
type dashboardMetricsSink struct {
	collector *dashboard.MetricsCollector
}

func (s *dashboardMetricsSink) SendMetrics(taskID string, metrics dashboard.VMMetrics) {
	s.collector.SendMetrics(taskID, metrics)
}

// New creates a new Daemon instance with the given configuration and secrets provider.
func New(cfg *config.Config, secretsProvider secrets.Provider) (*Daemon, error) {
	// Only create ZFS manager for Firecracker backend (ZFS doesn't exist on macOS)
	var zfsMgr *zfs.Manager
	if cfg.Backend == "" || cfg.Backend == "firecracker" {
		zfsMgr = zfs.NewManager(cfg.ZFS.Pool, cfg.ZFS.BasePath)
	}

	state, err := NewState(cfg.Daemon.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state: %w", err)
	}

	d := &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		zfs:     zfsMgr,
		state:   state,
	}

	// Initialize task manager with VM backend
	var backend vmbackend.Backend
	switch cfg.Backend {
	case "", "firecracker":
		if cfg.Firecracker.KernelPath != "" && cfg.Firecracker.RootfsPath != "" {
			fcCfg := firecracker.ClientConfig{
				KernelPath: cfg.Firecracker.KernelPath,
				RootfsPath: cfg.Firecracker.RootfsPath,
				BridgeName: cfg.Firecracker.BridgeName,
				ImagesPath: cfg.ZFS.ImagesPath,
				VMsPath:    cfg.ZFS.VMsPath,
			}
			client, err := firecracker.NewClient(fcCfg, d.zfs)
			if err != nil {
				fmt.Printf("Warning: failed to create firecracker client: %v\n", err)
			} else {
				backend = vmbackend.NewFirecrackerBackend(client)
			}
		}
	case "vfkit":
		var err error
		backend, err = createVfkitBackend(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create vfkit backend: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown backend: %s", cfg.Backend)
	}
	d.tasks = NewTaskManager(d, backend)
	d.rootfsProvisioner = createRootfsProvisioner(cfg)
	d.queueManager = NewQueueManager(state, cfg)

	// DHCP and IP pool are only needed for Firecracker backend
	if cfg.Backend == "" || cfg.Backend == "firecracker" {
		// Initialize DHCP server
		dhcpConfig := network.DHCPConfig{
			Bridge:     cfg.Firecracker.BridgeName,
			Gateway:    cfg.Firecracker.VMGateway,
			RangeStart: cfg.Firecracker.DHCPRangeStart,
			RangeEnd:   cfg.Firecracker.DHCPRangeEnd,
			Netmask:    "255.255.255.0", // /24
			LeaseTime:  cfg.Firecracker.DHCPLeaseTime,
			DNS:        "8.8.8.8",
		}
		dhcpServer, err := network.NewDHCPServer(dhcpConfig, cfg.Daemon.DataDir)
		if err != nil {
			return nil, fmt.Errorf("failed to create DHCP server: %w", err)
		}
		d.dhcp = dhcpServer

		// Initialize IP pool for static VM IPs
		// Use the gateway and a /24 prefix (standard for VM networks)
		ipPool, err := network.NewIPPoolFromGateway(cfg.Firecracker.VMGateway, 24)
		if err != nil {
			return nil, fmt.Errorf("failed to create IP pool: %w", err)
		}
		// Persist allocations to survive daemon restarts
		ipPool.SetPersistPath(filepath.Join(cfg.Daemon.DataDir, "ip_pool.json"))
		if err := ipPool.LoadState(); err != nil {
			// Log warning but continue - fresh state is fine
			fmt.Printf("Warning: could not load IP pool state: %v\n", err)
		}
		d.ipPool = ipPool
	}

	return d, nil
}

// reconcileRunningVMs checks all tasks marked as "running" and updates their
// status if the underlying Firecracker process is no longer alive. This handles
// VMs that may have died while the daemon was stopped.
func (d *Daemon) reconcileRunningVMs() {
	tasks, err := d.state.ListTasks("running")
	if err != nil {
		fmt.Printf("Warning: Failed to list running tasks for reconciliation: %v\n", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	fmt.Printf("Reconciling %d running task(s)...\n", len(tasks))

	// VM state directory: <data-dir>/vms/stockyard/<task-id>/
	vmStateDir := filepath.Join(d.cfg.Daemon.DataDir, "vms")
	const vmNamespace = "stockyard"

	for _, task := range tasks {
		// Check for PID file from either backend
		pidFile := filepath.Join(vmStateDir, vmNamespace, task.ID, "firecracker.pid")
		if _, err := os.Stat(pidFile); os.IsNotExist(err) {
			pidFile = filepath.Join(vmStateDir, vmNamespace, task.ID, "vfkit.pid")
		}
		pidData, err := os.ReadFile(pidFile)
		if err != nil {
			// PID file doesn't exist - VM is definitely not running
			fmt.Printf("  Task %s: PID file missing, marking as stopped\n", task.ID)
			d.state.UpdateTaskStatus(task.ID, "stopped")
			continue
		}

		var pid int
		if _, err := fmt.Sscanf(string(pidData), "%d", &pid); err != nil {
			fmt.Printf("  Task %s: Invalid PID file, marking as stopped\n", task.ID)
			d.state.UpdateTaskStatus(task.ID, "stopped")
			continue
		}

		// Check if the process is still running
		process, err := os.FindProcess(pid)
		if err != nil {
			fmt.Printf("  Task %s: Process %d not found, marking as stopped\n", task.ID, pid)
			d.state.UpdateTaskStatus(task.ID, "stopped")
			continue
		}

		// Signal 0 checks if process exists without actually sending a signal
		if err := process.Signal(syscall.Signal(0)); err != nil {
			fmt.Printf("  Task %s: Process %d not running, marking as stopped\n", task.ID, pid)
			d.state.UpdateTaskStatus(task.ID, "stopped")
		} else {
			fmt.Printf("  Task %s: Process %d still running\n", task.ID, pid)
		}
	}
}

// Start begins the daemon, listening on the configured Unix socket.
// It blocks until the context is cancelled or an error occurs.
func (d *Daemon) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("daemon already running")
	}
	d.running = true
	d.ctx = ctx
	d.snapshots = NewSnapshotService(d)
	d.mu.Unlock()

	// Reconcile running VMs - update status for any that died while daemon was stopped
	d.reconcileRunningVMs()

	// Ensure base rootfs image is available for VM creation (Firecracker only — uses ZFS)
	if d.cfg.Backend == "" || d.cfg.Backend == "firecracker" {
		if err := d.ensureBaseImage(ctx); err != nil {
			return fmt.Errorf("failed to ensure base image: %w", err)
		}
	}

	// Start DHCP server (Firecracker backend only)
	if d.cfg.Backend == "" || d.cfg.Backend == "firecracker" {
		fmt.Println("Starting DHCP server...")
		if err := d.dhcp.Start(); err != nil {
			// Log warning but don't fail - dnsmasq might not be installed
			fmt.Printf("Warning: Failed to start DHCP server: %v\n", err)
			fmt.Println("VMs may not receive dynamic IPs. Ensure dnsmasq is installed.")
		}
	}

	socketDir := filepath.Dir(d.cfg.Daemon.SocketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove any existing socket file
	os.Remove(d.cfg.Daemon.SocketPath)

	listener, err := net.Listen("unix", d.cfg.Daemon.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	d.listener = listener

	// Make socket accessible to non-root users (requires write permission to connect)
	if err := os.Chmod(d.cfg.Daemon.SocketPath, 0666); err != nil {
		return fmt.Errorf("failed to set socket permissions: %w", err)
	}

	fmt.Printf("Daemon listening on %s\n", d.cfg.Daemon.SocketPath)

	grpcSrv := grpc.NewServer()
	grpcHandler := newGRPCServer(d)
	grpcHandler.Register(grpcSrv)
	d.grpcServer = grpcSrv

	go func() {
		if err := grpcSrv.Serve(listener); err != nil {
			fmt.Printf("gRPC server error: %v\n", err)
		}
	}()

	fmt.Printf("gRPC server started on %s\n", d.cfg.Daemon.SocketPath)

	// Start optional TCP listener for remote gRPC access
	if d.cfg.Daemon.GRPCAddr != "" {
		tcpListener, err := net.Listen("tcp", d.cfg.Daemon.GRPCAddr)
		if err != nil {
			d.listener.Close()
			return fmt.Errorf("failed to listen on TCP %s: %w", d.cfg.Daemon.GRPCAddr, err)
		}
		d.grpcListener = tcpListener
		fmt.Printf("gRPC server listening on %s\n", d.cfg.Daemon.GRPCAddr)

		go func() {
			if err := grpcSrv.Serve(tcpListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				fmt.Printf("gRPC TCP server error: %v\n", err)
			}
		}()
	}

	// Start HTTP server if enabled
	if d.cfg.HTTP.Enabled {
		// Create dashboard facade and adapter
		facade := NewDashboardFacade(d.state, d.tasks, d.zfs)
		adapter := dashboard.NewDaemonAdapter(facade)
		d.dashboardServer = dashboard.NewServer(adapter, d.cfg.VM.User)
		tsClient := tailscale.NewLocalClient()
		handler := dashboard.AuthMiddleware(d.dashboardServer, tsClient)

		// Create real-time components
		hub := d.dashboardServer.Hub()

		// Status broadcaster - wire to state callback
		d.statusBroadcaster = dashboard.NewStatusBroadcaster(hub)
		d.state.SetStatusChangeCallback(func(taskID, oldStatus, newStatus string) {
			d.statusBroadcaster.TaskStatusChanged(taskID, oldStatus, newStatus)
			// Note: VMFailed activity events are recorded in TaskManager.FailTask
			// with specific failure reasons, not here in the generic callback.
		})

		// Log streamer and tailer
		logStreamer := dashboard.NewLogStreamer(hub)
		d.logTailer = NewLogTailer(&dashboardLogSink{
			streamer:   logStreamer,
			logHistory: d.dashboardServer.LogHistory(),
		})

		// Metrics collector and poller (with alert checking)
		d.metricsCollector = dashboard.NewMetricsCollector(hub, d.dashboardServer.AlertChecker())
		// Only create metrics poller for Firecracker backend (uses FIFO)
		if d.cfg.Backend == "" || d.cfg.Backend == "firecracker" {
			d.metricsPoller = NewMetricsPoller(d, &dashboardMetricsSink{d.metricsCollector}, 5*time.Second)
			d.metricsPoller.Start()
		}

		// Host metrics collector and polling
		d.hostMetricsCollector = NewHostMetricsCollector()
		go d.pollHostMetrics()

		d.httpServer = &http.Server{
			Addr:    d.cfg.HTTP.Addr,
			Handler: handler,
		}
		go func() {
			fmt.Printf("Starting HTTP server on %s\n", d.cfg.HTTP.Addr)
			if err := d.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Printf("HTTP server error: %v\n", err)
			}
		}()
	}

	// Start snapshot service
	go func() {
		if err := d.snapshots.Start(ctx); err != nil {
			fmt.Printf("Snapshot service error: %v\n", err)
		}
	}()

	<-ctx.Done()
	return d.Stop()
}

// Stop gracefully shuts down the daemon.
func (d *Daemon) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return nil
	}

	d.running = false

	// Stop log tailer
	if d.logTailer != nil {
		d.logTailer.Stop()
	}

	// Stop metrics polling
	if d.metricsPoller != nil {
		d.metricsPoller.Stop()
	}

	// Close dashboard server
	if d.dashboardServer != nil {
		d.dashboardServer.Close()
	}

	// Shutdown HTTP server if running
	if d.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.httpServer.Shutdown(ctx)
	}

	if d.grpcServer != nil {
		d.grpcServer.GracefulStop()
	}

	if d.listener != nil {
		d.listener.Close()
	}

	if d.grpcListener != nil {
		d.grpcListener.Close()
	}

	// Stop DHCP server
	if d.dhcp != nil {
		d.dhcp.Stop()
	}

	if d.state != nil {
		d.state.Close()
	}

	return nil
}

// State returns the daemon's state manager.
func (d *Daemon) State() *State {
	return d.state
}

// ZFS returns the daemon's ZFS manager.
func (d *Daemon) ZFS() *zfs.Manager {
	return d.zfs
}

// Secrets returns the daemon's secrets provider.
func (d *Daemon) Secrets() secrets.Provider {
	return d.secrets
}

// Config returns the daemon's configuration.
func (d *Daemon) Config() *config.Config {
	return d.cfg
}

// Tasks returns the daemon's task manager.
func (d *Daemon) Tasks() *TaskManager {
	return d.tasks
}

// QueueManager returns the daemon's queue manager.
func (d *Daemon) QueueManager() *QueueManager {
	return d.queueManager
}

// SetTaskManager sets the daemon's task manager.
func (d *Daemon) SetTaskManager(tm *TaskManager) {
	d.tasks = tm
}

// DHCP returns the daemon's DHCP server.
func (d *Daemon) DHCP() *network.DHCPServer {
	return d.dhcp
}

// IPPool returns the daemon's IP pool for static VM IP allocation.
func (d *Daemon) IPPool() *network.IPPool {
	return d.ipPool
}

// RootfsProvisioner returns the daemon's rootfs provisioner, or nil if not configured.
func (d *Daemon) RootfsProvisioner() rootfs.Provisioner {
	return d.rootfsProvisioner
}

// ActivityFeed returns the activity feed for recording events.
// Returns nil if the dashboard is not enabled.
func (d *Daemon) ActivityFeed() *dashboard.ActivityFeed {
	if d.dashboardServer == nil {
		return nil
	}
	return d.dashboardServer.ActivityFeed()
}

// pollHostMetrics collects and broadcasts host metrics every 5 seconds.
func (d *Daemon) pollHostMetrics() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if d.hostMetricsCollector == nil || d.metricsCollector == nil {
				continue
			}
			metrics, err := d.hostMetricsCollector.Collect()
			if err != nil {
				continue
			}
			d.metricsCollector.SendHostMetrics(dashboard.HostMetrics{
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

// ensureBaseImage checks if the base rootfs snapshot exists and imports it if not.
func (d *Daemon) ensureBaseImage(ctx context.Context) error {
	// Construct the expected snapshot path: pool/ImagesPath/rootfs@base
	// e.g., tank/stockyard/images/rootfs@base
	snapshotPath := fmt.Sprintf("%s/%s/rootfs@base", d.cfg.ZFS.Pool, d.cfg.ZFS.ImagesPath)

	// Check if snapshot exists
	cmd := exec.CommandContext(ctx, "zfs", "list", "-t", "snapshot", snapshotPath)
	if err := cmd.Run(); err != nil {
		// Snapshot doesn't exist, import from configured rootfs
		fmt.Printf("Importing base rootfs image from %s...\n", d.cfg.Firecracker.RootfsPath)
		if err := d.zfs.ImportRootfsImage(ctx, d.cfg.ZFS.ImagesPath, d.cfg.Firecracker.RootfsPath); err != nil {
			return fmt.Errorf("failed to import base image: %w", err)
		}
		fmt.Println("Base image imported successfully")
	}
	return nil
}

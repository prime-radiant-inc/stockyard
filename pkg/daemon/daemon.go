package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/dashboard"
	"github.com/obra/stockyard/pkg/network"
	"github.com/obra/stockyard/pkg/secrets"
	"github.com/obra/stockyard/pkg/tailscale"
	"github.com/obra/stockyard/pkg/zfs"
)

// Daemon is the core daemon process that manages workspaces and tasks.
type Daemon struct {
	cfg       *config.Config
	secrets   secrets.Provider
	zfs       *zfs.Manager
	state     *State
	tasks     *TaskManager
	snapshots *SnapshotService
	dhcp      *network.DHCPServer

	listener   net.Listener
	grpcServer *grpc.Server
	httpServer *http.Server
	mu         sync.Mutex
	running    bool

	// Real-time dashboard components
	dashboardServer   *dashboard.Server
	metricsPoller     *MetricsPoller
	logTailer         *LogTailer
	statusBroadcaster *dashboard.StatusBroadcaster
}

// dashboardLogSink adapts LogStreamer to the LogSink interface.
type dashboardLogSink struct {
	streamer *dashboard.LogStreamer
}

func (s *dashboardLogSink) SendLog(taskID, stream, line string) {
	s.streamer.SendLog(taskID, stream, line)
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
	zfsMgr := zfs.NewManager(cfg.ZFS.Pool, cfg.ZFS.BasePath)

	state, err := NewState()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state: %w", err)
	}

	d := &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		zfs:     zfsMgr,
		state:   state,
	}

	// Initialize task manager with firecracker configuration
	var fcConfig *FirecrackerConfig
	if cfg.Firecracker.KernelPath != "" && cfg.Firecracker.RootfsPath != "" {
		fcConfig = &FirecrackerConfig{
			KernelPath: cfg.Firecracker.KernelPath,
			RootfsPath: cfg.Firecracker.RootfsPath,
			BridgeName: cfg.Firecracker.BridgeName,
			ImagesPath: cfg.ZFS.ImagesPath,
			VMsPath:    cfg.ZFS.VMsPath,
		}
	}
	d.tasks = NewTaskManager(d, fcConfig)

	// Initialize DHCP server
	dhcpConfig := network.DHCPConfig{
		Bridge:     cfg.Firecracker.BridgeName,
		Gateway:    cfg.Firecracker.VMGateway,
		RangeStart: cfg.Firecracker.DHCPRangeStart,
		RangeEnd:   cfg.Firecracker.DHCPRangeEnd,
		Netmask:    "255.255.192.0", // /18
		LeaseTime:  cfg.Firecracker.DHCPLeaseTime,
		DNS:        "8.8.8.8",
	}
	dataDir := "/var/lib/stockyard"
	dhcpServer, err := network.NewDHCPServer(dhcpConfig, dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create DHCP server: %w", err)
	}
	d.dhcp = dhcpServer

	return d, nil
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
	d.snapshots = NewSnapshotService(d)
	d.mu.Unlock()

	// Ensure base rootfs image is available for VM creation
	if err := d.ensureBaseImage(ctx); err != nil {
		return fmt.Errorf("failed to ensure base image: %w", err)
	}

	// Start DHCP server
	fmt.Println("Starting DHCP server...")
	if err := d.dhcp.Start(); err != nil {
		// Log warning but don't fail - dnsmasq might not be installed
		fmt.Printf("Warning: Failed to start DHCP server: %v\n", err)
		fmt.Println("VMs may not receive dynamic IPs. Ensure dnsmasq is installed.")
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

	// Start HTTP server if enabled
	if d.cfg.HTTP.Enabled {
		// Create dashboard facade and adapter
		facade := NewDashboardFacade(d.state, d.tasks, d.zfs)
		adapter := dashboard.NewDaemonAdapter(facade)
		d.dashboardServer = dashboard.NewServer(adapter)
		tsClient := tailscale.NewLocalClient()
		handler := dashboard.AuthMiddleware(d.dashboardServer, tsClient)

		// Create real-time components
		hub := d.dashboardServer.Hub()

		// Status broadcaster - wire to state callback
		d.statusBroadcaster = dashboard.NewStatusBroadcaster(hub)
		d.state.SetStatusChangeCallback(func(taskID, oldStatus, newStatus string) {
			d.statusBroadcaster.TaskStatusChanged(taskID, oldStatus, newStatus)
		})

		// Log streamer and tailer
		logStreamer := dashboard.NewLogStreamer(hub)
		d.logTailer = NewLogTailer(&dashboardLogSink{logStreamer})

		// Metrics collector and poller
		metricsCollector := dashboard.NewMetricsCollector(hub)
		d.metricsPoller = NewMetricsPoller(d, &dashboardMetricsSink{metricsCollector}, 5*time.Second)
		d.metricsPoller.Start()

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

// SetTaskManager sets the daemon's task manager.
func (d *Daemon) SetTaskManager(tm *TaskManager) {
	d.tasks = tm
}

// DHCP returns the daemon's DHCP server.
func (d *Daemon) DHCP() *network.DHCPServer {
	return d.dhcp
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

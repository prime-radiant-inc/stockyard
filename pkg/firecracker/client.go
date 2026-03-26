// Package firecracker provides direct Firecracker microVM management.
package firecracker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/obra/stockyard/pkg/zfs"
)

// Default paths and settings.
const (
	DefaultStateDir      = "/var/lib/stockyard/vms"
	DefaultFirecrackerBin = "/usr/local/bin/firecracker"
	DefaultBridgeName    = "flbr0"
)

// ClientConfig holds configuration for the Firecracker client.
type ClientConfig struct {
	StateDir       string
	FirecrackerBin string
	BridgeName     string
	KernelPath     string
	RootfsPath     string
	ImagesPath     string // ZFS dataset path for images (e.g., "stockyard/images")
	VMsPath        string // ZFS dataset path for VMs (e.g., "stockyard/vms")
}

// Client manages Firecracker microVMs.
type Client struct {
	config     ClientConfig
	zfs        *zfs.Manager
	network    *NetworkManager
	cidCounter uint32     // Next CID to allocate
	cidMu      sync.Mutex // Protects cidCounter
	mu         sync.Mutex
	procs      map[string]*exec.Cmd // vmID -> cmd, for reaping
}

// NewClient creates a new Firecracker client.
// The zfsMgr parameter can be nil if ZFS cloning is not needed.
func NewClient(cfg ClientConfig, zfsMgr *zfs.Manager) (*Client, error) {
	if cfg.StateDir == "" {
		cfg.StateDir = DefaultStateDir
	}
	if cfg.FirecrackerBin == "" {
		cfg.FirecrackerBin = DefaultFirecrackerBin
	}
	if cfg.BridgeName == "" {
		cfg.BridgeName = DefaultBridgeName
	}

	// Ensure state directory exists
	if err := os.MkdirAll(cfg.StateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	return &Client{
		config:     cfg,
		zfs:        zfsMgr,
		network:    NewNetworkManager(cfg.BridgeName),
		cidCounter: 100, // Start CIDs at 100 (3-99 reserved)
		procs:      make(map[string]*exec.Cmd),
	}, nil
}

// Close cleans up resources by killing and reaping all tracked firecracker processes.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, cmd := range c.procs {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		delete(c.procs, id)
	}
	return nil
}

// allocateCID returns the next available CID for a new VM.
func (c *Client) allocateCID() uint32 {
	c.cidMu.Lock()
	defer c.cidMu.Unlock()
	cid := c.cidCounter
	c.cidCounter++
	return cid
}

// CreateVM creates and starts a new Firecracker microVM using the API mode.
func (c *Client) CreateVM(ctx context.Context, config *VMConfig) (*VMInfo, error) {
	// Fill in defaults from client config before validation
	if config.KernelPath == "" {
		config.KernelPath = c.config.KernelPath
	}
	if config.RootfsPath == "" {
		config.RootfsPath = c.config.RootfsPath
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid VM config: %w", err)
	}

	namespace := config.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Create VM state directory
	vmDir := filepath.Join(c.config.StateDir, namespace, config.ID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create VM directory: %w", err)
	}

	// Create tap device
	tapName := TapNameForVM(config.ID)
	if err := c.network.DeleteTap(tapName); err != nil {
		// Ignore errors from non-existent tap
	}
	if err := c.network.CreateTap(tapName); err != nil {
		return nil, fmt.Errorf("failed to create tap device: %w", err)
	}

	// Generate MAC address
	macAddr := GenerateMAC()

	// Use paths from config (already filled in with defaults)
	kernelPath := config.KernelPath
	rootfsPath := config.RootfsPath

	// Create rootfs for this VM (each VM needs its own writable copy)
	var vmRootfs string
	var vmDatasetPath string // Track ZFS dataset for cleanup on failure
	if c.zfs != nil {
		// Use ZFS clone for copy-on-write rootfs
		// Full snapshot path: tank/stockyard/images/rootfs@base
		snapshotPath := fmt.Sprintf("%s/%s/rootfs@base", c.zfs.PoolName, c.config.ImagesPath)
		// Full clone target: tank/stockyard/vms/<vmID>
		vmDatasetPath = fmt.Sprintf("%s/%s/%s", c.zfs.PoolName, c.config.VMsPath, config.ID)

		// Ensure parent dataset exists (zfs clone doesn't create parents like zfs create -p)
		parentDataset := fmt.Sprintf("%s/%s", c.zfs.PoolName, c.config.VMsPath)
		exec.CommandContext(ctx, "zfs", "create", "-p", parentDataset).Run() // Ignore error - may already exist

		// Clone: zfs clone <snapshot> <target>
		cmd := exec.CommandContext(ctx, "zfs", "clone", snapshotPath, vmDatasetPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			c.network.DeleteTap(tapName)
			return nil, fmt.Errorf("failed to clone rootfs: %w: %s", err, string(output))
		}

		// Get mountpoint: zfs get -H -o value mountpoint <dataset>
		cmd = exec.CommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", vmDatasetPath)
		output, err := cmd.Output()
		if err != nil {
			destroyZFSDataset(vmDatasetPath)
			c.network.DeleteTap(tapName)
			return nil, fmt.Errorf("failed to get clone mountpoint: %w", err)
		}
		mountpoint := strings.TrimSpace(string(output))
		vmRootfs = filepath.Join(mountpoint, "rootfs.ext4")
	} else {
		// Fallback to file copy if no ZFS manager
		vmRootfs = filepath.Join(vmDir, "rootfs.ext4")
		if _, err := os.Stat(vmRootfs); os.IsNotExist(err) {
			if err := copyFile(rootfsPath, vmRootfs); err != nil {
				c.network.DeleteTap(tapName)
				return nil, fmt.Errorf("failed to copy rootfs: %w", err)
			}
		}
	}

	// Save tap name and MAC for cleanup
	os.WriteFile(filepath.Join(vmDir, "tap_name"), []byte(tapName), 0644)
	os.WriteFile(filepath.Join(vmDir, "mac_addr"), []byte(macAddr), 0644)

	// Start Firecracker with API socket
	// NOTE: We use exec.Command (not CommandContext) because the firecracker
	// process should run independently of the request context. If we used
	// CommandContext, the process would be killed when the gRPC request completes.
	apiSocketPath := filepath.Join(vmDir, "api.sock")
	stdoutLog, _ := os.Create(filepath.Join(vmDir, "stdout.log"))
	stderrLog, _ := os.Create(filepath.Join(vmDir, "stderr.log"))

	cmd := exec.Command(c.config.FirecrackerBin,
		"--api-sock", apiSocketPath,
	)
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Start in new process group
	}

	if err := cmd.Start(); err != nil {
		stdoutLog.Close()
		stderrLog.Close()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("failed to start firecracker: %w", err)
	}

	stdoutLog.Close()
	stderrLog.Close()

	// Save PID and API socket path
	pidFile := filepath.Join(vmDir, "firecracker.pid")
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	os.WriteFile(filepath.Join(vmDir, "api.sock.path"), []byte(apiSocketPath), 0644)

	// Track process for reaping
	c.mu.Lock()
	c.procs[config.ID] = cmd
	c.mu.Unlock()

	// Reaper goroutine — wait for process exit to avoid zombies
	go func() {
		cmd.Wait() // blocks until firecracker exits; reaps the zombie
		c.mu.Lock()
		delete(c.procs, config.ID)
		c.mu.Unlock()
	}()

	// Brief check to catch immediate failures (bad binary, permissions, etc.)
	time.Sleep(50 * time.Millisecond)
	if !processRunning(cmd.Process.Pid) {
		stderrContent, _ := os.ReadFile(filepath.Join(vmDir, "stderr.log"))
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("firecracker exited immediately: %s", string(stderrContent))
	}

	// Wait for API socket to become available
	apiClient := NewAPIClient(apiSocketPath)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := apiClient.WaitForSocket(waitCtx); err != nil {
		cmd.Process.Kill()
		// reaper goroutine will call cmd.Wait()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("wait for API socket: %w", err)
	}

	// Configure via API
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
	if config.StaticIPArgs != "" {
		bootArgs += " " + config.StaticIPArgs
	}
	if err := apiClient.SetBootSource(ctx, kernelPath, bootArgs); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set boot source: %w", err)
	}

	if err := apiClient.SetDrive(ctx, "rootfs", vmRootfs, true, false); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set drive: %w", err)
	}

	if err := apiClient.SetNetworkInterface(ctx, "eth0", macAddr, tapName); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set network interface: %w", err)
	}

	if err := apiClient.SetMachineConfig(ctx, config.VCPU, config.MemoryMB); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set machine config: %w", err)
	}

	// Allocate CID and configure vsock for terminal access
	cid := c.allocateCID()
	vsockPath := filepath.Join(vmDir, "vsock.sock")
	if err := apiClient.SetVsock(ctx, cid, vsockPath); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set vsock: %w", err)
	}

	// Configure MMDS for cloud-init
	if err := apiClient.SetMMDSConfig(ctx, []string{"eth0"}); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set MMDS config: %w", err)
	}

	hostname := fmt.Sprintf("stockyard-%s", config.ID)
	mmdsData := BuildMMDSData(MMDSMetadata{
		InstanceID:        "i-" + config.ID,
		Hostname:          hostname,
		TailscaleAuthKey:  config.TailscaleAuthKey,
		SSHAuthorizedKeys: config.SSHAuthorizedKeys,
		UserData:          config.CloudInitData,
		NetworkConfig:     config.NetworkMMDS,
		TailscaleState:    config.TailscaleState,
		DotEnv:            config.DotEnv,
	})
	if err := apiClient.SetMMDSData(ctx, mmdsData); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set MMDS data: %w", err)
	}

	// Create metrics FIFO
	metricsPath := filepath.Join(vmDir, "metrics.fifo")
	if err := syscall.Mkfifo(metricsPath, 0644); err != nil && !os.IsExist(err) {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("create metrics fifo: %w", err)
	}

	// Open FIFO for reading first (in background) - Firecracker needs a reader
	// before it can open the FIFO for writing. We use O_RDWR to avoid blocking.
	fifoFile, err := os.OpenFile(metricsPath, os.O_RDWR, 0)
	if err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("open metrics fifo: %w", err)
	}
	// Close after SetMetrics succeeds - the actual reader will re-open it
	defer fifoFile.Close()

	// Configure metrics before starting instance
	if err := apiClient.SetMetrics(ctx, metricsPath); err != nil {
		// Log warning but don't fail - metrics are optional
		fmt.Printf("Warning: failed to configure metrics: %v\n", err)
	}

	// Start instance
	if err := apiClient.StartInstance(ctx); err != nil {
		cmd.Process.Kill()
		destroyZFSDataset(vmDatasetPath)
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("start instance: %w", err)
	}

	return &VMInfo{
		ID:            config.ID,
		Namespace:     namespace,
		PID:           cmd.Process.Pid,
		APISocketPath: apiSocketPath,
		RootfsPath:    vmRootfs,
		MetricsPath:   metricsPath,
		CID:           cid,
		VsockPath:     vsockPath,
		State:         "running",
		CreatedAt:     time.Now(),
	}, nil
}

// GetVM retrieves information about a VM.
func (c *Client) GetVM(ctx context.Context, namespace, id string) (*VM, error) {
	if namespace == "" {
		namespace = "default"
	}

	vmDir := filepath.Join(c.config.StateDir, namespace, id)
	if _, err := os.Stat(vmDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("VM not found: %s/%s", namespace, id)
	}

	vm := &VM{
		ID:        id,
		Namespace: namespace,
		StateDir:  vmDir,
		Status:    VMStatusStopped,
	}

	// Read tap name
	if data, err := os.ReadFile(filepath.Join(vmDir, "tap_name")); err == nil {
		vm.TapDevice = string(data)
	}

	// Read MAC address
	if data, err := os.ReadFile(filepath.Join(vmDir, "mac_addr")); err == nil {
		vm.MAC = string(data)
	}

	// Check if running
	if data, err := os.ReadFile(filepath.Join(vmDir, "firecracker.pid")); err == nil {
		if pid, err := strconv.Atoi(string(data)); err == nil {
			vm.PID = pid
			if processRunning(pid) {
				vm.Status = VMStatusRunning
			}
		}
	}

	return vm, nil
}

// DeleteVM stops and removes a VM.
func (c *Client) DeleteVM(ctx context.Context, namespace, id string) error {
	if namespace == "" {
		namespace = "default"
	}

	vmDir := filepath.Join(c.config.StateDir, namespace, id)
	if _, err := os.Stat(vmDir); os.IsNotExist(err) {
		return nil // Already gone
	}

	// Stop if running — prefer tracked process for clean reaping
	c.mu.Lock()
	trackedCmd, tracked := c.procs[id]
	c.mu.Unlock()

	if tracked {
		trackedCmd.Process.Signal(syscall.SIGTERM)
		// Give it a second to exit gracefully
		done := make(chan struct{})
		go func() {
			trackedCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			// reaped
		case <-time.After(time.Second):
			trackedCmd.Process.Kill()
			<-done // wait for reaper
		}
		c.mu.Lock()
		delete(c.procs, id)
		c.mu.Unlock()
	} else if data, err := os.ReadFile(filepath.Join(vmDir, "firecracker.pid")); err == nil {
		// Fallback for processes we don't track (e.g., after daemon restart).
		// These are reparented to init, which will reap them.
		if pid, err := strconv.Atoi(string(data)); err == nil && processRunning(pid) {
			syscall.Kill(pid, syscall.SIGTERM)
			time.Sleep(time.Second)
			if processRunning(pid) {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}

	// Clean up tap device
	if data, err := os.ReadFile(filepath.Join(vmDir, "tap_name")); err == nil {
		c.network.DeleteTap(string(data))
	}

	// Destroy ZFS clone dataset if using ZFS
	if c.zfs != nil {
		vmDatasetPath := fmt.Sprintf("%s/%s/%s", c.zfs.PoolName, c.config.VMsPath, id)
		destroyZFSDataset(vmDatasetPath)
	}

	// Remove state directory
	if err := os.RemoveAll(vmDir); err != nil {
		return fmt.Errorf("failed to remove VM directory: %w", err)
	}

	return nil
}

// ListVMs returns all VMs in a namespace.
func (c *Client) ListVMs(ctx context.Context, namespace string) ([]*VM, error) {
	if namespace == "" {
		namespace = "default"
	}

	nsDir := filepath.Join(c.config.StateDir, namespace)
	entries, err := os.ReadDir(nsDir)
	if os.IsNotExist(err) {
		return []*VM{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var vms []*VM
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		vm, err := c.GetVM(ctx, namespace, entry.Name())
		if err == nil {
			vms = append(vms, vm)
		}
	}

	return vms, nil
}

// StartVM starts a stopped VM using its existing rootfs.
// This is used for restarting VMs that were stopped but not destroyed.
func (c *Client) StartVM(ctx context.Context, config *VMConfig) (*VMInfo, error) {
	// Fill in defaults from client config before validation
	if config.KernelPath == "" {
		config.KernelPath = c.config.KernelPath
	}

	namespace := config.Namespace
	if namespace == "" {
		namespace = "default"
	}

	vmDir := filepath.Join(c.config.StateDir, namespace, config.ID)

	// Get existing rootfs path from ZFS dataset
	var vmRootfs string
	if c.zfs != nil {
		vmDatasetPath := fmt.Sprintf("%s/%s/%s", c.zfs.PoolName, c.config.VMsPath, config.ID)
		cmd := exec.CommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", vmDatasetPath)
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to get VM rootfs mountpoint: %w", err)
		}
		mountpoint := strings.TrimSpace(string(output))
		vmRootfs = filepath.Join(mountpoint, "rootfs.ext4")
	} else {
		vmRootfs = filepath.Join(vmDir, "rootfs.ext4")
	}

	// Verify rootfs exists
	if _, err := os.Stat(vmRootfs); err != nil {
		return nil, fmt.Errorf("VM rootfs not found at %s: %w", vmRootfs, err)
	}

	// Set the rootfs path in config for validation
	config.RootfsPath = vmRootfs

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid VM config: %w", err)
	}

	// Create tap device
	tapName := TapNameForVM(config.ID)
	if err := c.network.DeleteTap(tapName); err != nil {
		// Ignore errors from non-existent tap
	}
	if err := c.network.CreateTap(tapName); err != nil {
		return nil, fmt.Errorf("failed to create tap device: %w", err)
	}

	// Read existing MAC address or generate new one
	macAddr := ""
	if data, err := os.ReadFile(filepath.Join(vmDir, "mac_addr")); err == nil {
		macAddr = strings.TrimSpace(string(data))
	}
	if macAddr == "" {
		macAddr = GenerateMAC()
		os.WriteFile(filepath.Join(vmDir, "mac_addr"), []byte(macAddr), 0644)
	}

	// Save tap name
	os.WriteFile(filepath.Join(vmDir, "tap_name"), []byte(tapName), 0644)

	// Start Firecracker with API socket
	apiSocketPath := filepath.Join(vmDir, "api.sock")
	// Remove old socket if it exists
	os.Remove(apiSocketPath)

	stdoutLog, _ := os.Create(filepath.Join(vmDir, "stdout.log"))
	stderrLog, _ := os.Create(filepath.Join(vmDir, "stderr.log"))

	cmd := exec.Command(c.config.FirecrackerBin,
		"--api-sock", apiSocketPath,
	)
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		stdoutLog.Close()
		stderrLog.Close()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("failed to start firecracker: %w", err)
	}

	stdoutLog.Close()
	stderrLog.Close()

	// Save PID
	pidFile := filepath.Join(vmDir, "firecracker.pid")
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	os.WriteFile(filepath.Join(vmDir, "api.sock.path"), []byte(apiSocketPath), 0644)

	// Track process for reaping
	c.mu.Lock()
	c.procs[config.ID] = cmd
	c.mu.Unlock()

	// Reaper goroutine — wait for process exit to avoid zombies
	go func() {
		cmd.Wait() // blocks until firecracker exits; reaps the zombie
		c.mu.Lock()
		delete(c.procs, config.ID)
		c.mu.Unlock()
	}()

	// Brief check to catch immediate failures (bad binary, permissions, etc.)
	time.Sleep(50 * time.Millisecond)
	if !processRunning(cmd.Process.Pid) {
		stderrContent, _ := os.ReadFile(filepath.Join(vmDir, "stderr.log"))
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("firecracker exited immediately: %s", string(stderrContent))
	}

	// Wait for API socket to become available
	apiClient := NewAPIClient(apiSocketPath)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := apiClient.WaitForSocket(waitCtx); err != nil {
		cmd.Process.Kill()
		// reaper goroutine will call cmd.Wait()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("wait for API socket: %w", err)
	}

	// Configure via API
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
	if config.StaticIPArgs != "" {
		bootArgs += " " + config.StaticIPArgs
	}
	if err := apiClient.SetBootSource(ctx, config.KernelPath, bootArgs); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set boot source: %w", err)
	}

	if err := apiClient.SetDrive(ctx, "rootfs", vmRootfs, true, false); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set drive: %w", err)
	}

	if err := apiClient.SetNetworkInterface(ctx, "eth0", macAddr, tapName); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set network interface: %w", err)
	}

	if err := apiClient.SetMachineConfig(ctx, config.VCPU, config.MemoryMB); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set machine config: %w", err)
	}

	// Allocate CID and configure vsock for terminal access
	cid := c.allocateCID()
	vsockPath := filepath.Join(vmDir, "vsock.sock")
	if err := apiClient.SetVsock(ctx, cid, vsockPath); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set vsock: %w", err)
	}

	// Configure MMDS for cloud-init
	if err := apiClient.SetMMDSConfig(ctx, []string{"eth0"}); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set MMDS config: %w", err)
	}

	hostname := fmt.Sprintf("stockyard-%s", config.ID)
	mmdsData := BuildMMDSData(MMDSMetadata{
		InstanceID:        "i-" + config.ID,
		Hostname:          hostname,
		TailscaleAuthKey:  config.TailscaleAuthKey,
		SSHAuthorizedKeys: config.SSHAuthorizedKeys,
		UserData:          config.CloudInitData,
		NetworkConfig:     config.NetworkMMDS,
		TailscaleState:    config.TailscaleState,
		DotEnv:            config.DotEnv,
	})
	if err := apiClient.SetMMDSData(ctx, mmdsData); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("set MMDS data: %w", err)
	}

	// Create metrics FIFO
	metricsPath := filepath.Join(vmDir, "metrics.fifo")
	if err := syscall.Mkfifo(metricsPath, 0644); err != nil && !os.IsExist(err) {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("create metrics fifo: %w", err)
	}

	// Open FIFO for reading first (in background) - Firecracker needs a reader
	// before it can open the FIFO for writing. We use O_RDWR to avoid blocking.
	fifoFile, err := os.OpenFile(metricsPath, os.O_RDWR, 0)
	if err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("open metrics fifo: %w", err)
	}
	// Close after SetMetrics succeeds - the actual reader will re-open it
	defer fifoFile.Close()

	// Configure metrics before starting instance
	if err := apiClient.SetMetrics(ctx, metricsPath); err != nil {
		// Log warning but don't fail - metrics are optional
		fmt.Printf("Warning: failed to configure metrics: %v\n", err)
	}

	// Start instance
	if err := apiClient.StartInstance(ctx); err != nil {
		cmd.Process.Kill()
		c.network.DeleteTap(tapName)
		return nil, fmt.Errorf("start instance: %w", err)
	}

	return &VMInfo{
		ID:            config.ID,
		Namespace:     namespace,
		PID:           cmd.Process.Pid,
		APISocketPath: apiSocketPath,
		RootfsPath:    vmRootfs,
		MetricsPath:   metricsPath,
		CID:           cid,
		VsockPath:     vsockPath,
		State:         "running",
		CreatedAt:     time.Now(),
	}, nil
}

// StopVM stops a running VM without deleting it.
func (c *Client) StopVM(ctx context.Context, namespace, id string) error {
	vm, err := c.GetVM(ctx, namespace, id)
	if err != nil {
		return err
	}

	if vm.Status != VMStatusRunning {
		return nil // Already stopped
	}

	vmDir := filepath.Join(c.config.StateDir, namespace, id)

	// Try graceful shutdown via API if socket exists
	apiSocketPath := filepath.Join(vmDir, "api.sock")
	if _, err := os.Stat(apiSocketPath); err == nil {
		apiClient := NewAPIClient(apiSocketPath)
		if err := apiClient.SendCtrlAltDel(ctx); err == nil {
			// Wait briefly for graceful shutdown
			time.Sleep(2 * time.Second)
		}
	}

	// Stop process — prefer tracked process for clean reaping
	c.mu.Lock()
	trackedCmd, tracked := c.procs[id]
	c.mu.Unlock()

	if tracked {
		trackedCmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			trackedCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			// reaped
		case <-time.After(2 * time.Second):
			trackedCmd.Process.Kill()
			<-done // wait for reaper
		}
		c.mu.Lock()
		delete(c.procs, id)
		c.mu.Unlock()
	} else if processRunning(vm.PID) {
		// Fallback for processes we don't track (e.g., after daemon restart).
		// These are reparented to init, which will reap them.
		syscall.Kill(vm.PID, syscall.SIGTERM)
		time.Sleep(time.Second)
		if processRunning(vm.PID) {
			syscall.Kill(vm.PID, syscall.SIGKILL)
		}
	}

	// Clean up tap device
	if vm.TapDevice != "" {
		c.network.DeleteTap(vm.TapDevice)
	}

	// Remove PID file
	os.Remove(filepath.Join(vmDir, "firecracker.pid"))

	return nil
}

// processRunning checks if a process is still running.
func processRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

// destroyZFSDataset destroys a ZFS dataset if the path is non-empty.
// Errors are ignored since this is best-effort cleanup.
func destroyZFSDataset(datasetPath string) {
	if datasetPath != "" {
		exec.Command("zfs", "destroy", "-r", datasetPath).Run()
	}
}

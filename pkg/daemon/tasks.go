package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/obra/stockyard/pkg/firecracker"
	"github.com/obra/stockyard/pkg/tailscale"
)

// TaskManager handles the lifecycle of VM-based tasks.
type TaskManager struct {
	daemon *Daemon
	fc     *firecracker.Client
}

// FirecrackerConfig holds configuration for direct Firecracker VM management.
type FirecrackerConfig struct {
	KernelPath string
	RootfsPath string
	BridgeName string
	ImagesPath string // ZFS dataset path for images
	VMsPath    string // ZFS dataset path for VMs
}

// NewTaskManager creates a TaskManager with the given daemon and firecracker configuration.
func NewTaskManager(d *Daemon, fcConfig *FirecrackerConfig) *TaskManager {
	tm := &TaskManager{
		daemon: d,
	}

	if fcConfig != nil {
		cfg := firecracker.ClientConfig{
			KernelPath: fcConfig.KernelPath,
			RootfsPath: fcConfig.RootfsPath,
			BridgeName: fcConfig.BridgeName,
			ImagesPath: fcConfig.ImagesPath,
			VMsPath:    fcConfig.VMsPath,
		}
		client, err := firecracker.NewClient(cfg, d.zfs)
		if err != nil {
			fmt.Printf("Warning: failed to create firecracker client: %v\n", err)
		} else {
			tm.fc = client
		}
	}

	return tm
}

// CreateTaskRequest contains the parameters for creating a new task.
type CreateTaskRequest struct {
	Repo             string
	Ref              string
	Name             string
	Command          []string
	Env              map[string]string
	CPUs             int32
	MemoryMB         int32
	NoTailscale       bool
	TailscaleAuthKey  string   // Optional: overrides 1Password lookup
	SSHAuthorizedKeys []string // SSH public keys for VM access
}

// CreateTask creates a new VM-based task with the given parameters.
func (tm *TaskManager) CreateTask(ctx context.Context, req *CreateTaskRequest) (*Task, error) {
	if req.Repo == "" {
		return nil, fmt.Errorf("repo is required")
	}

	// Apply defaults
	if req.Ref == "" {
		req.Ref = "main"
	}
	if req.CPUs <= 0 {
		req.CPUs = 2
	}
	if req.MemoryMB <= 0 {
		req.MemoryMB = 1024
	}

	// Generate task ID
	taskID := firecracker.GenerateVMID()

	// Create ZFS dataset for workspace
	if err := tm.daemon.zfs.CreateDataset(ctx, taskID); err != nil {
		return nil, fmt.Errorf("failed to create ZFS dataset: %w", err)
	}

	// Get workspace mountpoint
	workspacePath, err := tm.daemon.zfs.GetMountpoint(ctx, taskID)
	if err != nil {
		// Clean up dataset on failure
		tm.daemon.zfs.DestroyDataset(ctx, taskID)
		return nil, fmt.Errorf("failed to get workspace mountpoint: %w", err)
	}

	// Build environment with secrets
	env := make(map[string]string)
	for k, v := range req.Env {
		env[k] = v
	}

	// Fetch secrets from provider
	secretNames := []string{"anthropic-api-key", "github-token"}
	for _, secretName := range secretNames {
		if secret, err := tm.daemon.secrets.GetSecret(ctx, secretName); err == nil {
			envKey := strings.ToUpper(strings.ReplaceAll(secretName, "-", "_"))
			env[envKey] = secret
		}
	}

	// Get Tailscale auth key if enabled
	var tailscaleAuthKey string
	var tailscaleHostname string
	if !req.NoTailscale {
		var key string
		if req.TailscaleAuthKey != "" {
			// Use provided auth key
			key = req.TailscaleAuthKey
		} else {
			// Fetch from secrets provider
			var err error
			key, err = tm.daemon.secrets.GetSecret(ctx, "tailscale-auth-key")
			if err != nil {
				log.Printf("Warning: could not get Tailscale auth key: %v", err)
				// Continue without Tailscale
			}
		}
		if key != "" {
			if err := tailscale.ValidateAuthKey(key); err != nil {
				log.Printf("Warning: invalid Tailscale auth key: %v", err)
				// Continue without Tailscale
			} else {
				tailscaleAuthKey = key
				tailscaleHostname = tailscale.BuildHostname(taskID)
			}
		}
	}

	// Generate hostname
	hostname := fmt.Sprintf("stockyard-%s", taskID)

	// Generate cloud-init config
	cloudInitCfg := &firecracker.CloudInitConfig{
		Hostname:          hostname,
		Environment:       env,
		TailscaleAuthKey:  tailscaleAuthKey,
		TailscaleHostname: tailscaleHostname,
		WorkspacePath:     workspacePath,
	}

	cloudInitData, err := cloudInitCfg.Generate()
	if err != nil {
		tm.daemon.zfs.DestroyDataset(ctx, taskID)
		return nil, fmt.Errorf("failed to generate cloud-init config: %w", err)
	}

	// Create VM if firecracker client is available
	var vmID string
	var vmMetricsPath string
	if tm.fc != nil {
		vmCfg := &firecracker.VMConfig{
			ID:                taskID,
			Namespace:         "stockyard",
			VCPU:              req.CPUs,
			MemoryMB:          req.MemoryMB,
			CloudInitData:     cloudInitData,
			TailscaleAuthKey:  tailscaleAuthKey,
			SSHAuthorizedKeys: req.SSHAuthorizedKeys,
			Metadata: map[string]string{
				"task-id":   taskID,
				"task-name": req.Name,
				"repo":      req.Repo,
				"ref":       req.Ref,
			},
		}

		vm, err := tm.fc.CreateVM(ctx, vmCfg)
		if err != nil {
			tm.daemon.zfs.DestroyDataset(ctx, taskID)
			return nil, fmt.Errorf("failed to create VM: %w", err)
		}
		vmID = vm.ID
		vmMetricsPath = vm.MetricsPath
	}

	// Determine command string for storage
	commandStr := ""
	if len(req.Command) > 0 {
		commandStr = strings.Join(req.Command, " ")
	}

	// Record task in database
	task := &Task{
		ID:                taskID,
		Name:              req.Name,
		Repo:              req.Repo,
		Ref:               req.Ref,
		Command:           commandStr,
		Status:            "running",
		VMID:              vmID,
		TailscaleHostname: tailscaleHostname,
		CreatedAt:         time.Now(),
	}

	if err := tm.daemon.state.CreateTask(task); err != nil {
		// Attempt cleanup on failure
		if tm.fc != nil && vmID != "" {
			tm.fc.DeleteVM(ctx, "stockyard", vmID)
		}
		tm.daemon.zfs.DestroyDataset(ctx, taskID)
		return nil, fmt.Errorf("failed to record task: %w", err)
	}

	// Record activity event for VM started
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMStarted(taskID, req.Name, req.Repo, "")
	}

	// Start log tailing if dashboard is enabled
	if tm.daemon.logTailer != nil && vmID != "" {
		vmDir := filepath.Join(tm.daemon.cfg.ZFS.VMsPath, vmID)
		tm.daemon.logTailer.TailFile(taskID, "stdout", filepath.Join(vmDir, "stdout.log"))
		tm.daemon.logTailer.TailFile(taskID, "stderr", filepath.Join(vmDir, "stderr.log"))
	}

	// Start metrics collection if dashboard is enabled
	if tm.daemon.metricsPoller != nil && vmMetricsPath != "" {
		memoryBytes := int64(req.MemoryMB) * 1024 * 1024
		tm.daemon.metricsPoller.StartTaskMetrics(taskID, vmMetricsPath, memoryBytes)
	}

	return task, nil
}

// RestartTask restarts a stopped task by starting its VM again.
func (tm *TaskManager) RestartTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	if task.Status != "stopped" && task.Status != "failed" {
		return fmt.Errorf("task %s is not stopped (status: %s)", taskID, task.Status)
	}

	// Update status to starting
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "starting"); err != nil {
		return err
	}

	// Start VM using existing workspace and rootfs
	var vmInfo *firecracker.VMInfo
	if tm.fc != nil && task.VMID != "" {
		// Build minimal config for restarting - use defaults for CPU/memory
		vmCfg := &firecracker.VMConfig{
			ID:        task.VMID,
			Namespace: "stockyard",
			VCPU:      2,      // Default
			MemoryMB:  1024,   // Default
		}

		var err error
		vmInfo, err = tm.fc.StartVM(ctx, vmCfg)
		if err != nil {
			// Revert status on failure
			tm.daemon.state.UpdateTaskStatus(taskID, "failed")
			return fmt.Errorf("failed to start VM: %w", err)
		}
	}

	// Update status to running
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "running"); err != nil {
		return err
	}

	// Start log tailing if dashboard is enabled
	if tm.daemon.logTailer != nil && task.VMID != "" {
		vmDir := filepath.Join(tm.daemon.cfg.ZFS.VMsPath, task.VMID)
		tm.daemon.logTailer.TailFile(taskID, "stdout", filepath.Join(vmDir, "stdout.log"))
		tm.daemon.logTailer.TailFile(taskID, "stderr", filepath.Join(vmDir, "stderr.log"))
	}

	// Start metrics collection if dashboard is enabled
	if tm.daemon.metricsPoller != nil && vmInfo != nil && vmInfo.MetricsPath != "" {
		// Use default memory (1024MB) since we don't store it in the task
		memoryBytes := int64(1024) * 1024 * 1024
		tm.daemon.metricsPoller.StartTaskMetrics(taskID, vmInfo.MetricsPath, memoryBytes)
	}

	// Record activity event for VM started
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMStarted(taskID, task.Name, task.Repo, "")
	}

	return nil
}

// StopTask stops a running task by its ID.
func (tm *TaskManager) StopTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	// Stop log tailing
	if tm.daemon.logTailer != nil {
		tm.daemon.logTailer.StopTask(taskID)
	}

	// Stop metrics collection
	if tm.daemon.metricsPoller != nil {
		tm.daemon.metricsPoller.StopTaskMetrics(taskID)
	}

	// Stop VM if firecracker client is available and task has a VM
	if tm.fc != nil && task.VMID != "" {
		if err := tm.fc.StopVM(ctx, "stockyard", task.VMID); err != nil {
			fmt.Printf("Warning: failed to stop VM %s: %v\n", task.VMID, err)
		}
	}

	// Update task status
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "stopped"); err != nil {
		return err
	}

	// Record activity event for VM stopped
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMStopped(taskID, task.Name)
	}

	return nil
}

// FailTask marks a task as failed with a reason.
// This is called when a VM crashes or becomes unresponsive.
func (tm *TaskManager) FailTask(ctx context.Context, taskID string, reason string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	// Stop log tailing
	if tm.daemon.logTailer != nil {
		tm.daemon.logTailer.StopTask(taskID)
	}

	// Stop metrics collection
	if tm.daemon.metricsPoller != nil {
		tm.daemon.metricsPoller.StopTaskMetrics(taskID)
	}

	// Update task status to failed
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "failed"); err != nil {
		return err
	}

	// Record activity event for VM failed with specific reason
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMFailed(taskID, task.Name, reason)
	}

	return nil
}

// DestroyTask destroys a task and its associated resources.
func (tm *TaskManager) DestroyTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	// Stop log tailing
	if tm.daemon.logTailer != nil {
		tm.daemon.logTailer.StopTask(taskID)
	}

	// Stop metrics collection
	if tm.daemon.metricsPoller != nil {
		tm.daemon.metricsPoller.StopTaskMetrics(taskID)
	}

	// Delete VM if firecracker client is available and task has a VM
	if tm.fc != nil && task.VMID != "" {
		if err := tm.fc.DeleteVM(ctx, "stockyard", task.VMID); err != nil {
			fmt.Printf("Warning: failed to delete VM %s: %v\n", task.VMID, err)
		}
	}

	// Destroy ZFS dataset
	if err := tm.daemon.zfs.DestroyDataset(ctx, taskID); err != nil {
		fmt.Printf("Warning: failed to destroy ZFS dataset for %s: %v\n", taskID, err)
	}

	// Record activity event for VM stopped (if it was running)
	if task.Status == "running" {
		if af := tm.daemon.ActivityFeed(); af != nil {
			af.VMStopped(taskID, task.Name)
		}
	}

	// Delete task from database
	return tm.daemon.state.DeleteTask(taskID)
}

// Close closes the task manager and releases resources.
func (tm *TaskManager) Close() error {
	if tm.fc != nil {
		return tm.fc.Close()
	}
	return nil
}

// GetVMMAC reads the MAC address for a VM from its state directory.
func (tm *TaskManager) GetVMMAC(namespace, vmID string) (string, error) {
	macPath := filepath.Join(tm.daemon.cfg.Daemon.DataDir, "vms", namespace, vmID, "mac_addr")
	data, err := os.ReadFile(macPath)
	if err != nil {
		return "", fmt.Errorf("failed to read MAC address: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// GetVMIP looks up a VM's IP address via DHCP leases.
func (tm *TaskManager) GetVMIP(namespace, vmID string) (string, error) {
	mac, err := tm.GetVMMAC(namespace, vmID)
	if err != nil {
		return "", err
	}

	if tm.daemon.DHCP() == nil {
		return "", fmt.Errorf("DHCP server not available")
	}

	ip, found := tm.daemon.DHCP().GetIPForMAC(mac)
	if !found {
		return "", fmt.Errorf("no DHCP lease found for MAC %s", mac)
	}
	return ip, nil
}

// parseMemory parses a memory string like "512m", "2g", "2GB" into megabytes.
// Returns 1024 (1GB) as default if the string is empty or invalid.
func parseMemory(s string) int32 {
	if s == "" {
		return 1024
	}

	s = strings.TrimSpace(strings.ToLower(s))

	// Check for gigabyte suffix
	if strings.HasSuffix(s, "gb") {
		s = strings.TrimSuffix(s, "gb")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val * 1024)
		}
		return 1024
	}
	if strings.HasSuffix(s, "g") {
		s = strings.TrimSuffix(s, "g")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val * 1024)
		}
		return 1024
	}

	// Check for megabyte suffix
	if strings.HasSuffix(s, "mb") {
		s = strings.TrimSuffix(s, "mb")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val)
		}
		return 1024
	}
	if strings.HasSuffix(s, "m") {
		s = strings.TrimSuffix(s, "m")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val)
		}
		return 1024
	}

	// Plain number (assume megabytes)
	if val, err := strconv.ParseInt(s, 10, 32); err == nil {
		return int32(val)
	}

	return 1024
}

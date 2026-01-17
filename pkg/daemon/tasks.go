package daemon

import (
	"context"
	"fmt"
	"log"
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
	NoTailscale      bool
	TailscaleAuthKey string // Optional: overrides 1Password lookup
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
	if tm.fc != nil {
		vmCfg := &firecracker.VMConfig{
			ID:               taskID,
			Namespace:        "stockyard",
			VCPU:             req.CPUs,
			MemoryMB:         req.MemoryMB,
			CloudInitData:    cloudInitData,
			TailscaleAuthKey: tailscaleAuthKey,
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

	return task, nil
}

// StopTask stops a running task by its ID.
func (tm *TaskManager) StopTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	// Stop VM if firecracker client is available and task has a VM
	if tm.fc != nil && task.VMID != "" {
		if err := tm.fc.StopVM(ctx, "stockyard", task.VMID); err != nil {
			fmt.Printf("Warning: failed to stop VM %s: %v\n", task.VMID, err)
		}
	}

	// Update task status
	return tm.daemon.state.UpdateTaskStatus(taskID, "stopped")
}

// DestroyTask destroys a task and its associated resources.
func (tm *TaskManager) DestroyTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
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

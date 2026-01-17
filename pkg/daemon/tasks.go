package daemon

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/obra/stockyard/pkg/flintlock"
)

// TaskManager handles the lifecycle of VM-based tasks.
type TaskManager struct {
	daemon   *Daemon
	flintock *flintlock.Client
}

// NewTaskManager creates a TaskManager with the given daemon and optional flintlock endpoint.
// If flintEndpoint is empty, VM creation will fail at runtime.
func NewTaskManager(d *Daemon, flintEndpoint string) *TaskManager {
	tm := &TaskManager{
		daemon: d,
	}

	if flintEndpoint != "" {
		client, err := flintlock.NewClient(flintEndpoint)
		if err != nil {
			fmt.Printf("Warning: failed to connect to flintlock at %s: %v\n", flintEndpoint, err)
		} else {
			tm.flintock = client
		}
	}

	return tm
}

// CreateTaskRequest contains the parameters for creating a new task.
type CreateTaskRequest struct {
	Repo        string
	Ref         string
	Name        string
	Command     []string
	Env         map[string]string
	CPUs        int32
	MemoryMB    int32
	NoTailscale bool
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
	taskID := flintlock.GenerateVMID()

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
	if !req.NoTailscale {
		if key, err := tm.daemon.secrets.GetSecret(ctx, "tailscale-auth-key"); err == nil {
			tailscaleAuthKey = key
		}
	}

	// Generate hostname
	hostname := fmt.Sprintf("stockyard-%s", taskID)

	// Generate cloud-init config
	cloudInitCfg := &flintlock.CloudInitConfig{
		Hostname:         hostname,
		Environment:      env,
		TailscaleAuthKey: tailscaleAuthKey,
		WorkspacePath:    workspacePath,
	}

	cloudInitData, err := cloudInitCfg.Generate()
	if err != nil {
		tm.daemon.zfs.DestroyDataset(ctx, taskID)
		return nil, fmt.Errorf("failed to generate cloud-init config: %w", err)
	}

	// Create VM if flintlock client is available
	var vmUID string
	if tm.flintock != nil {
		vmCfg := &flintlock.VMConfig{
			ID:            taskID,
			Namespace:     "stockyard",
			VCPU:          req.CPUs,
			MemoryMB:      req.MemoryMB,
			Image:         "docker.io/library/ubuntu:22.04", // Default image
			KernelImage:   "ghcr.io/weaveworks-liquidmetal/flintlock-kernel:5.10.77",
			WorkspacePath: workspacePath,
			CloudInitData: cloudInitData,
			Network: flintlock.NetworkConfig{
				EnableTailscale: !req.NoTailscale,
			},
			Metadata: map[string]string{
				"task-id":   taskID,
				"task-name": req.Name,
				"repo":      req.Repo,
				"ref":       req.Ref,
			},
		}

		vm, err := tm.flintock.CreateVM(ctx, vmCfg)
		if err != nil {
			tm.daemon.zfs.DestroyDataset(ctx, taskID)
			return nil, fmt.Errorf("failed to create VM: %w", err)
		}
		vmUID = vm.UID
	}

	// Determine command string for storage
	commandStr := ""
	if len(req.Command) > 0 {
		commandStr = strings.Join(req.Command, " ")
	}

	// Record task in database
	task := &Task{
		ID:        taskID,
		Name:      req.Name,
		Repo:      req.Repo,
		Ref:       req.Ref,
		Command:   commandStr,
		Status:    "running",
		VMID:      vmUID,
		CreatedAt: time.Now(),
	}

	if err := tm.daemon.state.CreateTask(task); err != nil {
		// Attempt cleanup on failure
		if tm.flintock != nil && vmUID != "" {
			tm.flintock.DeleteVM(ctx, vmUID)
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

	// Stop VM if flintlock client is available and task has a VM
	if tm.flintock != nil && task.VMID != "" {
		if err := tm.flintock.DeleteVM(ctx, task.VMID); err != nil {
			fmt.Printf("Warning: failed to delete VM %s: %v\n", task.VMID, err)
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

	// Delete VM if flintlock client is available and task has a VM
	if tm.flintock != nil && task.VMID != "" {
		if err := tm.flintock.DeleteVM(ctx, task.VMID); err != nil {
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
	if tm.flintock != nil {
		return tm.flintock.Close()
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

package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/obra/stockyard/pkg/dashboard"
	"github.com/obra/stockyard/pkg/zfs"
)

// DashboardFacade adapts the daemon's State and TaskManager to the dashboard.RealDaemon interface.
// This provides the dashboard with access to daemon data without import cycles.
type DashboardFacade struct {
	state *State
	tasks *TaskManager
	zfs   *zfs.Manager
}

// NewDashboardFacade creates a new facade for dashboard access.
func NewDashboardFacade(state *State, tasks *TaskManager, zfsMgr *zfs.Manager) *DashboardFacade {
	return &DashboardFacade{
		state: state,
		tasks: tasks,
		zfs:   zfsMgr,
	}
}

// ListTasks returns all tasks, optionally filtered by status.
func (f *DashboardFacade) ListTasks(ctx context.Context, status string) ([]*dashboard.DaemonTask, error) {
	tasks, err := f.state.ListTasks(status)
	if err != nil {
		return nil, err
	}

	result := make([]*dashboard.DaemonTask, len(tasks))
	for i, t := range tasks {
		result[i] = convertToDashboardTask(t)
	}
	return result, nil
}

// GetTask returns a task by ID, or nil if not found.
func (f *DashboardFacade) GetTask(ctx context.Context, id string) (*dashboard.DaemonTask, error) {
	task, err := f.state.GetTask(id)
	if err != nil {
		// Check if it's a "not found" error
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	return convertToDashboardTask(task), nil
}

// CreateTask creates a new task.
func (f *DashboardFacade) CreateTask(ctx context.Context, req *dashboard.DaemonCreateTaskRequest) (*dashboard.DaemonTask, error) {
	if f.tasks == nil {
		return nil, fmt.Errorf("TaskManager not available")
	}

	daemonReq := &CreateTaskRequest{
		Repo:        req.Repo,
		Ref:         req.Ref,
		Name:        req.Name,
		Command:     req.Command,
		CPUs:        req.CPUs,
		MemoryMB:    req.MemoryMB,
		Env:         req.Env,
		NoTailscale: req.NoTailscale,
	}

	task, err := f.tasks.CreateTask(ctx, daemonReq)
	if err != nil {
		return nil, err
	}
	return convertToDashboardTask(task), nil
}

// StopTask stops a running task.
func (f *DashboardFacade) StopTask(ctx context.Context, id string) error {
	if f.tasks != nil {
		return f.tasks.StopTask(ctx, id)
	}
	// Fallback to just updating status if TaskManager not available
	return f.state.UpdateTaskStatus(id, "stopped")
}

// RestartTask restarts a stopped task.
func (f *DashboardFacade) RestartTask(ctx context.Context, id string) error {
	if f.tasks == nil {
		return fmt.Errorf("TaskManager not available")
	}
	return f.tasks.RestartTask(ctx, id)
}

// DestroyTask destroys a task and its resources.
func (f *DashboardFacade) DestroyTask(ctx context.Context, id string) error {
	if f.tasks != nil {
		return f.tasks.DestroyTask(ctx, id)
	}
	// Fallback to just deleting from state if TaskManager not available
	return f.state.DeleteTask(id)
}

// ListTaskSnapshots returns snapshots for a task.
func (f *DashboardFacade) ListTaskSnapshots(ctx context.Context, taskID string) ([]dashboard.DaemonSnapshot, error) {
	snaps, err := f.state.ListTaskSnapshots(taskID)
	if err != nil {
		return nil, err
	}

	result := make([]dashboard.DaemonSnapshot, len(snaps))
	for i, s := range snaps {
		result[i] = dashboard.DaemonSnapshot{
			Name:      s.Name,
			CreatedAt: s.CreatedAt,
		}
	}
	return result, nil
}

// CreateSnapshot creates a new snapshot for a task.
func (f *DashboardFacade) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
	// Get task to find its VMID
	task, err := f.state.GetTask(taskID)
	if err != nil {
		return "", fmt.Errorf("get task: %w", err)
	}

	if task.VMID == "" {
		return "", fmt.Errorf("task %s has no VM", taskID)
	}

	// Create ZFS snapshot
	if f.zfs == nil {
		return "", fmt.Errorf("ZFS manager not available")
	}

	// Sync before snapshot to ensure data consistency
	if err := f.zfs.Sync(ctx, task.VMID); err != nil {
		// Log but don't fail - sync might fail if dataset is busy
	}

	snapName, err := f.zfs.CreateSnapshot(ctx, task.VMID, label)
	if err != nil {
		return "", fmt.Errorf("create ZFS snapshot: %w", err)
	}

	// Record in database
	if err := f.state.RecordSnapshot(taskID, snapName); err != nil {
		// Snapshot was created, so log the DB error but return success
		return snapName, nil
	}

	return snapName, nil
}

// RestoreSnapshot restores a task to a previous snapshot.
func (f *DashboardFacade) RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error {
	// Get task to check status and find VMID
	task, err := f.state.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	if task.VMID == "" {
		return fmt.Errorf("task %s has no VM", taskID)
	}

	if f.zfs == nil {
		return fmt.Errorf("ZFS manager not available")
	}

	// If VM is running, stop it first
	wasRunning := task.Status == "running"
	if wasRunning {
		if f.tasks == nil {
			return fmt.Errorf("cannot stop running VM: TaskManager not available")
		}
		if err := f.tasks.StopTask(ctx, taskID); err != nil {
			return fmt.Errorf("stop VM before restore: %w", err)
		}
	}

	// Roll back the ZFS dataset
	if err := f.zfs.RollbackSnapshot(ctx, task.VMID, snapshotName); err != nil {
		return fmt.Errorf("ZFS rollback: %w", err)
	}

	// Note: We don't automatically restart the VM after rollback.
	// The user can manually restart if needed. This is safer because:
	// 1. The restored state might be incompatible with current config
	// 2. The user might want to inspect the state before running

	return nil
}

// GetVMIP looks up a VM's IP address from DHCP leases.
func (f *DashboardFacade) GetVMIP(ctx context.Context, taskID string) (string, error) {
	if f.tasks == nil {
		return "", fmt.Errorf("TaskManager not available")
	}
	return f.tasks.GetVMIP("stockyard", taskID)
}

// GetVMCID returns the vsock CID for a VM.
func (f *DashboardFacade) GetVMCID(ctx context.Context, taskID string) (uint32, error) {
	task, err := f.state.GetTask(taskID)
	if err != nil {
		return 0, err
	}
	if task == nil {
		return 0, fmt.Errorf("task not found: %s", taskID)
	}
	if task.CID == 0 {
		return 0, fmt.Errorf("VM CID not available (VM may not be running)")
	}
	return task.CID, nil
}

// convertToDashboardTask converts a daemon Task to a dashboard DaemonTask.
func convertToDashboardTask(t *Task) *dashboard.DaemonTask {
	return &dashboard.DaemonTask{
		ID:                t.ID,
		Name:              t.Name,
		Repo:              t.Repo,
		Ref:               t.Ref,
		Command:           t.Command,
		Status:            t.Status,
		VMID:              t.VMID,
		TailscaleHostname: t.TailscaleHostname,
		CreatedAt:         t.CreatedAt,
		StoppedAt:         t.StoppedAt,
	}
}

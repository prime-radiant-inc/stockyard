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
	state   *State
	tasks   *TaskManager
	zfs     *zfs.Manager
	backend string
}

// NewDashboardFacade creates a new facade for dashboard access.
func NewDashboardFacade(state *State, tasks *TaskManager, zfsMgr *zfs.Manager, backend string) *DashboardFacade {
	return &DashboardFacade{
		state:   state,
		tasks:   tasks,
		zfs:     zfsMgr,
		backend: backend,
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
		result[i] = f.convertToDashboardTask(t)
	}
	return result, nil
}

// GetTask returns a task by ID, or nil if not found.
func (f *DashboardFacade) GetTask(ctx context.Context, id string) (*dashboard.DaemonTask, error) {
	if f.tasks == nil {
		return nil, fmt.Errorf("TaskManager not available")
	}
	task, err := f.tasks.GetTask(id)
	if err != nil {
		// Check if it's a "not found" error
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	return f.convertToDashboardTask(task), nil
}

// CreateTask creates a new task.
func (f *DashboardFacade) CreateTask(ctx context.Context, req *dashboard.DaemonCreateTaskRequest) (*dashboard.DaemonTask, error) {
	if f.tasks == nil {
		return nil, fmt.Errorf("TaskManager not available")
	}

	daemonReq := &CreateTaskRequest{
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
	return f.convertToDashboardTask(task), nil
}

// StopTask stops a running task.
func (f *DashboardFacade) StopTask(ctx context.Context, id string) error {
	if f.tasks == nil {
		return fmt.Errorf("TaskManager not available")
	}
	return f.tasks.StopTask(ctx, id)
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
	if f.tasks == nil {
		return fmt.Errorf("TaskManager not available")
	}
	return f.tasks.DestroyTask(ctx, id)
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
	if f.tasks == nil {
		return "", fmt.Errorf("TaskManager not available")
	}
	return f.tasks.CreateSnapshot(ctx, taskID, label)
}

// RestoreSnapshot restores a task to a previous snapshot.
func (f *DashboardFacade) RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error {
	if f.tasks == nil {
		return fmt.Errorf("TaskManager not available")
	}
	return f.tasks.RestoreSnapshot(ctx, taskID, snapshotName)
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

// GetVsockPath returns the vsock UDS path for a VM.
func (f *DashboardFacade) GetVsockPath(ctx context.Context, taskID string) (string, error) {
	task, err := f.state.GetTask(taskID)
	if err != nil {
		return "", err
	}
	if task == nil {
		return "", fmt.Errorf("task not found: %s", taskID)
	}
	if task.VsockPath == "" {
		return "", fmt.Errorf("vsock path not available (VM may not be running)")
	}
	return task.VsockPath, nil
}

// convertToDashboardTask converts a daemon Task to a dashboard DaemonTask.
func (f *DashboardFacade) convertToDashboardTask(t *Task) *dashboard.DaemonTask {
	return &dashboard.DaemonTask{
		ID:                t.ID,
		Name:              t.Name,
		Command:           t.Command,
		Status:            t.Status,
		VMID:              t.VMID,
		Backend:           f.backend,
		TailscaleHostname: t.TailscaleHostname,
		CreatedAt:         t.CreatedAt,
		StoppedAt:         t.StoppedAt,
	}
}

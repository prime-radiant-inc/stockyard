package daemon

import (
	"context"
	"strings"

	"github.com/obra/stockyard/pkg/dashboard"
)

// DashboardFacade adapts the daemon's State and TaskManager to the dashboard.RealDaemon interface.
// This provides the dashboard with access to daemon data without import cycles.
type DashboardFacade struct {
	state *State
	tasks *TaskManager
}

// NewDashboardFacade creates a new facade for dashboard access.
func NewDashboardFacade(state *State, tasks *TaskManager) *DashboardFacade {
	return &DashboardFacade{
		state: state,
		tasks: tasks,
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

// StopTask stops a running task.
func (f *DashboardFacade) StopTask(ctx context.Context, id string) error {
	if f.tasks != nil {
		return f.tasks.StopTask(ctx, id)
	}
	// Fallback to just updating status if TaskManager not available
	return f.state.UpdateTaskStatus(id, "stopped")
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
	// For now, just record in the database
	// In a full implementation, this would also create the ZFS snapshot
	snapName := taskID + "@" + label
	if err := f.state.RecordSnapshot(taskID, snapName); err != nil {
		return "", err
	}
	return snapName, nil
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

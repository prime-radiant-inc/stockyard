package dashboard

import (
	"context"
	"time"
)

// RealDaemon is the interface we need from the actual daemon package.
// These types are designed to match what daemon.State and daemon.TaskManager provide.
// Using separate types avoids import cycles.
type RealDaemon interface {
	// Task operations - matches daemon.State.ListTasks/GetTask and daemon.TaskManager.Stop/Destroy
	ListTasks(ctx context.Context, status string) ([]*DaemonTask, error)
	GetTask(ctx context.Context, id string) (*DaemonTask, error)
	StopTask(ctx context.Context, id string) error
	DestroyTask(ctx context.Context, id string) error

	// Snapshot operations - matches daemon.State.ListTaskSnapshots and ZFS manager
	ListTaskSnapshots(ctx context.Context, taskID string) ([]DaemonSnapshot, error)
	CreateSnapshot(ctx context.Context, taskID, label string) (string, error)
}

// DaemonTask mirrors daemon.Task to avoid import cycles.
type DaemonTask struct {
	ID                string
	Name              string
	Repo              string
	Ref               string
	Command           string
	Status            string
	VMID              string
	TailscaleHostname string
	CreatedAt         time.Time
	StoppedAt         *time.Time
}

// DaemonSnapshot mirrors daemon.SnapshotRecord to avoid import cycles.
type DaemonSnapshot struct {
	Name      string
	CreatedAt time.Time
}

// DaemonAdapter adapts the real daemon to the DaemonAPI interface.
type DaemonAdapter struct {
	daemon RealDaemon
}

// NewDaemonAdapter creates an adapter wrapping the real daemon.
func NewDaemonAdapter(daemon RealDaemon) *DaemonAdapter {
	return &DaemonAdapter{daemon: daemon}
}

func (a *DaemonAdapter) ListTasks(ctx context.Context) ([]Task, error) {
	daemonTasks, err := a.daemon.ListTasks(ctx, "")
	if err != nil {
		return nil, err
	}

	tasks := make([]Task, len(daemonTasks))
	for i, dt := range daemonTasks {
		tasks[i] = convertTask(dt)
	}
	return tasks, nil
}

func (a *DaemonAdapter) GetTask(ctx context.Context, id string) (*Task, error) {
	dt, err := a.daemon.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if dt == nil {
		return nil, nil
	}
	task := convertTask(dt)
	return &task, nil
}

func (a *DaemonAdapter) StopTask(ctx context.Context, id string) error {
	return a.daemon.StopTask(ctx, id)
}

func (a *DaemonAdapter) DestroyTask(ctx context.Context, id string) error {
	return a.daemon.DestroyTask(ctx, id)
}

func (a *DaemonAdapter) ListSnapshots(ctx context.Context, taskID string) ([]Snapshot, error) {
	daemonSnaps, err := a.daemon.ListTaskSnapshots(ctx, taskID)
	if err != nil {
		return nil, err
	}

	snapshots := make([]Snapshot, len(daemonSnaps))
	for i, ds := range daemonSnaps {
		snapshots[i] = convertSnapshot(taskID, ds)
	}
	return snapshots, nil
}

func (a *DaemonAdapter) CreateSnapshot(ctx context.Context, taskID, label string) (*Snapshot, error) {
	snapName, err := a.daemon.CreateSnapshot(ctx, taskID, label)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Name:      snapName,
		TaskID:    taskID,
		Label:     label,
		CreatedAt: time.Now(),
	}, nil
}

// convertTask converts a daemon task to a dashboard task.
func convertTask(dt *DaemonTask) Task {
	return Task{
		ID:            dt.ID,
		Name:          dt.Name,
		RepoURL:       dt.Repo,
		GitRef:        dt.Ref,
		Status:        dt.Status,
		TailscaleHost: dt.TailscaleHostname,
		CreatedAt:     dt.CreatedAt,
		StoppedAt:     dt.StoppedAt,
	}
}

// convertSnapshot converts a daemon snapshot to a dashboard snapshot.
func convertSnapshot(taskID string, ds DaemonSnapshot) Snapshot {
	return Snapshot{
		Name:      ds.Name,
		TaskID:    taskID,
		CreatedAt: ds.CreatedAt,
	}
}

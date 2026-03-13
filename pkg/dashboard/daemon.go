package dashboard

import (
	"context"
	"time"
)

// Task represents a VM task for the dashboard.
type Task struct {
	ID            string
	Name          string
	Status        string
	Owner         string
	TailscaleHost string
	CreatedAt     time.Time
	StoppedAt     *time.Time
}

// Snapshot represents a VM snapshot.
type Snapshot struct {
	Name      string
	TaskID    string
	Label     string
	CreatedAt time.Time
}

// CreateTaskRequest contains the parameters for creating a new task.
type CreateTaskRequest struct {
	Name        string
	Command     []string
	CPUs        int32
	MemoryMB    int32
	Env         map[string]string
	NoTailscale bool
}

// DaemonAPI defines the interface the dashboard needs from the daemon.
type DaemonAPI interface {
	ListTasks(ctx context.Context) ([]Task, error)
	GetTask(ctx context.Context, id string) (*Task, error)
	CreateTask(ctx context.Context, req CreateTaskRequest) (*Task, error)
	StopTask(ctx context.Context, id string) error
	RestartTask(ctx context.Context, id string) error
	DestroyTask(ctx context.Context, id string) error
	ListSnapshots(ctx context.Context, taskID string) ([]Snapshot, error)
	CreateSnapshot(ctx context.Context, taskID, label string) (*Snapshot, error)
	RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error
	GetVMIP(ctx context.Context, taskID string) (string, error)
	GetVMCID(ctx context.Context, taskID string) (uint32, error)
	GetVsockPath(ctx context.Context, taskID string) (string, error)
}

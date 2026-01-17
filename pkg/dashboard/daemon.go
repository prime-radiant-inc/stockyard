package dashboard

import (
	"context"
	"time"
)

// Task represents a VM task for the dashboard.
type Task struct {
	ID            string
	Name          string
	RepoURL       string
	GitRef        string
	Status        string
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

// DaemonAPI defines the interface the dashboard needs from the daemon.
type DaemonAPI interface {
	ListTasks(ctx context.Context) ([]Task, error)
	GetTask(ctx context.Context, id string) (*Task, error)
	StopTask(ctx context.Context, id string) error
	DestroyTask(ctx context.Context, id string) error
	ListSnapshots(ctx context.Context, taskID string) ([]Snapshot, error)
	CreateSnapshot(ctx context.Context, taskID, label string) (*Snapshot, error)
}

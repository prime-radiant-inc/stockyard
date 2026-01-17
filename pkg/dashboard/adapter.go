package dashboard

import (
	"context"
)

// RealDaemon is the interface we need from the actual daemon package.
// This avoids import cycles.
type RealDaemon interface {
	ListTasks(ctx context.Context) ([]interface{}, error)
	GetTask(ctx context.Context, id string) (interface{}, error)
	StopTask(ctx context.Context, id string) error
	DestroyTask(ctx context.Context, id string) error
	ListSnapshots(ctx context.Context, taskID string) ([]interface{}, error)
	CreateSnapshot(ctx context.Context, taskID, label string) (interface{}, error)
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
	// TODO: Implement conversion from daemon types
	return nil, nil
}

func (a *DaemonAdapter) GetTask(ctx context.Context, id string) (*Task, error) {
	// TODO: Implement conversion
	return nil, nil
}

func (a *DaemonAdapter) StopTask(ctx context.Context, id string) error {
	return a.daemon.StopTask(ctx, id)
}

func (a *DaemonAdapter) DestroyTask(ctx context.Context, id string) error {
	return a.daemon.DestroyTask(ctx, id)
}

func (a *DaemonAdapter) ListSnapshots(ctx context.Context, taskID string) ([]Snapshot, error) {
	// TODO: Implement conversion
	return nil, nil
}

func (a *DaemonAdapter) CreateSnapshot(ctx context.Context, taskID, label string) (*Snapshot, error) {
	// TODO: Implement conversion
	return nil, nil
}

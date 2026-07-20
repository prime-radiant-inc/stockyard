package daemon

import (
	"context"
	"fmt"
	"sync"
)

type taskLifecycleLock struct {
	mu   sync.Mutex
	refs int
}

// taskLifecycleLocks serializes resource changes for one task without
// unnecessarily blocking independent tasks.
type taskLifecycleLocks struct {
	mu      sync.Mutex
	entries map[string]*taskLifecycleLock
}

// These narrow hooks make lifecycle admission and snapshot dependencies
// controllable in deterministic public-operation behavior tests.
type taskLifecycleHooks struct {
	queued func(string)
}

type taskSnapshotHooks struct {
	sync    func(context.Context, string) error
	create  func(context.Context, string, string) (string, error)
	restore func(context.Context, string, string) error
	list    func(context.Context, string) ([]string, error)
}

func (tm *TaskManager) withTaskLock(taskID string, fn func() error) error {
	tm.lifecycleLocks.mu.Lock()
	if tm.lifecycleLocks.entries == nil {
		tm.lifecycleLocks.entries = make(map[string]*taskLifecycleLock)
	}
	entry := tm.lifecycleLocks.entries[taskID]
	if entry == nil {
		entry = &taskLifecycleLock{}
		tm.lifecycleLocks.entries[taskID] = entry
	}
	entry.refs++
	tm.lifecycleLocks.mu.Unlock()
	if tm.lifecycleHooks != nil && tm.lifecycleHooks.queued != nil {
		tm.lifecycleHooks.queued(taskID)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()
	defer func() {
		tm.lifecycleLocks.mu.Lock()
		defer tm.lifecycleLocks.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(tm.lifecycleLocks.entries, taskID)
		}
	}()
	return fn()
}

// GetTask returns an exact task row under its lifecycle lock.
func (tm *TaskManager) GetTask(taskID string) (*Task, error) {
	var task *Task
	err := tm.withTaskLock(taskID, func() error {
		var err error
		task, err = tm.daemon.state.GetTask(taskID)
		return err
	})
	return task, err
}

// GetAttachTask admits a task to an interactive session only while it remains
// outside the retained cleanup state.
func (tm *TaskManager) GetAttachTask(taskID string) (*Task, error) {
	var task *Task
	err := tm.withTaskLock(taskID, func() error {
		var err error
		task, err = tm.daemon.state.GetTask(taskID)
		if err != nil {
			return err
		}
		return rejectCleanupPending(task)
	})
	return task, err
}

func (tm *TaskManager) GetVMCID(taskID string) (uint32, error) {
	task, err := tm.GetAttachTask(taskID)
	if err != nil {
		return 0, err
	}
	if task.CID == 0 {
		return 0, fmt.Errorf("VM CID not available (VM may not be running)")
	}
	return task.CID, nil
}

func (tm *TaskManager) GetVsockPath(taskID string) (string, error) {
	task, err := tm.GetAttachTask(taskID)
	if err != nil {
		return "", err
	}
	if task.VsockPath == "" {
		return "", fmt.Errorf("vsock path not available (VM may not be running)")
	}
	return task.VsockPath, nil
}

func (tm *TaskManager) ListTaskSnapshots(ctx context.Context, taskID string) ([]string, error) {
	var snapshots []string
	err := tm.withTaskLock(taskID, func() error {
		task, err := tm.daemon.state.GetTask(taskID)
		if err != nil {
			return err
		}
		if err := rejectCleanupPending(task); err != nil {
			return err
		}
		if tm.daemon.zfs == nil {
			return fmt.Errorf("ZFS manager not available")
		}
		snapshots, err = tm.listSnapshots(ctx, taskID)
		return err
	})
	return snapshots, err
}

func (tm *TaskManager) ListRecordedSnapshots(taskID string) ([]SnapshotRecord, error) {
	var snapshots []SnapshotRecord
	err := tm.withTaskLock(taskID, func() error {
		task, err := tm.daemon.state.GetTask(taskID)
		if err != nil {
			return err
		}
		if err := rejectCleanupPending(task); err != nil {
			return err
		}
		snapshots, err = tm.daemon.state.ListTaskSnapshots(taskID)
		return err
	})
	return snapshots, err
}

func rejectCleanupPending(task *Task) error {
	if task.Status == TaskStatusCleanupPending {
		return ErrTaskCleanupPending
	}
	return nil
}

func (tm *TaskManager) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
	var name string
	err := tm.withTaskLock(taskID, func() error {
		task, err := tm.daemon.state.GetTask(taskID)
		if err != nil {
			return err
		}
		if err := rejectCleanupPending(task); err != nil {
			return err
		}
		if tm.daemon.zfs == nil {
			return fmt.Errorf("ZFS manager not available")
		}
		if err := tm.syncWorkspace(ctx, taskID); err != nil {
			return fmt.Errorf("sync workspace: %w", err)
		}
		name, err = tm.createSnapshot(ctx, taskID, label)
		if err != nil {
			return err
		}
		return tm.daemon.state.RecordSnapshot(taskID, name)
	})
	return name, err
}

func (tm *TaskManager) RestoreSnapshot(ctx context.Context, taskID, name string) error {
	return tm.withTaskLock(taskID, func() error {
		task, err := tm.daemon.state.GetTask(taskID)
		if err != nil {
			return err
		}
		if err := rejectCleanupPending(task); err != nil {
			return err
		}
		if tm.daemon.zfs == nil {
			return fmt.Errorf("ZFS manager not available")
		}
		if task.Status == "running" {
			if err := tm.stopTaskLocked(ctx, taskID); err != nil {
				return err
			}
		}
		return tm.restoreSnapshot(ctx, taskID, name)
	})
}

func (tm *TaskManager) syncWorkspace(ctx context.Context, taskID string) error {
	if tm.snapshotHooks != nil && tm.snapshotHooks.sync != nil {
		return tm.snapshotHooks.sync(ctx, taskID)
	}
	return tm.daemon.zfs.Sync(ctx, taskID)
}

func (tm *TaskManager) createSnapshot(ctx context.Context, taskID, label string) (string, error) {
	if tm.snapshotHooks != nil && tm.snapshotHooks.create != nil {
		return tm.snapshotHooks.create(ctx, taskID, label)
	}
	return tm.daemon.zfs.CreateSnapshot(ctx, taskID, label)
}

func (tm *TaskManager) restoreSnapshot(ctx context.Context, taskID, name string) error {
	if tm.snapshotHooks != nil && tm.snapshotHooks.restore != nil {
		return tm.snapshotHooks.restore(ctx, taskID, name)
	}
	return tm.daemon.zfs.RollbackSnapshot(ctx, taskID, name)
}

func (tm *TaskManager) listSnapshots(ctx context.Context, taskID string) ([]string, error) {
	if tm.snapshotHooks != nil && tm.snapshotHooks.list != nil {
		return tm.snapshotHooks.list(ctx, taskID)
	}
	return tm.daemon.zfs.ListSnapshots(ctx, taskID)
}

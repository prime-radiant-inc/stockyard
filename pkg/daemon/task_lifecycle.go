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
		if err := tm.daemon.zfs.Sync(ctx, taskID); err != nil {
			return fmt.Errorf("sync workspace: %w", err)
		}
		name, err = tm.daemon.zfs.CreateSnapshot(ctx, taskID, label)
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
		return tm.daemon.zfs.RollbackSnapshot(ctx, taskID, name)
	})
}

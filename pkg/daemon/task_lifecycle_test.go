package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
	"github.com/obra/stockyard/pkg/zfs"
)

type lifecycleTestBackend struct {
	deleteVM func(context.Context, string) error
}

func (b *lifecycleTestBackend) CreateVM(context.Context, *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return nil, nil
}

func (b *lifecycleTestBackend) StartVM(_ context.Context, cfg *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return &vmbackend.VMInfo{ID: cfg.ID}, nil
}

func (b *lifecycleTestBackend) StopVM(context.Context, string) error { return nil }

func (b *lifecycleTestBackend) DeleteVM(ctx context.Context, id string) error {
	if b.deleteVM != nil {
		return b.deleteVM(ctx, id)
	}
	return nil
}

func (b *lifecycleTestBackend) GetVM(context.Context, string) (*vmbackend.VMState, error) {
	return nil, nil
}

func (b *lifecycleTestBackend) ListVMs(context.Context) ([]*vmbackend.VMState, error) {
	return nil, nil
}

func (b *lifecycleTestBackend) Close() error { return nil }

func TestTaskLifecyclePublicOperationsSerializeWithDestroy(t *testing.T) {
	tests := []struct {
		name   string
		status string
		run    func(context.Context, *TaskManager, string) error
	}{
		{name: "restart", status: "stopped", run: func(ctx context.Context, tm *TaskManager, id string) error {
			return tm.RestartTask(ctx, id)
		}},
		{name: "stop", status: "running", run: func(ctx context.Context, tm *TaskManager, id string) error {
			return tm.StopTask(ctx, id)
		}},
		{name: "fail", status: "running", run: func(ctx context.Context, tm *TaskManager, id string) error {
			return tm.FailTask(ctx, id, "test failure")
		}},
		{name: "destroy", status: "running", run: func(ctx context.Context, tm *TaskManager, id string) error {
			return tm.DestroyTask(ctx, id)
		}},
		{name: "exact get", status: "running", run: func(_ context.Context, tm *TaskManager, id string) error {
			_, err := tm.GetTask(id)
			return err
		}},
		{name: "create snapshot", status: "running", run: func(ctx context.Context, tm *TaskManager, id string) error {
			_, err := tm.CreateSnapshot(ctx, id, "test")
			return err
		}},
		{name: "restore snapshot", status: "stopped", run: func(ctx context.Context, tm *TaskManager, id string) error {
			return tm.RestoreSnapshot(ctx, id, "snapshot")
		}},
		{name: "snapshot list", status: "running", run: func(ctx context.Context, tm *TaskManager, id string) error {
			_, err := tm.ListTaskSnapshots(ctx, id)
			return err
		}},
		{name: "recorded snapshot list", status: "running", run: func(_ context.Context, tm *TaskManager, id string) error {
			_, err := tm.ListRecordedSnapshots(id)
			return err
		}},
		{name: "attach admission", status: "running", run: func(_ context.Context, tm *TaskManager, id string) error {
			_, err := tm.GetAttachTask(id)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := NewStateInMemory()
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()

			for _, id := range []string{"task-one", "task-two"} {
				if err := state.CreateTask(&Task{
					ID:        id,
					Command:   "run",
					Status:    tt.status,
					VMID:      id,
					CID:       123,
					VsockPath: "/tmp/" + id + ".vsock",
					CreatedAt: time.Now(),
				}); err != nil {
					t.Fatal(err)
				}
			}

			destroyEntered := make(chan struct{})
			releaseDestroy := make(chan struct{})
			var destroyOnce sync.Once
			backend := &lifecycleTestBackend{deleteVM: func(_ context.Context, id string) error {
				if id == "task-one" {
					first := false
					destroyOnce.Do(func() { first = true })
					if first {
						close(destroyEntered)
						<-releaseDestroy
					}
				}
				return nil
			}}
			daemon := &Daemon{
				cfg:   &config.Config{Backend: "apple-container"},
				state: state,
				zfs:   zfs.NewManager("tank", "stockyard/workspaces"),
			}
			tm := NewTaskManager(daemon, backend)
			queued := make(chan string, 3)
			tm.lifecycleHooks = &taskLifecycleHooks{queued: func(taskID string) { queued <- taskID }}
			tm.snapshotHooks = &taskSnapshotHooks{
				sync:    func(context.Context, string) error { return nil },
				create:  func(_ context.Context, taskID, _ string) (string, error) { return taskID + "@snapshot", nil },
				restore: func(context.Context, string, string) error { return nil },
				list:    func(context.Context, string) ([]string, error) { return []string{"snapshot"}, nil },
			}

			destroyDone := make(chan error, 1)
			go func() { destroyDone <- tm.DestroyTask(context.Background(), "task-one") }()
			select {
			case taskID := <-queued:
				if taskID != "task-one" {
					t.Fatalf("destroy queued task = %q", taskID)
				}
			case <-destroyEntered:
				t.Fatal("DestroyTask reached its dependency without queuing on the lifecycle lock")
			}
			<-destroyEntered

			sameDone := make(chan error, 1)
			go func() { sameDone <- tt.run(context.Background(), tm, "task-one") }()
			select {
			case taskID := <-queued:
				if taskID != "task-one" {
					t.Fatalf("same-task operation queued task = %q", taskID)
				}
			case err := <-sameDone:
				t.Fatalf("same-task operation bypassed the lifecycle lock: %v", err)
			}
			select {
			case err := <-sameDone:
				t.Fatalf("same-task operation completed during destroy: %v", err)
			default:
			}

			differentDone := make(chan error, 1)
			go func() { differentDone <- tt.run(context.Background(), tm, "task-two") }()
			select {
			case taskID := <-queued:
				if taskID != "task-two" {
					t.Fatalf("different-task operation queued task = %q", taskID)
				}
			case err := <-differentDone:
				t.Fatalf("different-task operation bypassed the lifecycle lock: %v", err)
			}
			if err := <-differentDone; err != nil {
				t.Fatalf("different-task operation: %v", err)
			}

			close(releaseDestroy)
			if err := <-destroyDone; err != nil {
				t.Fatalf("DestroyTask: %v", err)
			}
			if err := <-sameDone; tt.name == "destroy" {
				if err != nil {
					t.Fatalf("idempotent queued destroy: %v", err)
				}
			} else if !errors.Is(err, ErrTaskNotFound) {
				t.Fatalf("same-task operation after destroy = %v, want task not found", err)
			}
		})
	}
}

func TestTaskLifecycleSerializesOneTaskAndAllowsDifferentTasks(t *testing.T) {
	tm := &TaskManager{}
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	differentDone := make(chan error, 1)

	go func() {
		firstDone <- tm.withTaskLock("task-one", func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	go func() { secondDone <- tm.withTaskLock("task-one", func() error { return nil }) }()
	go func() { differentDone <- tm.withTaskLock("task-two", func() error { return nil }) }()

	select {
	case err := <-differentDone:
		if err != nil {
			t.Fatalf("different task lock: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("different task operation waited for task-one")
	}
	select {
	case <-secondDone:
		t.Fatal("same task operation entered before first operation released")
	default:
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first task operation: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second task operation: %v", err)
	}
}

func TestTaskLifecycleLockEntriesAreReleased(t *testing.T) {
	tm := &TaskManager{}
	for range 2 {
		if err := tm.withTaskLock("task-one", func() error { return nil }); err != nil {
			t.Fatalf("withTaskLock: %v", err)
		}
	}
	if len(tm.lifecycleLocks.entries) != 0 {
		t.Fatalf("retained lock entries = %d, want 0", len(tm.lifecycleLocks.entries))
	}
}

func TestCleanupPendingRejectsNonDestroyOperations(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.CreateTask(&Task{ID: "task-one", Command: "run", Status: "running", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkTaskCleanupPending("task-one"); err != nil {
		t.Fatal(err)
	}
	tm := &TaskManager{daemon: &Daemon{state: state}}
	for name, operation := range map[string]func() error{
		"restart":                func() error { return tm.RestartTask(context.Background(), "task-one") },
		"stop":                   func() error { return tm.StopTask(context.Background(), "task-one") },
		"fail":                   func() error { return tm.FailTask(context.Background(), "task-one", "test") },
		"create snapshot":        func() error { _, err := tm.CreateSnapshot(context.Background(), "task-one", "test"); return err },
		"restore snapshot":       func() error { return tm.RestoreSnapshot(context.Background(), "task-one", "snapshot") },
		"snapshot list":          func() error { _, err := tm.ListTaskSnapshots(context.Background(), "task-one"); return err },
		"recorded snapshot list": func() error { _, err := tm.ListRecordedSnapshots("task-one"); return err },
		"attach admission":       func() error { _, err := tm.GetAttachTask("task-one"); return err },
		"CID attach metadata":    func() error { _, err := tm.GetVMCID("task-one"); return err },
		"vsock attach metadata":  func() error { _, err := tm.GetVsockPath("task-one"); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, ErrTaskCleanupPending) {
				t.Fatalf("%s error = %v, want cleanup-pending rejection", name, err)
			}
		})
	}
	if err := tm.DestroyTask(context.Background(), "task-one"); err != nil {
		t.Fatalf("DestroyTask must resume cleanup_pending: %v", err)
	}
}

func TestTaskLifecycleLockDoesNotRace(t *testing.T) {
	tm := &TaskManager{}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { _ = tm.withTaskLock("task-one", func() error { return nil }) })
	}
	wg.Wait()
}

package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

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
		"restart": func() error { return tm.RestartTask(context.Background(), "task-one") },
		"stop":    func() error { return tm.StopTask(context.Background(), "task-one") },
		"fail":    func() error { return tm.FailTask(context.Background(), "task-one", "test") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, ErrTaskCleanupPending) {
				t.Fatalf("%s error = %v, want cleanup-pending rejection", name, err)
			}
		})
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

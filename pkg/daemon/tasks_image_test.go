package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeImageValidator struct {
	err     error
	lastRef string
}

func (f *fakeImageValidator) ValidateImage(ctx context.Context, ref string) error {
	f.lastRef = ref
	return f.err
}

func TestResolveTaskImage_EmptyResolvesToDefault(t *testing.T) {
	got, err := resolveTaskImage(context.Background(), "", "apple-container", "stockyard-vm:latest", &fakeImageValidator{})
	if err != nil {
		t.Fatalf("resolveTaskImage: %v", err)
	}
	if got != "stockyard-vm:latest" {
		t.Errorf("resolved = %q, want default", got)
	}
}

func TestResolveTaskImage_UnsupportedBackendRejects(t *testing.T) {
	_, err := resolveTaskImage(context.Background(), "prudence-vm:1.2", "firecracker", "default", nil)
	if err == nil {
		t.Fatal("expected rejection when backend lacks ImageValidator")
	}
	want := "firecracker backend does not support per-task images yet (PRI-2150 phase 2)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want substring %q", err, want)
	}
}

func TestResolveTaskImage_ValidatorMissPropagates(t *testing.T) {
	v := &fakeImageValidator{err: fmt.Errorf(`image "nope" not found`)}
	_, err := resolveTaskImage(context.Background(), "nope", "apple-container", "stockyard-vm:latest", v)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected validator error to propagate, got %v", err)
	}
	if v.lastRef != "nope" {
		t.Errorf("validator called with %q, want \"nope\"", v.lastRef)
	}
}

func TestResolveTaskImage_ValidRequestResolves(t *testing.T) {
	got, err := resolveTaskImage(context.Background(), "prudence-vm:1.2", "apple-container", "stockyard-vm:latest", &fakeImageValidator{})
	if err != nil {
		t.Fatalf("resolveTaskImage: %v", err)
	}
	if got != "prudence-vm:1.2" {
		t.Errorf("resolved = %q, want requested ref", got)
	}
}

// TestDestroyTasksByImage verifies that DestroyTasksByImage removes only the
// tasks whose Image matches, leaves others intact, and is safe with a nil
// backend (DestroyTask nil-guards all backend/zfs/feed access and ends in
// state.DeleteTask).
func TestDestroyTasksByImage(t *testing.T) {
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	defer state.Close()

	d := &Daemon{state: state}
	tm := NewTaskManager(d, nil) // nil backend: safe per DestroyTask nil-guards

	// Create two tasks with different images.
	taskTarget := &Task{
		ID: "task-target", Name: "target", Command: "sh", Status: "running",
		Image: "my-image", CreatedAt: time.Now(),
	}
	taskOther := &Task{
		ID: "task-other", Name: "other", Command: "sh", Status: "running",
		Image: "other-image", CreatedAt: time.Now(),
	}
	if err := state.CreateTask(taskTarget); err != nil {
		t.Fatalf("CreateTask target: %v", err)
	}
	if err := state.CreateTask(taskOther); err != nil {
		t.Fatalf("CreateTask other: %v", err)
	}

	if err := tm.DestroyTasksByImage(context.Background(), "my-image"); err != nil {
		t.Fatalf("DestroyTasksByImage: %v", err)
	}

	// Matching task's row must be gone.
	if _, err := state.GetTask("task-target"); err == nil {
		t.Error("expected task-target to be deleted, but GetTask succeeded")
	}

	// Other task's row must survive.
	if _, err := state.GetTask("task-other"); err != nil {
		t.Errorf("task-other should survive: %v", err)
	}
}

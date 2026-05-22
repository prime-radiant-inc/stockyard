package daemon

import (
	"context"
	"testing"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

// fakeReconcileBackend is a Backend stub returning scripted ListVMs results.
type fakeReconcileBackend struct {
	states []*vmbackend.VMState
}

func (f *fakeReconcileBackend) CreateVM(context.Context, *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return nil, nil
}
func (f *fakeReconcileBackend) StartVM(context.Context, *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return nil, nil
}
func (f *fakeReconcileBackend) StopVM(context.Context, string) error   { return nil }
func (f *fakeReconcileBackend) DeleteVM(context.Context, string) error { return nil }
func (f *fakeReconcileBackend) GetVM(context.Context, string) (*vmbackend.VMState, error) {
	return nil, nil
}
func (f *fakeReconcileBackend) ListVMs(context.Context) ([]*vmbackend.VMState, error) {
	return f.states, nil
}
func (f *fakeReconcileBackend) Close() error { return nil }

func TestReconcileRunningVMs_AppleContainer(t *testing.T) {
	dataDir := t.TempDir()
	state, err := NewState(dataDir)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	defer state.Close()

	// Two running tasks: one whose container is live, one whose container is gone.
	live := &Task{ID: "live0001", Name: "live", Status: "running", VMID: "live0001"}
	dead := &Task{ID: "dead0002", Name: "dead", Status: "running", VMID: "dead0002"}
	if err := state.CreateTask(live); err != nil {
		t.Fatalf("CreateTask live: %v", err)
	}
	if err := state.CreateTask(dead); err != nil {
		t.Fatalf("CreateTask dead: %v", err)
	}

	d := &Daemon{
		cfg:   &config.Config{Backend: "apple-container", Daemon: config.DaemonConfig{DataDir: dataDir}},
		state: state,
	}
	d.tasks = NewTaskManager(d, &fakeReconcileBackend{
		states: []*vmbackend.VMState{{ID: "live0001", Status: "running"}},
	})

	d.reconcileRunningVMs()

	got, err := state.GetTask("live0001")
	if err != nil {
		t.Fatalf("GetTask live: %v", err)
	}
	if got.Status != "running" {
		t.Errorf("live container should stay running, got %q", got.Status)
	}
	got, err = state.GetTask("dead0002")
	if err != nil {
		t.Fatalf("GetTask dead: %v", err)
	}
	if got.Status != "stopped" {
		t.Errorf("dead container should be marked stopped, got %q", got.Status)
	}
}

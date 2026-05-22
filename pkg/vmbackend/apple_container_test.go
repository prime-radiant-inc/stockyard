//go:build darwin

package vmbackend

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// fakeRunner records invocations and returns scripted output/errors.
type fakeRunner struct {
	calls   [][]string        // each call's argv (name + args)
	outputs map[string]string // keyed by args[0] (the container subcommand), stdout to return
	errs    map[string]error  // keyed by args[0], error to return
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{}, errs: map[string]error{}}
}

func (f *fakeRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(args) == 0 {
		return nil, nil
	}
	sub := args[0]
	if err, ok := f.errs[sub]; ok {
		return []byte(f.outputs[sub]), err
	}
	return []byte(f.outputs[sub]), nil
}

func TestAppleContainerBackend_ImplementsInterface(t *testing.T) {
	var _ Backend = (*AppleContainerBackend)(nil)
}

func TestAppleContainerBackend_NewSetsDefaults(t *testing.T) {
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{}, newFakeRunner().run)
	if b.cfg.ContainerBin != "container" {
		t.Errorf("expected default ContainerBin %q, got %q", "container", b.cfg.ContainerBin)
	}
}

func TestAppleContainerBackend_CloseKillsFollowers(t *testing.T) {
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, newFakeRunner().run)
	// Register a real, long-lived process as a follower.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	b.mu.Lock()
	b.followers["abc12345"] = &logFollower{cmd: cmd}
	b.mu.Unlock()

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("expected follower process to be killed, but it exited cleanly")
	}
	b.mu.Lock()
	n := len(b.followers)
	b.mu.Unlock()
	if n != 0 {
		t.Errorf("expected followers map cleared, got %d entries", n)
	}
}

func TestAppleContainerBackend_StartVM(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	b.skipLogFollower = true
	if _, err := b.StartVM(context.Background(), &VMConfig{ID: "abc12345"}); err != nil {
		t.Fatalf("StartVM: %v", err)
	}
	joined := strings.Join(fr.calls[0], " ")
	if !strings.Contains(joined, "start stockyard-abc12345") {
		t.Errorf("expected `start stockyard-abc12345`, got: %s", joined)
	}
}

func TestAppleContainerBackend_StopVM(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	if err := b.StopVM(context.Background(), "abc12345"); err != nil {
		t.Fatalf("StopVM: %v", err)
	}
	joined := strings.Join(fr.calls[0], " ")
	if !strings.Contains(joined, "stop stockyard-abc12345") {
		t.Errorf("expected `stop stockyard-abc12345`, got: %s", joined)
	}
}

func TestAppleContainerBackend_DeleteVM(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	if err := b.DeleteVM(context.Background(), "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	// DeleteVM = stop then rm.
	var sawStop, sawRm bool
	for _, c := range fr.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "stop stockyard-abc12345") {
			sawStop = true
		}
		if strings.Contains(j, "rm stockyard-abc12345") {
			sawRm = true
		}
	}
	if !sawStop || !sawRm {
		t.Errorf("DeleteVM should stop and rm; sawStop=%v sawRm=%v", sawStop, sawRm)
	}
}

func TestAppleContainerBackend_CreateVM_BuildsRunArgs(t *testing.T) {
	fr := newFakeRunner()
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{
		Image:    "stockyard-vm:container",
		StateDir: t.TempDir(),
	}, fr.run)
	// Avoid spawning a real log follower in unit tests.
	b.skipLogFollower = true

	info, err := b.CreateVM(context.Background(), &VMConfig{
		ID:       "abc12345",
		VCPU:     4,
		MemoryMB: 2048,
		Env:      map[string]string{"FOO": "bar"},
		Metadata: map[string]string{"task-id": "abc12345"},
	})
	if err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	if info.ID != "abc12345" {
		t.Errorf("expected ID abc12345, got %q", info.ID)
	}
	if len(fr.calls) == 0 {
		t.Fatal("expected at least one container call")
	}
	joined := strings.Join(fr.calls[0], " ")
	for _, want := range []string{
		"run", "-d",
		"--name stockyard-abc12345",
		"--cpus 4",
		"--memory 2048M",
		"--env FOO=bar",
		"--label task-id=abc12345",
		"stockyard-vm:container",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("run args missing %q; got: %s", want, joined)
		}
	}
}

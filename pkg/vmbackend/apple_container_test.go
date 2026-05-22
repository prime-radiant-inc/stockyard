//go:build darwin

package vmbackend

import (
	"context"
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

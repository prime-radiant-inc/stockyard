//go:build darwin

package vmbackend

import (
	"context"
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

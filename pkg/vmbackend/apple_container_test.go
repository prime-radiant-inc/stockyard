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

func TestAppleContainerBackend_GetVM_ParsesStatus(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["inspect"] = `[{"status":"running","configuration":{"id":"stockyard-abc12345"}}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)

	st, err := b.GetVM(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if st.ID != "abc12345" {
		t.Errorf("expected ID abc12345, got %q", st.ID)
	}
	if st.Status != "running" {
		t.Errorf("expected status running, got %q", st.Status)
	}
}

func TestAppleContainerBackend_GetVM_StoppedStatus(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["inspect"] = `[{"status":"stopped","configuration":{"id":"stockyard-abc12345"}}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	st, err := b.GetVM(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if st.Status != "stopped" {
		t.Errorf("expected status stopped, got %q", st.Status)
	}
}

func TestAppleContainerBackend_ListVMs_ParsesArray(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["ls"] = `[
		{"status":"running","configuration":{"id":"stockyard-aaa11111"}},
		{"status":"stopped","configuration":{"id":"stockyard-bbb22222"}},
		{"status":"running","configuration":{"id":"not-ours"}}
	]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	states, err := b.ListVMs(context.Background())
	if err != nil {
		t.Fatalf("ListVMs: %v", err)
	}
	// Only stockyard-prefixed containers belong to us.
	got := map[string]string{}
	for _, s := range states {
		got[s.ID] = s.Status
	}
	if got["aaa11111"] != "running" {
		t.Errorf("expected aaa11111 running, got %q", got["aaa11111"])
	}
	if got["bbb22222"] != "stopped" {
		t.Errorf("expected bbb22222 stopped, got %q", got["bbb22222"])
	}
	if _, ok := got["not-ours"]; ok {
		t.Error("non-stockyard container should be ignored")
	}
}

func TestAppleContainerBackend_InspectIP(t *testing.T) {
	fr := newFakeRunner()
	fr.outputs["inspect"] = `[{"status":"running","networks":[{"address":"192.168.64.7/24"}],"configuration":{"id":"stockyard-abc12345"}}]`
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, fr.run)
	ip, err := b.inspectIP(context.Background(), "abc12345")
	if err != nil {
		t.Fatalf("inspectIP: %v", err)
	}
	if ip != "192.168.64.7" {
		t.Errorf("expected 192.168.64.7 (CIDR stripped), got %q", ip)
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
	// Provide inspect output so pollIP resolves on the first attempt (no delay).
	fr.outputs["inspect"] = `[{"status":"running","networks":[{"address":"192.168.64.7/24"}],"configuration":{"id":"stockyard-abc12345"}}]`
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

// TestAppleContainerBackend_StartLogFollower_EvictsExisting asserts that
// calling startLogFollower for a VM ID that already has a registered follower
// kills the old process before spawning a new one. This is a regression test
// for the follower-leak bug where the old *logFollower was silently overwritten.
func TestAppleContainerBackend_StartLogFollower_EvictsExisting(t *testing.T) {
	b := newAppleContainerBackendWithRunner(AppleContainerConfig{StateDir: t.TempDir()}, newFakeRunner().run)

	// Register a real long-lived process as the existing follower.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	b.mu.Lock()
	b.followers["abc12345"] = &logFollower{cmd: cmd}
	b.mu.Unlock()

	// startLogFollower will attempt to evict the old follower before spawning
	// `container logs -f`. Because `container` is not installed, the spawn will
	// fail — but the eviction must have already happened, which is what we test.
	_ = b.startLogFollower("abc12345")

	// The old process must have been killed.
	if err := cmd.Wait(); err == nil {
		t.Error("expected old follower process to be killed before new spawn, but it exited cleanly")
	}
}

func TestAppleContainerBackend_CreateVM_BuildsRunArgs(t *testing.T) {
	fr := newFakeRunner()
	// Provide inspect output so pollIP resolves on the first attempt (no delay).
	fr.outputs["inspect"] = `[{"status":"running","networks":[{"address":"192.168.64.5/24"}],"configuration":{"id":"stockyard-abc12345"}}]`
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

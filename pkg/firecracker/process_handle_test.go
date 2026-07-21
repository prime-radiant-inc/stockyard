package firecracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfiguredExecutableIdentityFollowsSymlinkAndLookPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "firecracker-real")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "firecracker")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	fromLink, err := resolveConfiguredExecutableIdentity(link)
	if err != nil {
		t.Fatalf("resolve symlink: %v", err)
	}
	fromLookPath, err := resolveConfiguredExecutableIdentity("firecracker")
	if err != nil {
		t.Fatalf("resolve LookPath name: %v", err)
	}
	if !fromLink.same(fromLookPath) || !fromLookPath.same(fromLink) {
		t.Fatal("symlink and LookPath resolutions do not identify the same executable file")
	}
}

func TestResolveConfiguredExecutableIdentityErrors(t *testing.T) {
	if _, err := resolveConfiguredExecutableIdentity(filepath.Join(t.TempDir(), "missing-firecracker")); err == nil {
		t.Fatal("missing configured executable resolved successfully")
	}

	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "firecracker")
	if err := os.WriteFile(notExecutable, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if _, err := resolveConfiguredExecutableIdentity("firecracker"); err == nil {
		t.Fatal("non-executable LookPath candidate resolved successfully")
	}
}

func TestFindProcessBySocketFailsClosedWhenConfiguredExecutableIsUnknown(t *testing.T) {
	client, vmDir := newDeleteTestClient(t, false, false)
	socketPath := filepath.Join(vmDir, "api.sock")
	resolveErr := errors.New("configured executable stat unavailable")
	client.resolveExecutable = func(string) (executableIdentity, error) { return nil, resolveErr }
	opened := false
	client.processes = &fakeStableProcessProvider{
		candidateFunc: func(_ context.Context, _, _ string) ([]int, error) { return []int{1234}, nil },
		openFunc: func(int) (stableProcessHandle, error) {
			opened = true
			return nil, errors.New("unexpected open")
		},
	}

	_, err := client.findProcessBySocket(context.Background(), socketPath)
	if !errors.Is(err, resolveErr) {
		t.Fatalf("findProcessBySocket error = %v, want %v", err, resolveErr)
	}
	if opened {
		t.Fatal("process handle opened without a configured executable identity")
	}
}

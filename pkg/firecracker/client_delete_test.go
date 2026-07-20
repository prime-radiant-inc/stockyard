package firecracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/obra/stockyard/pkg/consolearchive"
)

func TestClientDeleteVMRetainsDirectoryWhenTapReadbackIsUnknown(t *testing.T) {
	stateDir := t.TempDir()
	client, err := NewClient(ClientConfig{StateDir: stateDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	vmDir := makeVMDir(t, stateDir, "abc12345")
	client.deleteHooks = &deleteVMHooks{
		tapExists: func(string) (bool, error) { return false, errors.New("tap readback unavailable") },
	}
	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err == nil {
		t.Fatal("DeleteVM succeeded with an unverifiable tap")
	}
	if _, err := os.Stat(vmDir); err != nil {
		t.Fatalf("VM directory was removed before all resources were verified: %v", err)
	}
}

func TestClientDeleteVMIsIdempotentAfterVerifiedAbsence(t *testing.T) {
	stateDir := t.TempDir()
	client, err := NewClient(ClientConfig{StateDir: stateDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("first exact-absence delete: %v", err)
	}
	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("second exact-absence delete: %v", err)
	}
}

func silentArchiver(dir string) *consolearchive.Archiver {
	return &consolearchive.Archiver{Dir: dir, Logf: func(string, ...any) {}}
}

func makeVMDir(t *testing.T, stateDir, id string) string {
	t.Helper()
	vmDir := filepath.Join(stateDir, "stockyard", id)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "stdout.log"), []byte("kernel boot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "stderr.log"), []byte("oh no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return vmDir
}

func TestDeleteVMArchivesConsoleBeforeRemoval(t *testing.T) {
	stateDir := t.TempDir()
	archiveDir := t.TempDir()
	client, err := NewClient(ClientConfig{StateDir: stateDir, ConsoleArchive: silentArchiver(archiveDir)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	vmDir := makeVMDir(t, stateDir, "abc12345")

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Error("vm dir should be removed")
	}
	matches, _ := filepath.Glob(filepath.Join(archiveDir, "*-abc12345-*", "stdout.log"))
	if len(matches) != 1 {
		t.Fatalf("expected one archived stdout.log, got %v", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil || string(data) != "kernel boot\n" {
		t.Errorf("archived console = %q, err %v", data, err)
	}
}

func TestDeleteVMSucceedsWhenArchiverFails(t *testing.T) {
	stateDir := t.TempDir()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientConfig{
		StateDir:       stateDir,
		ConsoleArchive: silentArchiver(filepath.Join(blocker, "archive")),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	vmDir := makeVMDir(t, stateDir, "abc12345")

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM must succeed despite archive failure: %v", err)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Error("vm dir should be removed even when archiving fails")
	}
}

func TestDeleteVMWithoutArchiver(t *testing.T) {
	stateDir := t.TempDir()
	client, err := NewClient(ClientConfig{StateDir: stateDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	vmDir := makeVMDir(t, stateDir, "abc12345")

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Error("vm dir should be removed")
	}
}

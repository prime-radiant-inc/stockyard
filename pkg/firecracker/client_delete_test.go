package firecracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/consolearchive"
	"github.com/obra/stockyard/pkg/zfs"
)

func newDeleteTestClient(t *testing.T, createDir, withZFS bool) (*Client, string) {
	t.Helper()
	stateDir := t.TempDir()
	var zfsManager *zfs.Manager
	if withZFS {
		zfsManager = zfs.NewManager("tank", "stockyard/workspaces")
	}
	client, err := NewClient(ClientConfig{
		StateDir:       stateDir,
		FirecrackerBin: "/usr/local/bin/firecracker",
		VMsPath:        "stockyard/vms",
	}, zfsManager)
	if err != nil {
		t.Fatal(err)
	}
	client.deleteHooks = &deleteVMHooks{
		listProcesses: func(context.Context) ([]byte, error) { return nil, nil },
		deleteTap:     func(string) error { return nil },
		tapExists:     func(string) (bool, error) { return false, nil },
	}
	vmDir := filepath.Join(stateDir, "stockyard", "abc12345")
	if createDir {
		makeVMDir(t, stateDir, "abc12345")
	}
	return client, vmDir
}

func firecrackerProcessRecord(pid int, socketPath string) []byte {
	return []byte(fmt.Sprintf("%d firecracker /usr/local/bin/firecracker --api-sock %s\n", pid, socketPath))
}

func TestClientDeleteVMTerminatesDetachedProcessWithoutStateMetadata(t *testing.T) {
	client, vmDir := newDeleteTestClient(t, false, false)
	socketPath := filepath.Join(vmDir, "api.sock")
	running := true
	var signals []syscall.Signal
	client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
		if running {
			return firecrackerProcessRecord(1234, socketPath), nil
		}
		return nil, nil
	}
	client.deleteHooks.signalProcess = func(pid int, signal syscall.Signal) error {
		if pid != 1234 {
			t.Fatalf("signaled PID = %d", pid)
		}
		signals = append(signals, signal)
		return nil
	}
	client.deleteHooks.waitForProcessExit = func(context.Context, string, int, time.Duration) error {
		running = false
		return nil
	}

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if len(signals) != 1 || signals[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v, want SIGTERM", signals)
	}
}

func TestClientDeleteVMKillsDetachedProcessWhenGracefulStopDoesNotConverge(t *testing.T) {
	client, vmDir := newDeleteTestClient(t, false, false)
	socketPath := filepath.Join(vmDir, "api.sock")
	running := true
	var signals []syscall.Signal
	client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
		if running {
			return firecrackerProcessRecord(1234, socketPath), nil
		}
		return nil, nil
	}
	client.deleteHooks.signalProcess = func(_ int, signal syscall.Signal) error {
		signals = append(signals, signal)
		return nil
	}
	waits := 0
	client.deleteHooks.waitForProcessExit = func(context.Context, string, int, time.Duration) error {
		waits++
		if waits == 1 {
			return errFirecrackerProcessStillRunning
		}
		running = false
		return nil
	}

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if len(signals) != 2 || signals[0] != syscall.SIGTERM || signals[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v, want SIGTERM then SIGKILL", signals)
	}
}

func TestClientDeleteVMTerminatesTrackedProcess(t *testing.T) {
	client, _ := newDeleteTestClient(t, true, false)
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	client.procs["abc12345"] = &trackedProc{cmd: cmd, done: done}
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	})

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("tracked process was not reaped")
	}
}

func TestClientDeleteVMProcessTerminationAndReadbackFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testing.T, *Client, string)
		want      string
	}{
		{
			name: "termination permission failure",
			configure: func(_ *testing.T, client *Client, vmDir string) {
				client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
					return firecrackerProcessRecord(1234, filepath.Join(vmDir, "api.sock")), nil
				}
				client.deleteHooks.signalProcess = func(int, syscall.Signal) error {
					return os.ErrPermission
				}
			},
			want: "stop Firecracker process",
		},
		{
			name: "process inventory permission failure",
			configure: func(_ *testing.T, client *Client, _ string) {
				client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
					return nil, os.ErrPermission
				}
			},
			want: "list processes",
		},
		{
			name: "malformed process inventory",
			configure: func(_ *testing.T, client *Client, _ string) {
				client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
					return []byte("not-a-pid firecracker --api-sock /tmp/socket\n"), nil
				}
			},
			want: "malformed process PID",
		},
		{
			name: "multiple exact process owners",
			configure: func(_ *testing.T, client *Client, vmDir string) {
				socketPath := filepath.Join(vmDir, "api.sock")
				client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
					return append(firecrackerProcessRecord(1234, socketPath), firecrackerProcessRecord(5678, socketPath)...), nil
				}
			},
			want: "multiple Firecracker processes",
		},
		{
			name: "final process readback failure",
			configure: func(_ *testing.T, client *Client, _ string) {
				calls := 0
				client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
					calls++
					if calls < 3 {
						return nil, nil
					}
					return nil, errors.New("process readback unavailable")
				}
			},
			want: "process readback unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, vmDir := newDeleteTestClient(t, true, false)
			tt.configure(t, client, vmDir)
			err := client.DeleteVM(context.Background(), "stockyard", "abc12345")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DeleteVM error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestClientDeleteVMIgnoresUnrelatedProcesses(t *testing.T) {
	client, vmDir := newDeleteTestClient(t, true, false)
	socketPath := filepath.Join(vmDir, "api.sock")
	client.deleteHooks.listProcesses = func(context.Context) ([]byte, error) {
		return []byte(fmt.Sprintf(
			"101 firecracker /usr/local/bin/firecracker --api-sock /tmp/other.sock\n102 unrelated /usr/bin/unrelated --api-sock %s\n",
			socketPath,
		)), nil
	}
	client.deleteHooks.signalProcess = func(int, syscall.Signal) error {
		t.Fatal("unrelated process was signaled")
		return nil
	}

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
}

func TestClientDeleteVMResourceFailureMatrix(t *testing.T) {
	tests := []struct {
		name      string
		withZFS   bool
		configure func(*Client, string)
		want      string
	}{
		{name: "VM directory initial read unknown", configure: func(client *Client, _ string) {
			client.deleteHooks.pathExists = func(string) (bool, error) { return false, os.ErrPermission }
		}, want: "read VM directory"},
		{name: "TAP deletion fails", configure: func(client *Client, _ string) {
			client.deleteHooks.deleteTap = func(string) error { return errors.New("tap delete failed") }
		}, want: "tap delete failed"},
		{name: "TAP readback remains", configure: func(client *Client, _ string) {
			client.deleteHooks.tapExists = func(string) (bool, error) { return true, nil }
		}, want: "TAP deletion"},
		{name: "TAP readback unknown", configure: func(client *Client, _ string) {
			client.deleteHooks.tapExists = func(string) (bool, error) { return false, os.ErrPermission }
		}, want: "verify TAP deletion"},
		{name: "rootfs destroy fails", withZFS: true, configure: func(client *Client, _ string) {
			client.deleteHooks.destroyRootfs = func(context.Context, string) error { return errors.New("rootfs destroy failed") }
		}, want: "rootfs destroy failed"},
		{name: "rootfs readback remains", withZFS: true, configure: func(client *Client, _ string) {
			client.deleteHooks.destroyRootfs = func(context.Context, string) error { return nil }
			client.deleteHooks.rootfsExists = func(context.Context, string) (bool, error) { return true, nil }
		}, want: "rootfs clone remains"},
		{name: "rootfs readback unknown", withZFS: true, configure: func(client *Client, _ string) {
			client.deleteHooks.destroyRootfs = func(context.Context, string) error { return nil }
			client.deleteHooks.rootfsExists = func(context.Context, string) (bool, error) { return false, os.ErrPermission }
		}, want: "verify VM rootfs clone deletion"},
		{name: "VM directory removal fails", configure: func(client *Client, _ string) {
			client.deleteHooks.removeAll = func(string) error { return errors.New("remove directory failed") }
		}, want: "remove directory failed"},
		{name: "VM directory readback remains", configure: func(client *Client, vmDir string) {
			calls := 0
			client.deleteHooks.pathExists = func(path string) (bool, error) {
				if path != vmDir {
					return false, fmt.Errorf("unexpected path %s", path)
				}
				calls++
				return true, nil
			}
		}, want: "VM directory deletion"},
		{name: "VM directory readback unknown", configure: func(client *Client, vmDir string) {
			calls := 0
			client.deleteHooks.pathExists = func(path string) (bool, error) {
				if path != vmDir {
					return false, fmt.Errorf("unexpected path %s", path)
				}
				calls++
				if calls == 1 {
					return true, nil
				}
				return false, os.ErrPermission
			}
		}, want: "verify VM directory deletion"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, vmDir := newDeleteTestClient(t, true, tt.withZFS)
			tt.configure(client, vmDir)
			err := client.DeleteVM(context.Background(), "stockyard", "abc12345")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DeleteVM error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestClientDeleteVMProcessMetadataWriteFailures(t *testing.T) {
	for _, filename := range []string{"firecracker.pid", "api.sock.path"} {
		t.Run(filename, func(t *testing.T) {
			vmDir := t.TempDir()
			if err := os.Mkdir(filepath.Join(vmDir, filename), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := writeProcessMetadata(vmDir, 1234, filepath.Join(vmDir, "api.sock")); err == nil {
				t.Fatalf("writeProcessMetadata accepted unwritable %s", filename)
			}
		})
	}
}

func TestClientDeleteVMRetainsDirectoryWhenTapReadbackIsUnknown(t *testing.T) {
	stateDir := t.TempDir()
	client, err := NewClient(ClientConfig{StateDir: stateDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	vmDir := makeVMDir(t, stateDir, "abc12345")
	client.deleteHooks = &deleteVMHooks{
		listProcesses: func(context.Context) ([]byte, error) { return nil, nil },
		deleteTap:     func(string) error { return nil },
		tapExists:     func(string) (bool, error) { return false, errors.New("tap readback unavailable") },
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

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
		deleteTap: func(string) error { return nil },
		tapExists: func(string) (bool, error) { return false, nil },
	}
	client.processes = noStableProcesses()
	vmDir := filepath.Join(stateDir, "stockyard", "abc12345")
	if createDir {
		makeVMDir(t, stateDir, "abc12345")
	}
	return client, vmDir
}

type fakeStableProcessProvider struct {
	candidateFunc func(context.Context, string, string) ([]int, error)
	openFunc      func(int) (stableProcessHandle, error)
}

func (p *fakeStableProcessProvider) candidatePIDs(ctx context.Context, executable, socketPath string) ([]int, error) {
	return p.candidateFunc(ctx, executable, socketPath)
}

func (p *fakeStableProcessProvider) open(pid int) (stableProcessHandle, error) {
	return p.openFunc(pid)
}

type fakeStableProcessHandle struct {
	pid      int
	identity func() (processIdentity, error)
	signal   func(syscall.Signal) error
	wait     func(context.Context, time.Duration) error
	close    func() error
}

func (h *fakeStableProcessHandle) PID() int { return h.pid }
func (h *fakeStableProcessHandle) Identity() (processIdentity, error) {
	return h.identity()
}
func (h *fakeStableProcessHandle) Signal(signal syscall.Signal) error {
	return h.signal(signal)
}
func (h *fakeStableProcessHandle) Wait(ctx context.Context, timeout time.Duration) error {
	return h.wait(ctx, timeout)
}
func (h *fakeStableProcessHandle) Close() error { return h.close() }

func noStableProcesses() stableProcessProvider {
	return &fakeStableProcessProvider{
		candidateFunc: func(context.Context, string, string) ([]int, error) { return nil, nil },
		openFunc: func(int) (stableProcessHandle, error) {
			return nil, errors.New("unexpected process handle open")
		},
	}
}

func newFakeProcessHandle(pid int, executable, socketPath string) *fakeStableProcessHandle {
	return &fakeStableProcessHandle{
		pid: pid,
		identity: func() (processIdentity, error) {
			return processIdentity{executable: executable, arguments: []string{"--api-sock", socketPath}}, nil
		},
		signal: func(syscall.Signal) error { return nil },
		wait:   func(context.Context, time.Duration) error { return nil },
		close:  func() error { return nil },
	}
}

func TestClientDeleteVMTerminatesDetachedProcessWithoutStateMetadata(t *testing.T) {
	client, vmDir := newDeleteTestClient(t, false, false)
	socketPath := filepath.Join(vmDir, "api.sock")
	running := true
	var signals []syscall.Signal
	handle := newFakeProcessHandle(1234, client.config.FirecrackerBin, socketPath)
	handle.signal = func(signal syscall.Signal) error {
		signals = append(signals, signal)
		return nil
	}
	handle.wait = func(context.Context, time.Duration) error {
		running = false
		return nil
	}
	client.processes = &fakeStableProcessProvider{
		candidateFunc: func(context.Context, string, string) ([]int, error) {
			if running {
				return []int{1234}, nil
			}
			return nil, nil
		},
		openFunc: func(pid int) (stableProcessHandle, error) {
			if pid != 1234 {
				t.Fatalf("opened PID = %d", pid)
			}
			return handle, nil
		},
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
	handle := newFakeProcessHandle(1234, client.config.FirecrackerBin, socketPath)
	handle.signal = func(signal syscall.Signal) error {
		signals = append(signals, signal)
		return nil
	}
	waits := 0
	handle.wait = func(context.Context, time.Duration) error {
		waits++
		if waits == 1 {
			return errFirecrackerProcessStillRunning
		}
		running = false
		return nil
	}
	client.processes = &fakeStableProcessProvider{
		candidateFunc: func(context.Context, string, string) ([]int, error) {
			if running {
				return []int{1234}, nil
			}
			return nil, nil
		},
		openFunc: func(int) (stableProcessHandle, error) { return handle, nil },
	}

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if len(signals) != 2 || signals[0] != syscall.SIGTERM || signals[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v, want SIGTERM then SIGKILL", signals)
	}
}

func TestClientDeleteVMDoesNotSignalReusedProcessIdentity(t *testing.T) {
	client, vmDir := newDeleteTestClient(t, false, false)
	socketPath := filepath.Join(vmDir, "api.sock")
	handle := newFakeProcessHandle(1234, "/usr/bin/unrelated", socketPath)
	successorSignaled := false
	handle.signal = func(syscall.Signal) error {
		successorSignaled = true
		return nil
	}
	client.processes = &fakeStableProcessProvider{
		candidateFunc: func(context.Context, string, string) ([]int, error) { return []int{1234}, nil },
		openFunc:      func(int) (stableProcessHandle, error) { return handle, nil },
	}

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if successorSignaled {
		t.Fatal("unrelated successor process was signaled after PID reuse")
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
				handle := newFakeProcessHandle(1234, client.config.FirecrackerBin, filepath.Join(vmDir, "api.sock"))
				signalCalls := 0
				handle.signal = func(syscall.Signal) error {
					signalCalls++
					if signalCalls == 1 {
						return os.ErrPermission
					}
					return nil
				}
				client.processes = processProviderUntilWait(handle)
			},
			want: "stop Firecracker process",
		},
		{
			name: "process candidate read failure",
			configure: func(_ *testing.T, client *Client, _ string) {
				calls := 0
				client.processes = &fakeStableProcessProvider{
					candidateFunc: func(context.Context, string, string) ([]int, error) {
						calls++
						if calls == 1 {
							return nil, os.ErrPermission
						}
						return nil, nil
					},
					openFunc: func(int) (stableProcessHandle, error) { return nil, errors.New("unexpected open") },
				}
			},
			want: "list process candidates",
		},
		{
			name: "stable handle open failure",
			configure: func(_ *testing.T, client *Client, _ string) {
				openCalls := 0
				client.processes = &fakeStableProcessProvider{
					candidateFunc: func(context.Context, string, string) ([]int, error) { return []int{1234}, nil },
					openFunc: func(int) (stableProcessHandle, error) {
						openCalls++
						if openCalls == 1 {
							return nil, os.ErrPermission
						}
						return nil, errProcessAbsent
					},
				}
			},
			want: "open stable process handle",
		},
		{
			name: "stable identity read failure",
			configure: func(_ *testing.T, client *Client, vmDir string) {
				handle := newFakeProcessHandle(1234, client.config.FirecrackerBin, filepath.Join(vmDir, "api.sock"))
				identityCalls := 0
				handle.identity = func() (processIdentity, error) {
					identityCalls++
					if identityCalls == 1 {
						return processIdentity{}, os.ErrPermission
					}
					return processIdentity{}, errProcessAbsent
				}
				client.processes = processProviderForHandle(handle)
			},
			want: "read stable process identity",
		},
		{
			name: "stable wait failure",
			configure: func(_ *testing.T, client *Client, vmDir string) {
				handle := newFakeProcessHandle(1234, client.config.FirecrackerBin, filepath.Join(vmDir, "api.sock"))
				waitCalls := 0
				handle.wait = func(context.Context, time.Duration) error {
					waitCalls++
					if waitCalls == 1 {
						return os.ErrPermission
					}
					return nil
				}
				client.processes = processProviderUntilWait(handle)
			},
			want: "verify Firecracker process deletion",
		},
		{
			name: "multiple exact process owners",
			configure: func(_ *testing.T, client *Client, vmDir string) {
				socketPath := filepath.Join(vmDir, "api.sock")
				candidateCalls := 0
				handles := map[int]stableProcessHandle{
					1234: newFakeProcessHandle(1234, client.config.FirecrackerBin, socketPath),
					5678: newFakeProcessHandle(5678, client.config.FirecrackerBin, socketPath),
				}
				client.processes = &fakeStableProcessProvider{
					candidateFunc: func(context.Context, string, string) ([]int, error) {
						candidateCalls++
						if candidateCalls == 1 {
							return []int{1234, 5678}, nil
						}
						return nil, nil
					},
					openFunc: func(pid int) (stableProcessHandle, error) { return handles[pid], nil },
				}
			},
			want: "multiple Firecracker processes",
		},
		{
			name: "final process readback failure",
			configure: func(_ *testing.T, client *Client, _ string) {
				calls := 0
				client.processes = &fakeStableProcessProvider{
					candidateFunc: func(context.Context, string, string) ([]int, error) {
						calls++
						if calls == 3 {
							return nil, errors.New("process readback unavailable")
						}
						return nil, nil
					},
					openFunc: func(int) (stableProcessHandle, error) { return nil, errors.New("unexpected open") },
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
			if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
				t.Fatalf("DeleteVM retry did not converge after %q: %v", tt.want, err)
			}
		})
	}
}

func processProviderForHandle(handle stableProcessHandle) stableProcessProvider {
	return &fakeStableProcessProvider{
		candidateFunc: func(context.Context, string, string) ([]int, error) { return []int{handle.PID()}, nil },
		openFunc:      func(int) (stableProcessHandle, error) { return handle, nil },
	}
}

func processProviderUntilWait(handle *fakeStableProcessHandle) stableProcessProvider {
	running := true
	wait := handle.wait
	handle.wait = func(ctx context.Context, timeout time.Duration) error {
		err := wait(ctx, timeout)
		if err == nil || errors.Is(err, errProcessAbsent) {
			running = false
		}
		return err
	}
	return &fakeStableProcessProvider{
		candidateFunc: func(context.Context, string, string) ([]int, error) {
			if running {
				return []int{handle.PID()}, nil
			}
			return nil, nil
		},
		openFunc: func(int) (stableProcessHandle, error) { return handle, nil },
	}
}

func TestClientDeleteVMHandlesExactProcessAbsence(t *testing.T) {
	tests := []struct {
		name     string
		provider func(*Client, string) stableProcessProvider
	}{
		{
			name: "process exits before stable handle opens",
			provider: func(*Client, string) stableProcessProvider {
				return &fakeStableProcessProvider{
					candidateFunc: func(context.Context, string, string) ([]int, error) { return []int{1234}, nil },
					openFunc:      func(int) (stableProcessHandle, error) { return nil, errProcessAbsent },
				}
			},
		},
		{
			name: "process exits before identity read",
			provider: func(client *Client, socketPath string) stableProcessProvider {
				handle := newFakeProcessHandle(1234, client.config.FirecrackerBin, socketPath)
				handle.identity = func() (processIdentity, error) { return processIdentity{}, errProcessAbsent }
				return processProviderForHandle(handle)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, vmDir := newDeleteTestClient(t, false, false)
			client.processes = tt.provider(client, filepath.Join(vmDir, "api.sock"))
			if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
				t.Fatalf("DeleteVM: %v", err)
			}
		})
	}
}

func TestClientDeleteVMHandlesProcessAbsenceDuringStableOperations(t *testing.T) {
	for _, operation := range []string{"signal", "wait"} {
		t.Run(operation, func(t *testing.T) {
			client, vmDir := newDeleteTestClient(t, false, false)
			socketPath := filepath.Join(vmDir, "api.sock")
			running := true
			handle := newFakeProcessHandle(1234, client.config.FirecrackerBin, socketPath)
			if operation == "signal" {
				handle.signal = func(syscall.Signal) error {
					running = false
					return errProcessAbsent
				}
				handle.wait = func(context.Context, time.Duration) error {
					return errors.New("wait called after signal proved exact absence")
				}
			} else {
				handle.wait = func(context.Context, time.Duration) error {
					running = false
					return errProcessAbsent
				}
			}
			client.processes = &fakeStableProcessProvider{
				candidateFunc: func(context.Context, string, string) ([]int, error) {
					if running {
						return []int{1234}, nil
					}
					return nil, nil
				},
				openFunc: func(int) (stableProcessHandle, error) { return handle, nil },
			}

			if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
				t.Fatalf("DeleteVM: %v", err)
			}
		})
	}
}

func TestClientDeleteVMIgnoresUnrelatedProcesses(t *testing.T) {
	client, vmDir := newDeleteTestClient(t, true, false)
	socketPath := filepath.Join(vmDir, "api.sock")
	handle := newFakeProcessHandle(102, "/usr/bin/unrelated", socketPath)
	handle.signal = func(syscall.Signal) error {
		t.Fatal("unrelated process was signaled")
		return nil
	}
	client.processes = processProviderForHandle(handle)

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
			calls := 0
			client.deleteHooks.pathExists = func(path string) (bool, error) {
				calls++
				if calls == 1 {
					return false, os.ErrPermission
				}
				_, err := os.Stat(path)
				return err == nil, ignoreNotExist(err)
			}
		}, want: "read VM directory"},
		{name: "TAP deletion fails", configure: func(client *Client, _ string) {
			calls := 0
			client.deleteHooks.deleteTap = func(string) error {
				calls++
				if calls == 1 {
					return errors.New("tap delete failed")
				}
				return nil
			}
		}, want: "tap delete failed"},
		{name: "TAP readback remains", configure: func(client *Client, _ string) {
			calls := 0
			client.deleteHooks.tapExists = func(string) (bool, error) {
				calls++
				return calls == 1, nil
			}
		}, want: "TAP deletion"},
		{name: "TAP readback unknown", configure: func(client *Client, _ string) {
			calls := 0
			client.deleteHooks.tapExists = func(string) (bool, error) {
				calls++
				if calls == 1 {
					return false, os.ErrPermission
				}
				return false, nil
			}
		}, want: "verify TAP deletion"},
		{name: "rootfs destroy fails", withZFS: true, configure: func(client *Client, _ string) {
			calls := 0
			client.deleteHooks.destroyRootfs = func(context.Context, string) error {
				calls++
				if calls == 1 {
					return errors.New("rootfs destroy failed")
				}
				return nil
			}
			client.deleteHooks.rootfsExists = func(context.Context, string) (bool, error) { return false, nil }
		}, want: "rootfs destroy failed"},
		{name: "rootfs readback remains", withZFS: true, configure: func(client *Client, _ string) {
			client.deleteHooks.destroyRootfs = func(context.Context, string) error { return nil }
			calls := 0
			client.deleteHooks.rootfsExists = func(context.Context, string) (bool, error) {
				calls++
				return calls == 1, nil
			}
		}, want: "rootfs clone remains"},
		{name: "rootfs readback unknown", withZFS: true, configure: func(client *Client, _ string) {
			client.deleteHooks.destroyRootfs = func(context.Context, string) error { return nil }
			calls := 0
			client.deleteHooks.rootfsExists = func(context.Context, string) (bool, error) {
				calls++
				if calls == 1 {
					return false, os.ErrPermission
				}
				return false, nil
			}
		}, want: "verify VM rootfs clone deletion"},
		{name: "VM directory removal fails", configure: func(client *Client, _ string) {
			calls := 0
			client.deleteHooks.removeAll = func(path string) error {
				calls++
				if calls == 1 {
					return errors.New("remove directory failed")
				}
				return os.RemoveAll(path)
			}
		}, want: "remove directory failed"},
		{name: "VM directory readback remains", configure: func(client *Client, vmDir string) {
			calls := 0
			client.deleteHooks.pathExists = func(path string) (bool, error) {
				if path != vmDir {
					return false, fmt.Errorf("unexpected path %s", path)
				}
				calls++
				return calls <= 2, nil
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
				if calls == 2 {
					return false, os.ErrPermission
				}
				return false, nil
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
			if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
				t.Fatalf("DeleteVM retry did not converge after %q: %v", tt.want, err)
			}
			if _, err := os.Stat(vmDir); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("VM directory remains after converged retry: %v", err)
			}
		})
	}
}

func ignoreNotExist(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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

func TestClientCreateVMMetadataFailureRunsExactCleanup(t *testing.T) {
	stateDir := t.TempDir()
	rootfsSource := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(rootfsSource, []byte("rootfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientConfig{
		StateDir:       stateDir,
		FirecrackerBin: os.Args[0],
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.processes = noStableProcesses()
	tapPresent := false
	startedPID := 0
	client.createHooks = &createVMHooks{
		createTap: func(string) error {
			tapPresent = true
			return nil
		},
		startProcess: func(cmd *exec.Cmd) error {
			if err := cmd.Start(); err != nil {
				return err
			}
			startedPID = cmd.Process.Pid
			return nil
		},
	}
	client.deleteHooks = &deleteVMHooks{
		deleteTap: func(string) error {
			tapPresent = false
			return nil
		},
		tapExists: func(string) (bool, error) { return tapPresent, nil },
	}
	vmDir := filepath.Join(stateDir, "stockyard", "abc12345")
	if err := os.MkdirAll(filepath.Join(vmDir, "firecracker.pid"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateVM(context.Background(), &VMConfig{
		ID:         "abc12345",
		Namespace:  "stockyard",
		VCPU:       1,
		MemoryMB:   128,
		KernelPath: "/kernel",
		RootfsPath: rootfsSource,
	})
	if err == nil || !strings.Contains(err.Error(), "write Firecracker PID") {
		t.Fatalf("CreateVM error = %v, want metadata write failure", err)
	}
	if startedPID == 0 {
		t.Fatal("Firecracker process was not started")
	}
	if processRunning(startedPID) {
		t.Errorf("Firecracker process %d remains after CreateVM failure", startedPID)
	}
	if tapPresent {
		t.Error("TAP remains after CreateVM failure")
	}
	if _, err := os.Stat(filepath.Join(vmDir, "rootfs.ext4")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rootfs remains after CreateVM failure: %v", err)
	}
	if _, err := os.Stat(vmDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("VM directory remains after CreateVM failure: %v", err)
	}
}

func TestClientCreateVMMetadataFailureSurfacesCleanupUncertainty(t *testing.T) {
	stateDir := t.TempDir()
	rootfsSource := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(rootfsSource, []byte("rootfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ClientConfig{StateDir: stateDir, FirecrackerBin: os.Args[0]}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.processes = noStableProcesses()
	client.createHooks = &createVMHooks{
		createTap:    func(string) error { return nil },
		startProcess: func(cmd *exec.Cmd) error { return cmd.Start() },
	}
	client.deleteHooks = &deleteVMHooks{
		deleteTap: func(string) error { return errors.New("TAP cleanup unavailable") },
		tapExists: func(string) (bool, error) {
			return true, nil
		},
	}
	vmDir := filepath.Join(stateDir, "stockyard", "abc12345")
	if err := os.MkdirAll(filepath.Join(vmDir, "firecracker.pid"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = client.CreateVM(context.Background(), &VMConfig{
		ID:         "abc12345",
		Namespace:  "stockyard",
		VCPU:       1,
		MemoryMB:   128,
		KernelPath: "/kernel",
		RootfsPath: rootfsSource,
	})
	if err == nil {
		t.Fatal("CreateVM succeeded despite metadata and cleanup failures")
	}
	for _, want := range []string{"write Firecracker PID", "TAP cleanup unavailable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("CreateVM error %q does not include %q", err, want)
		}
	}
	if _, statErr := os.Stat(vmDir); statErr != nil {
		t.Errorf("VM directory was removed despite cleanup uncertainty: %v", statErr)
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
		deleteTap: func(string) error { return nil },
		tapExists: func(string) (bool, error) { return false, errors.New("tap readback unavailable") },
	}
	client.processes = noStableProcesses()
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
	client.processes = noStableProcesses()
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
	client.processes = noStableProcesses()
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
	client.processes = noStableProcesses()
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
	client.processes = noStableProcesses()
	vmDir := makeVMDir(t, stateDir, "abc12345")

	if err := client.DeleteVM(context.Background(), "stockyard", "abc12345"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Error("vm dir should be removed")
	}
}

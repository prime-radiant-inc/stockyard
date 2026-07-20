//go:build linux

package firecracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type linuxStableProcessProvider struct{}

type linuxStableProcessHandle struct {
	pid int
	fd  int
}

func newStableProcessProvider() stableProcessProvider {
	return linuxStableProcessProvider{}
}

func (linuxStableProcessProvider) candidatePIDs(ctx context.Context, executable, socketPath string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var candidates []int
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		identity, err := readLinuxProcessIdentity(pid)
		if errors.Is(err, errProcessAbsent) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read /proc/%d/cmdline: %w", pid, err)
		}
		if identityOwnsSocket(identity, executable, socketPath) {
			candidates = append(candidates, pid)
		}
	}
	return candidates, nil
}

func (linuxStableProcessProvider) open(pid int) (stableProcessHandle, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.ENOENT) {
		return nil, errProcessAbsent
	}
	if err != nil {
		return nil, err
	}
	return &linuxStableProcessHandle{pid: pid, fd: fd}, nil
}

func (h *linuxStableProcessHandle) PID() int {
	return h.pid
}

func (h *linuxStableProcessHandle) Identity() (processIdentity, error) {
	return readLinuxProcessIdentity(h.pid)
}

func (h *linuxStableProcessHandle) Signal(signal syscall.Signal) error {
	err := unix.PidfdSendSignal(h.fd, unix.Signal(signal), nil, 0)
	if errors.Is(err, unix.ESRCH) {
		return errProcessAbsent
	}
	return err
}

func (h *linuxStableProcessHandle) Wait(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		pollFor := min(remaining, 50*time.Millisecond)
		ready, err := unix.Poll([]unix.PollFd{{Fd: int32(h.fd), Events: unix.POLLIN}}, int(pollFor.Milliseconds()))
		if err != nil {
			return err
		}
		if ready > 0 {
			return nil
		}
		if remaining == 0 {
			return fmt.Errorf("%w: PID %d", errFirecrackerProcessStillRunning, h.pid)
		}
	}
}

func (h *linuxStableProcessHandle) Close() error {
	return unix.Close(h.fd)
}

func readLinuxProcessIdentity(pid int) (processIdentity, error) {
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if errors.Is(err, os.ErrNotExist) {
		return processIdentity{}, errProcessAbsent
	}
	if err != nil {
		return processIdentity{}, err
	}
	fields := bytes.Split(bytes.TrimSuffix(cmdline, []byte{0}), []byte{0})
	if len(fields) == 0 || len(fields[0]) == 0 {
		return processIdentity{}, nil
	}
	identity := processIdentity{executable: string(fields[0])}
	for _, field := range fields[1:] {
		identity.arguments = append(identity.arguments, string(field))
	}
	return identity, nil
}

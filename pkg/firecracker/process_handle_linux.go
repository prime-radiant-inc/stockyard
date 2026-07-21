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
	"strings"
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

func (linuxStableProcessProvider) candidatePIDs(ctx context.Context, _ string, socketPath string) ([]int, error) {
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
		arguments, err := readLinuxProcessArguments(pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read /proc/%d/cmdline: %w", pid, err)
		}
		if argumentsOwnSocket(arguments, socketPath) {
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
	executable, err := readLinuxExecutableIdentity(h.pid)
	if err != nil {
		return processIdentity{}, h.classifyIdentityReadError("read kernel executable identity", err)
	}
	arguments, err := readLinuxProcessArguments(h.pid)
	if err != nil {
		return processIdentity{}, h.classifyIdentityReadError("read process arguments", err)
	}
	return processIdentity{executable: executable, arguments: arguments}, nil
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

func (h *linuxStableProcessHandle) classifyIdentityReadError(action string, readErr error) error {
	if errors.Is(readErr, os.ErrNotExist) {
		exited, err := h.exited()
		if err != nil {
			return errors.Join(fmt.Errorf("%s for PID %d: %w", action, h.pid, readErr), err)
		}
		if exited {
			return errProcessAbsent
		}
	}
	return fmt.Errorf("%s for PID %d: %w", action, h.pid, readErr)
}

func (h *linuxStableProcessHandle) exited() (bool, error) {
	ready, err := unix.Poll([]unix.PollFd{{Fd: int32(h.fd), Events: unix.POLLIN}}, 0)
	if err != nil {
		return false, fmt.Errorf("poll stable process handle for PID %d: %w", h.pid, err)
	}
	return ready > 0, nil
}

func readLinuxExecutableIdentity(pid int) (executableIdentity, error) {
	executablePath := filepath.Join("/proc", strconv.Itoa(pid), "exe")
	target, err := os.Readlink(executablePath)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(target, " (deleted)") {
		return nil, fmt.Errorf("kernel executable for PID %d is deleted", pid)
	}
	info, err := os.Stat(executablePath)
	if err != nil {
		return nil, err
	}
	return fileExecutableIdentity{info: info}, nil
}

func readLinuxProcessArguments(pid int) ([]string, error) {
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	fields := bytes.Split(bytes.TrimSuffix(cmdline, []byte{0}), []byte{0})
	if len(fields) < 2 {
		return nil, nil
	}
	arguments := make([]string, 0, len(fields)-1)
	for _, field := range fields[1:] {
		arguments = append(arguments, string(field))
	}
	return arguments, nil
}

package firecracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

var errProcessAbsent = errors.New("process is absent")

type executableIdentity interface {
	same(executableIdentity) bool
}

type fileExecutableIdentity struct {
	info os.FileInfo
}

func (identity fileExecutableIdentity) same(other executableIdentity) bool {
	otherFile, ok := other.(fileExecutableIdentity)
	return ok && os.SameFile(identity.info, otherFile.info)
}

type processIdentity struct {
	executable executableIdentity
	arguments  []string
}

type stableProcessHandle interface {
	PID() int
	Identity() (processIdentity, error)
	Signal(syscall.Signal) error
	Wait(context.Context, time.Duration) error
	Close() error
}

type stableProcessProvider interface {
	candidatePIDs(context.Context, string, string) ([]int, error)
	open(int) (stableProcessHandle, error)
}

func (c *Client) findProcessBySocket(ctx context.Context, socketPath string) (result stableProcessHandle, retErr error) {
	pids, err := c.processes.candidatePIDs(ctx, c.config.FirecrackerBin, socketPath)
	if err != nil {
		return nil, fmt.Errorf("list process candidates: %w", err)
	}
	if len(pids) == 0 {
		return nil, nil
	}
	expectedExecutable, err := c.resolveExecutable(c.config.FirecrackerBin)
	if err != nil {
		return nil, fmt.Errorf("resolve configured Firecracker executable identity: %w", err)
	}

	var match stableProcessHandle
	defer func() {
		if match != nil {
			retErr = errors.Join(retErr, closeStableProcessHandle(match, "close retained Firecracker process handle"))
		}
	}()
	for _, pid := range pids {
		process, err := c.processes.open(pid)
		if errors.Is(err, errProcessAbsent) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("open stable process handle for PID %d: %w", pid, err)
		}

		identity, err := process.Identity()
		if errors.Is(err, errProcessAbsent) {
			if closeErr := closeStableProcessHandle(process, "close absent process handle"); closeErr != nil {
				return nil, closeErr
			}
			continue
		}
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("read stable process identity for PID %d: %w", pid, err),
				closeStableProcessHandle(process, "close process handle after identity failure"),
			)
		}
		if !argumentsOwnSocket(identity.arguments, socketPath) {
			if err := closeStableProcessHandle(process, "close unrelated process handle"); err != nil {
				return nil, err
			}
			continue
		}
		if !sameExecutableIdentity(identity.executable, expectedExecutable) {
			return nil, errors.Join(
				fmt.Errorf("Firecracker process candidate PID %d has an unknown executable identity", pid),
				closeStableProcessHandle(process, "close executable-mismatch process handle"),
			)
		}
		if match != nil {
			firstPID := match.PID()
			return nil, errors.Join(
				fmt.Errorf("multiple Firecracker processes own API socket %s: PIDs %d and %d", socketPath, firstPID, pid),
				closeStableProcessHandle(process, "close additional Firecracker process handle"),
			)
		}
		match = process
	}
	result = match
	match = nil
	return result, nil
}

func resolveConfiguredExecutableIdentity(configured string) (executableIdentity, error) {
	path, err := exec.LookPath(configured)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return fileExecutableIdentity{info: info}, nil
}

func sameExecutableIdentity(left, right executableIdentity) bool {
	if left == nil || right == nil {
		return false
	}
	return left.same(right) && right.same(left)
}

func argumentsOwnSocket(arguments []string, socketPath string) bool {
	for i := 0; i+1 < len(arguments); i++ {
		if arguments[i] == "--api-sock" && arguments[i+1] == socketPath {
			return true
		}
	}
	return false
}

func closeStableProcessHandle(process stableProcessHandle, action string) error {
	if err := process.Close(); err != nil {
		return fmt.Errorf("%s for PID %d: %w", action, process.PID(), err)
	}
	return nil
}

package firecracker

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
)

var errProcessAbsent = errors.New("process is absent")

type processIdentity struct {
	executable string
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

func (c *Client) findProcessBySocket(ctx context.Context, socketPath string) (stableProcessHandle, error) {
	pids, err := c.processes.candidatePIDs(ctx, c.config.FirecrackerBin, socketPath)
	if err != nil {
		return nil, fmt.Errorf("list process candidates: %w", err)
	}

	var match stableProcessHandle
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
			if closeErr := process.Close(); closeErr != nil {
				return nil, fmt.Errorf("close absent process handle for PID %d: %w", pid, closeErr)
			}
			continue
		}
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("read stable process identity for PID %d: %w", pid, err),
				process.Close(),
			)
		}
		if !identityOwnsSocket(identity, c.config.FirecrackerBin, socketPath) {
			if err := process.Close(); err != nil {
				return nil, fmt.Errorf("close unrelated process handle for PID %d: %w", pid, err)
			}
			continue
		}
		if match != nil {
			firstPID := match.PID()
			closeErr := errors.Join(match.Close(), process.Close())
			return nil, errors.Join(
				fmt.Errorf("multiple Firecracker processes own API socket %s: PIDs %d and %d", socketPath, firstPID, pid),
				closeErr,
			)
		}
		match = process
	}
	return match, nil
}

func identityOwnsSocket(identity processIdentity, executable, socketPath string) bool {
	if identity.executable != executable {
		return false
	}
	for i := 0; i+1 < len(identity.arguments); i++ {
		if identity.arguments[i] == "--api-sock" && identity.arguments[i+1] == socketPath {
			return true
		}
	}
	return false
}

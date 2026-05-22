//go:build darwin

package vmbackend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// AppleContainerConfig configures the Apple `container` backend.
type AppleContainerConfig struct {
	ContainerBin string // Path to the `container` binary (default: "container")
	Image        string // OCI image reference for task containers
	StateDir     string // Directory holding per-VM state (captured log files)
}

// commandRunner runs an external command and returns its combined behaviour.
// It is the test seam: production uses execRunner; tests inject a fake.
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRunner is the production commandRunner — it shells out for real.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %v: %w: %s", name, args, err, string(out))
	}
	return out, nil
}

// logFollower tracks a `container logs -f` process so it can be killed later.
type logFollower struct {
	cmd *exec.Cmd
}

// AppleContainerBackend implements Backend by shelling out to Apple's `container` CLI.
type AppleContainerBackend struct {
	cfg             AppleContainerConfig
	run             commandRunner
	mu              sync.Mutex
	followers       map[string]*logFollower // keyed by VM ID
	skipLogFollower bool
}

// containerName returns the deterministic container name for a VM ID.
func containerName(vmID string) string {
	return "stockyard-" + vmID
}

// newAppleContainerBackendWithRunner builds a backend with an injectable runner (for tests).
func newAppleContainerBackendWithRunner(cfg AppleContainerConfig, run commandRunner) *AppleContainerBackend {
	if cfg.ContainerBin == "" {
		cfg.ContainerBin = "container"
	}
	return &AppleContainerBackend{
		cfg:       cfg,
		run:       run,
		followers: make(map[string]*logFollower),
	}
}

// NewAppleContainerBackend builds a production backend using the real CLI runner.
func NewAppleContainerBackend(cfg AppleContainerConfig) *AppleContainerBackend {
	return newAppleContainerBackendWithRunner(cfg, execRunner)
}

var errNotImplemented = errors.New("apple-container backend: not implemented")

func (b *AppleContainerBackend) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) StopVM(ctx context.Context, id string) error {
	return errNotImplemented
}

func (b *AppleContainerBackend) DeleteVM(ctx context.Context, id string) error {
	return errNotImplemented
}

func (b *AppleContainerBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) Close() error {
	return nil
}

// vmStateDir returns the per-VM state directory (holds captured logs).
func (b *AppleContainerBackend) vmStateDir(id string) string {
	return filepath.Join(b.cfg.StateDir, id)
}

// ensureStateDir creates the per-VM state directory.
func (b *AppleContainerBackend) ensureStateDir(id string) (string, error) {
	dir := b.vmStateDir(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create VM state dir: %w", err)
	}
	return dir, nil
}

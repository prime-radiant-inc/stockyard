//go:build darwin

package vmbackend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
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
	stateDir, err := b.ensureStateDir(cfg.ID)
	if err != nil {
		return nil, err
	}

	args := b.buildRunArgs(cfg)
	if _, err := b.run(ctx, b.cfg.ContainerBin, args...); err != nil {
		os.RemoveAll(stateDir)
		return nil, fmt.Errorf("container run: %w", err)
	}

	if !b.skipLogFollower {
		if err := b.startLogFollower(cfg.ID); err != nil {
			// Non-fatal: the container is running; logs just won't be captured.
			fmt.Printf("Warning: apple-container log follower for %s: %v\n", cfg.ID, err)
		}
	}

	ip, _ := b.inspectIP(ctx, cfg.ID) // best-effort; empty IP is acceptable

	return &VMInfo{
		ID:        cfg.ID,
		PID:       0, // container manages the workload; no meaningful hypervisor PID
		IP:        ip,
		StateDir:  stateDir,
		State:     "running",
		CreatedAt: time.Now(),
	}, nil
}

// buildRunArgs constructs the `container run -d ...` argument vector.
func (b *AppleContainerBackend) buildRunArgs(cfg *VMConfig) []string {
	args := []string{
		"run", "-d",
		"--name", containerName(cfg.ID),
		"--cpus", fmt.Sprintf("%d", cfg.VCPU),
		"--memory", fmt.Sprintf("%dM", cfg.MemoryMB),
	}
	// Deterministic ordering so tests and diffs are stable.
	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "--env", k+"="+cfg.Env[k])
	}
	metaKeys := make([]string, 0, len(cfg.Metadata))
	for k := range cfg.Metadata {
		metaKeys = append(metaKeys, k)
	}
	sort.Strings(metaKeys)
	for _, k := range metaKeys {
		args = append(args, "--label", k+"="+cfg.Metadata[k])
	}
	args = append(args, b.cfg.Image)
	return args
}

// startLogFollower spawns `container logs -f` redirecting into the per-VM
// stdout.log / stderr.log so the daemon's logTailer works unchanged.
func (b *AppleContainerBackend) startLogFollower(id string) error {
	dir := b.vmStateDir(id)
	stdoutF, err := os.Create(filepath.Join(dir, "stdout.log"))
	if err != nil {
		return fmt.Errorf("create stdout.log: %w", err)
	}
	stderrF, err := os.Create(filepath.Join(dir, "stderr.log"))
	if err != nil {
		stdoutF.Close()
		return fmt.Errorf("create stderr.log: %w", err)
	}
	cmd := exec.Command(b.cfg.ContainerBin, "logs", "-f", containerName(id))
	cmd.Stdout = stdoutF
	cmd.Stderr = stderrF
	if err := cmd.Start(); err != nil {
		stdoutF.Close()
		stderrF.Close()
		return fmt.Errorf("start log follower: %w", err)
	}
	go func() {
		cmd.Wait()
		stdoutF.Close()
		stderrF.Close()
	}()
	b.mu.Lock()
	b.followers[id] = &logFollower{cmd: cmd}
	b.mu.Unlock()
	return nil
}

// stopLogFollower kills and forgets the log follower for a VM, if any.
func (b *AppleContainerBackend) stopLogFollower(id string) {
	b.mu.Lock()
	f, ok := b.followers[id]
	delete(b.followers, id)
	b.mu.Unlock()
	if ok && f.cmd.Process != nil {
		f.cmd.Process.Kill()
	}
}

// inspectIP reads the container's IP from `container inspect --format json`.
// Defined fully in Task 1.6; a stub here keeps CreateVM compiling.
func (b *AppleContainerBackend) inspectIP(ctx context.Context, id string) (string, error) {
	return "", nil
}

func (b *AppleContainerBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	stateDir, err := b.ensureStateDir(cfg.ID)
	if err != nil {
		return nil, err
	}
	if _, err := b.run(ctx, b.cfg.ContainerBin, "start", containerName(cfg.ID)); err != nil {
		return nil, fmt.Errorf("container start: %w", err)
	}
	if !b.skipLogFollower {
		if err := b.startLogFollower(cfg.ID); err != nil {
			fmt.Printf("Warning: apple-container log follower for %s: %v\n", cfg.ID, err)
		}
	}
	ip, _ := b.inspectIP(ctx, cfg.ID)
	return &VMInfo{
		ID:        cfg.ID,
		IP:        ip,
		StateDir:  stateDir,
		State:     "running",
		CreatedAt: time.Now(),
	}, nil
}

func (b *AppleContainerBackend) StopVM(ctx context.Context, id string) error {
	b.stopLogFollower(id)
	if _, err := b.run(ctx, b.cfg.ContainerBin, "stop", containerName(id)); err != nil {
		return fmt.Errorf("container stop: %w", err)
	}
	return nil
}

func (b *AppleContainerBackend) DeleteVM(ctx context.Context, id string) error {
	b.stopLogFollower(id)
	// Best-effort stop; ignore error (container may already be stopped).
	b.run(ctx, b.cfg.ContainerBin, "stop", containerName(id))
	if _, err := b.run(ctx, b.cfg.ContainerBin, "rm", containerName(id)); err != nil {
		return fmt.Errorf("container rm: %w", err)
	}
	os.RemoveAll(b.vmStateDir(id))
	return nil
}

func (b *AppleContainerBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	return nil, errNotImplemented
}

func (b *AppleContainerBackend) Close() error {
	b.mu.Lock()
	ids := make([]string, 0, len(b.followers))
	for id := range b.followers {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		b.stopLogFollower(id)
	}
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

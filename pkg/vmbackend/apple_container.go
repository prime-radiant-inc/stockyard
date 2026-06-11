//go:build darwin

package vmbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

	ip := b.pollIP(ctx, cfg.ID) // best-effort; empty IP is acceptable

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
	image := cfg.Image
	if image == "" {
		image = b.cfg.Image
	}
	args = append(args, image)
	return args
}

// startLogFollower spawns `container logs -f` redirecting into the per-VM
// stdout.log / stderr.log so the daemon's logTailer works unchanged.
func (b *AppleContainerBackend) startLogFollower(id string) error {
	// Kill any existing follower for this ID before spawning a new one.
	// Without this, a retry or duplicate call would silently leak the old process.
	b.stopLogFollower(id)

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

// pollIP retries inspectIP until a non-empty IP is returned or the budget is
// exhausted. It is used by CreateVM and StartVM to give vmnet a short window
// to assign an address. An empty result after the budget is acceptable
// best-effort — callers treat an empty IP as non-fatal.
func (b *AppleContainerBackend) pollIP(ctx context.Context, id string) string {
	const maxAttempts = 20
	const pause = 100 * time.Millisecond
	for i := 0; i < maxAttempts; i++ {
		ip, _ := b.inspectIP(ctx, id)
		if ip != "" {
			return ip
		}
		time.Sleep(pause)
	}
	return ""
}

func (b *AppleContainerBackend) inspectIP(ctx context.Context, id string) (string, error) {
	out, err := b.run(ctx, b.cfg.ContainerBin, "inspect", containerName(id))
	if err != nil {
		return "", fmt.Errorf("container inspect: %w", err)
	}
	var arr []containerJSON
	if err := json.Unmarshal(out, &arr); err != nil {
		return "", fmt.Errorf("parse container inspect JSON: %w", err)
	}
	if len(arr) == 0 || len(arr[0].Networks) == 0 {
		return "", nil
	}
	return addressToIP(arr[0].Networks[0].IPv4Address), nil
}

// StartVM restarts an existing (stopped) container by name. The container's
// environment (including TAILSCALE_AUTH_KEY) was baked in at `container run`
// time and persists across stop/start cycles; tailscaled's node state also
// lives in the container's writable layer and survives restart. No new env
// variables need to be injected here. Known limitation: if the container has
// been stopped longer than Tailscale's node-key expiry the node may require
// re-authorization — this edge case is not handled automatically.
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
	ip := b.pollIP(ctx, cfg.ID) // best-effort; empty IP is acceptable
	return &VMInfo{
		ID:        cfg.ID,
		IP:        ip,
		StateDir:  stateDir,
		State:     "running",
		CreatedAt: time.Now(),
	}, nil
}

// containerGone reports whether err indicates the target container does not
// exist. For stop and delete that is the desired end state, not a failure:
// `container stop`/`rm` exit non-zero when the container is already gone.
func containerGone(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") || strings.Contains(s, "notfound")
}

func (b *AppleContainerBackend) StopVM(ctx context.Context, id string) error {
	b.stopLogFollower(id)
	if _, err := b.run(ctx, b.cfg.ContainerBin, "stop", containerName(id)); err != nil {
		if containerGone(err) {
			return nil // an absent container is already stopped
		}
		return fmt.Errorf("container stop: %w", err)
	}
	return nil
}

func (b *AppleContainerBackend) DeleteVM(ctx context.Context, id string) error {
	b.stopLogFollower(id)
	// Best-effort stop; ignore error (container may already be stopped or gone).
	b.run(ctx, b.cfg.ContainerBin, "stop", containerName(id))
	if _, err := b.run(ctx, b.cfg.ContainerBin, "rm", containerName(id)); err != nil {
		// An already-absent container is the desired end state. DeleteVM must
		// be idempotent so a task whose container is already gone can still be
		// destroyed instead of getting stuck in the daemon's state.
		if !containerGone(err) {
			return fmt.Errorf("container rm: %w", err)
		}
	}
	os.RemoveAll(b.vmStateDir(id))
	return nil
}

// containerJSON is the subset of `container inspect`/`container ls` JSON we use.
// Verified against the `container` 0.12.3 CLI: both commands emit a JSON array
// of these objects (`inspect` emits JSON by default — it has no --format flag;
// `ls` needs --format json). Parse defensively and tolerate missing fields.
type containerJSON struct {
	Status        string `json:"status"`
	Configuration struct {
		ID string `json:"id"`
	} `json:"configuration"`
	Networks []struct {
		IPv4Address string `json:"ipv4Address"` // CIDR form, e.g. "192.168.65.4/24"
	} `json:"networks"`
}

// vmIDFromName strips the "stockyard-" prefix; returns ("", false) if absent.
func vmIDFromName(name string) (string, bool) {
	const prefix = "stockyard-"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	return strings.TrimPrefix(name, prefix), true
}

// addressToIP strips a trailing CIDR suffix ("/24") from an address.
func addressToIP(addr string) string {
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i]
	}
	return addr
}

func (b *AppleContainerBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	out, err := b.run(ctx, b.cfg.ContainerBin, "inspect", containerName(id))
	if err != nil {
		return nil, fmt.Errorf("container inspect: %w", err)
	}
	var arr []containerJSON
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parse container inspect JSON: %w", err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("VM not found: %s", id)
	}
	return &VMState{ID: id, Status: arr[0].Status}, nil
}

func (b *AppleContainerBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	out, err := b.run(ctx, b.cfg.ContainerBin, "ls", "--all", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("container ls: %w", err)
	}
	var arr []containerJSON
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parse container ls JSON: %w", err)
	}
	var states []*VMState
	for _, c := range arr {
		vmID, ok := vmIDFromName(c.Configuration.ID)
		if !ok {
			continue // not one of ours
		}
		states = append(states, &VMState{ID: vmID, Status: c.Status})
	}
	return states, nil
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

// EnsureLogFollower starts a log follower for vmID if one is not already
// tracked. It is a no-op (returns nil) when a follower process is already
// running for this VM. This is called by reconcileViaBackend after a daemon
// restart to re-attach the log stream for tasks that stayed running.
//
// AppleContainerBackend implements vmbackend.LogFollowerEnsurer.
func (b *AppleContainerBackend) EnsureLogFollower(vmID string) error {
	b.mu.Lock()
	_, exists := b.followers[vmID]
	b.mu.Unlock()
	if exists {
		return nil // already following
	}
	if b.skipLogFollower {
		return nil
	}
	return b.startLogFollower(vmID)
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

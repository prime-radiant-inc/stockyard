//go:build darwin

package vmbackend

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const macOSLeaseFile = "/var/db/dhcpd_leases"

// Matches kernel IP-Config output: "my address is 192.168.64.X"
var ipConfigRegex = regexp.MustCompile(`my address is (192\.168\.\d+\.\d+)`)

// VfkitConfig holds configuration for the vfkit backend.
type VfkitConfig struct {
	VfkitBin   string
	KernelPath string
	StateDir   string
}

type vfkitProc struct {
	cmd   *exec.Cmd
	mac   string
	vmDir string
}

// VfkitBackend manages VMs via vfkit subprocesses on macOS.
type VfkitBackend struct {
	cfg   VfkitConfig
	procs map[string]*vfkitProc
	mu    sync.Mutex
}

func NewVfkitBackend(cfg VfkitConfig) *VfkitBackend {
	if cfg.VfkitBin == "" {
		cfg.VfkitBin = "vfkit"
	}
	return &VfkitBackend{
		cfg:   cfg,
		procs: make(map[string]*vfkitProc),
	}
}

func (b *VfkitBackend) CreateVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	vmDir := filepath.Join(b.cfg.StateDir, cfg.ID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return nil, fmt.Errorf("create VM state dir: %w", err)
	}

	mac := generateMAC()
	os.WriteFile(filepath.Join(vmDir, "mac_addr"), []byte(mac), 0644)

	if err := writeAuthorizedKeys(vmDir, cfg); err != nil {
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("write authorized_keys: %w", err)
	}

	args := b.buildArgs(cfg, vmDir, mac)

	cmd := exec.Command(b.cfg.VfkitBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutLog, _ := os.Create(filepath.Join(vmDir, "stdout.log"))
	stderrLog, _ := os.Create(filepath.Join(vmDir, "stderr.log"))
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog

	if err := cmd.Start(); err != nil {
		stdoutLog.Close()
		stderrLog.Close()
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("start vfkit: %w", err)
	}
	stdoutLog.Close()
	stderrLog.Close()

	os.WriteFile(filepath.Join(vmDir, "vfkit.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0644)

	b.mu.Lock()
	b.procs[cfg.ID] = &vfkitProc{cmd: cmd, mac: mac, vmDir: vmDir}
	b.mu.Unlock()

	go func() {
		cmd.Wait()
		b.mu.Lock()
		if p, ok := b.procs[cfg.ID]; ok && p.cmd == cmd {
			delete(b.procs, cfg.ID)
		}
		b.mu.Unlock()
	}()

	time.Sleep(50 * time.Millisecond)
	if !vfkitProcessRunning(cmd.Process.Pid) {
		stderrContent, _ := os.ReadFile(filepath.Join(vmDir, "stderr.log"))
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("vfkit exited immediately: %s", string(stderrContent))
	}

	// Discover IP from kernel console log (kernel ip=dhcp prints it at ~0.2s)
	consolePath := filepath.Join(vmDir, "console.log")
	discoveredIP := ""
	for i := 0; i < 20; i++ {
		if data, err := os.ReadFile(consolePath); err == nil {
			if match := ipConfigRegex.FindSubmatch(data); match != nil {
				discoveredIP = string(match[1])
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	return &VMInfo{
		ID:        cfg.ID,
		PID:       cmd.Process.Pid,
		IP:        discoveredIP,
		StateDir:  vmDir,
		State:     "running",
		CreatedAt: time.Now(),
	}, nil
}

func (b *VfkitBackend) StartVM(ctx context.Context, cfg *VMConfig) (*VMInfo, error) {
	return b.CreateVM(ctx, cfg)
}

func (b *VfkitBackend) StopVM(ctx context.Context, id string) error {
	b.mu.Lock()
	proc, ok := b.procs[id]
	b.mu.Unlock()

	if !ok || proc.cmd.Process == nil {
		return nil
	}

	// Ephemeral VMs — no graceful shutdown needed, just kill.
	// Don't call cmd.Wait() here — the reaper goroutine from CreateVM handles that.
	// Calling Wait twice on the same Cmd is undefined behavior in Go.
	proc.cmd.Process.Kill()

	return nil
}

func (b *VfkitBackend) DeleteVM(ctx context.Context, id string) error {
	b.StopVM(ctx, id)
	vmDir := filepath.Join(b.cfg.StateDir, id)
	return os.RemoveAll(vmDir)
}

func (b *VfkitBackend) GetVM(ctx context.Context, id string) (*VMState, error) {
	b.mu.Lock()
	proc, ok := b.procs[id]
	b.mu.Unlock()

	if ok {
		return &VMState{ID: id, PID: proc.cmd.Process.Pid, Status: "running"}, nil
	}

	vmDir := filepath.Join(b.cfg.StateDir, id)
	if _, err := os.Stat(vmDir); err == nil {
		return &VMState{ID: id, Status: "stopped"}, nil
	}
	return nil, fmt.Errorf("VM not found: %s", id)
}

func (b *VfkitBackend) ListVMs(ctx context.Context) ([]*VMState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var states []*VMState
	for id, proc := range b.procs {
		states = append(states, &VMState{
			ID:     id,
			PID:    proc.cmd.Process.Pid,
			Status: "running",
		})
	}
	return states, nil
}

func (b *VfkitBackend) Close() error {
	b.mu.Lock()
	ids := make([]string, 0, len(b.procs))
	for id := range b.procs {
		ids = append(ids, id)
	}
	b.mu.Unlock()

	for _, id := range ids {
		b.StopVM(context.Background(), id)
	}
	return nil
}

func (b *VfkitBackend) buildArgs(cfg *VMConfig, vmDir, mac string) []string {
	kernelPath := cfg.KernelPath
	if kernelPath == "" {
		kernelPath = b.cfg.KernelPath
	}

	// vfkit requires an initrd even with --bootloader. Create a minimal empty one.
	initrdPath := filepath.Join(vmDir, "empty-initrd.img")
	if _, err := os.Stat(initrdPath); os.IsNotExist(err) {
		os.WriteFile(initrdPath, []byte{}, 0644)
	}

	// Kernel-level DHCP — gets IP at ~0.2s during boot, before init runs.
	// vmnet NAT only routes to IPs it assigned, so DHCP is required.
	cmdline := "console=hvc0 root=/dev/vda rw ip=dhcp"

	sharedDir := filepath.Join(vmDir, "shared")

	args := []string{
		"--cpus", strconv.Itoa(int(cfg.VCPU)),
		"--memory", strconv.Itoa(int(cfg.MemoryMB)),
		"--bootloader", fmt.Sprintf("linux,kernel=%s,initrd=%s,cmdline=%s", kernelPath, initrdPath, cmdline),
		"--device", fmt.Sprintf("virtio-blk,path=%s", cfg.RootfsPath),
		"--device", fmt.Sprintf("virtio-net,nat,mac=%s", mac),
		"--device", "virtio-rng",
		"--device", fmt.Sprintf("virtio-serial,logFilePath=%s", filepath.Join(vmDir, "console.log")),
		"--device", fmt.Sprintf("virtio-fs,sharedDir=%s,mountTag=stockyard", sharedDir),
		"--restful-uri", fmt.Sprintf("unix://%s", filepath.Join(vmDir, "vfkit-rest.sock")),
	}

	return args
}

func writeAuthorizedKeys(vmDir string, cfg *VMConfig) error {
	sharedDir := filepath.Join(vmDir, "shared")
	if err := os.MkdirAll(sharedDir, 0755); err != nil {
		return err
	}

	if len(cfg.SSHAuthorizedKeys) == 0 {
		return nil
	}

	keys := strings.Join(cfg.SSHAuthorizedKeys, "\n") + "\n"
	return os.WriteFile(filepath.Join(sharedDir, "authorized_keys"), []byte(keys), 0644)
}

func generateMAC() string {
	buf := make([]byte, 5)
	rand.Read(buf)
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", buf[0], buf[1], buf[2], buf[3], buf[4])
}

func vfkitProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

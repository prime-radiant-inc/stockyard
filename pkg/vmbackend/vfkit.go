//go:build darwin

package vmbackend

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const macOSLeaseFile = "/var/db/dhcpd_leases"

// VfkitConfig holds configuration for the vfkit backend.
type VfkitConfig struct {
	VfkitBin   string
	KernelPath string
	InitrdPath string
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

	if err := writeCloudInit(vmDir, cfg); err != nil {
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("write cloud-init: %w", err)
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

	time.Sleep(100 * time.Millisecond)
	if !vfkitProcessRunning(cmd.Process.Pid) {
		stderrContent, _ := os.ReadFile(filepath.Join(vmDir, "stderr.log"))
		os.RemoveAll(vmDir)
		return nil, fmt.Errorf("vfkit exited immediately: %s", string(stderrContent))
	}

	ip, _ := waitForIP(mac, 30*time.Second)

	return &VMInfo{
		ID:        cfg.ID,
		PID:       cmd.Process.Pid,
		IP:        ip,
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

	proc.cmd.Process.Signal(syscall.SIGTERM)

	done := make(chan struct{})
	go func() {
		proc.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		proc.cmd.Process.Kill()
		<-done
	}

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

	initrdPath := b.cfg.InitrdPath

	args := []string{
		"--cpus", strconv.Itoa(int(cfg.VCPU)),
		"--memory", strconv.Itoa(int(cfg.MemoryMB)),
		"--kernel", kernelPath,
		"--initrd", initrdPath,
		"--kernel-cmdline", "console=hvc0 root=/dev/vda1 rw",
		"--device", fmt.Sprintf("virtio-blk,path=%s", cfg.RootfsPath),
		"--device", fmt.Sprintf("virtio-net,nat,mac=%s", mac),
		"--device", "virtio-rng",
		"--device", fmt.Sprintf("virtio-serial,logFilePath=%s", filepath.Join(vmDir, "console.log")),
		"--restful-uri", fmt.Sprintf("unix://%s", filepath.Join(vmDir, "vfkit-rest.sock")),
	}

	userDataPath := filepath.Join(vmDir, "user-data")
	metaDataPath := filepath.Join(vmDir, "meta-data")
	if _, err := os.Stat(userDataPath); err == nil {
		args = append(args, "--cloud-init", fmt.Sprintf("%s,%s", userDataPath, metaDataPath))
	}

	return args
}

func writeCloudInit(vmDir string, cfg *VMConfig) error {
	hostname := fmt.Sprintf("stockyard-%s", cfg.ID)

	metaData := fmt.Sprintf("instance-id: i-%s\nlocal-hostname: %s\n", cfg.ID, hostname)
	if err := os.WriteFile(filepath.Join(vmDir, "meta-data"), []byte(metaData), 0644); err != nil {
		return err
	}

	var userData strings.Builder
	userData.WriteString("#cloud-config\n")

	if len(cfg.SSHAuthorizedKeys) > 0 {
		userData.WriteString("ssh_authorized_keys:\n")
		for _, key := range cfg.SSHAuthorizedKeys {
			userData.WriteString(fmt.Sprintf("  - %s\n", key))
		}
	}

	return os.WriteFile(filepath.Join(vmDir, "user-data"), []byte(userData.String()), 0644)
}

func waitForIP(mac string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ip, err := FindIPByMAC(macOSLeaseFile, mac)
		if err == nil {
			return ip, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("timeout waiting for IP for MAC %s", mac)
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

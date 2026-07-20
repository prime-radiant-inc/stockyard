package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/obra/stockyard/pkg/firecracker"
	"github.com/obra/stockyard/pkg/network"
	"github.com/obra/stockyard/pkg/tailscale"
	"github.com/obra/stockyard/pkg/vmbackend"
)

// TaskManager handles the lifecycle of VM-based tasks.
type TaskManager struct {
	daemon  *Daemon
	backend vmbackend.Backend
}

// NewTaskManager creates a TaskManager with the given daemon and VM backend.
func NewTaskManager(d *Daemon, backend vmbackend.Backend) *TaskManager {
	return &TaskManager{
		daemon:  d,
		backend: backend,
	}
}

// CreateTaskRequest contains the parameters for creating a new task.
type CreateTaskRequest struct {
	Name              string
	Command           []string
	Env               map[string]string
	CPUs              int32
	MemoryMB          int32
	NoTailscale       bool
	TailscaleAuthKey  string   // Optional: overrides 1Password lookup
	SSHAuthorizedKeys []string // SSH public keys for VM access
	DotEnv            []byte   // Raw .env file bytes
	Image             string   // OCI image ref; empty = daemon default (PRI-2150)
}

// resolveTaskImage turns a per-task image request into the ref the task will
// actually run — never the empty string, so the stored record always names a
// real image. A nil validator means the backend cannot honor per-task images
// (PRI-2150 phase 1: only apple-container can).
func resolveTaskImage(ctx context.Context, requested, backendName, defaultImage string, validator vmbackend.ImageValidator) (string, error) {
	if requested == "" {
		return defaultImage, nil
	}
	if validator == nil {
		return "", fmt.Errorf("%s backend does not support per-task images yet (PRI-2150 phase 2)", backendName)
	}
	if err := validator.ValidateImage(ctx, requested); err != nil {
		return "", err
	}
	return requested, nil
}

// CreateTask creates a new VM-based task with the given parameters.
func (tm *TaskManager) CreateTask(ctx context.Context, req *CreateTaskRequest) (*Task, error) {
	// Apply defaults
	if req.CPUs <= 0 {
		req.CPUs = 2
	}
	if req.MemoryMB <= 0 {
		req.MemoryMB = 1024
	}

	// Resolve the task's image before allocating anything (PRI-2150).
	backendName := tm.daemon.cfg.Backend
	if backendName == "" {
		backendName = "firecracker"
	}
	defaultImage := "default" // Firecracker's registry name arrives in phase 2
	if backendName == "apple-container" {
		defaultImage = tm.daemon.cfg.AppleContainer.Image
	}
	// A nil tm.backend here means a backendless test daemon — real daemons
	// fail at startup if their backend can't be constructed — so a nil
	// validator can only misattribute the rejection message in tests.
	var validator vmbackend.ImageValidator
	if tm.daemon.images != nil {
		validator = tm.daemon.images // Firecracker: the registry validates
	} else {
		validator, _ = tm.backend.(vmbackend.ImageValidator)
	}
	resolvedImage, err := resolveTaskImage(ctx, req.Image, backendName, defaultImage, validator)
	if err != nil {
		return nil, err
	}

	// Generate task ID
	taskID := vmbackend.GenerateVMID()

	// Allocate static IP for the VM
	var staticIPArgs string
	var networkConfig *network.StaticNetworkConfig
	if tm.daemon.IPPool() != nil {
		if _, err := tm.daemon.IPPool().Allocate(taskID); err != nil {
			return nil, fmt.Errorf("allocate static IP: %w", err)
		}
		staticIPArgs = tm.daemon.IPPool().KernelIPArgs(taskID)
		networkConfig = tm.daemon.IPPool().NetworkConfig(taskID)
	}

	// Create ZFS dataset for workspace (Firecracker backend only)
	var workspacePath string
	datasetCreated := false
	if tm.daemon.zfs != nil && (tm.daemon.cfg.Backend == "" || tm.daemon.cfg.Backend == "firecracker") {
		if err := tm.daemon.zfs.CreateDataset(ctx, taskID); err != nil {
			return nil, errors.Join(
				fmt.Errorf("failed to create ZFS dataset: %w", err),
				tm.cleanupCreateTask(ctx, taskID, "", false),
			)
		}
		datasetCreated = true

		var err error
		workspacePath, err = tm.daemon.zfs.GetMountpoint(ctx, taskID)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("failed to get workspace mountpoint: %w", err),
				tm.cleanupCreateTask(ctx, taskID, "", datasetCreated),
			)
		}
	}

	// Build environment with secrets
	env := make(map[string]string)
	for k, v := range req.Env {
		env[k] = v
	}

	// Fetch secrets from provider
	secretNames := []string{"anthropic-api-key", "github-token"}
	for _, secretName := range secretNames {
		if secret, err := tm.daemon.secrets.GetSecret(ctx, secretName); err == nil {
			envKey := strings.ToUpper(strings.ReplaceAll(secretName, "-", "_"))
			env[envKey] = secret
		}
	}

	// Get Tailscale auth key if enabled
	var tailscaleAuthKey string
	var tailscaleHostname string
	if !req.NoTailscale {
		var key string
		if req.TailscaleAuthKey != "" {
			// Use provided auth key
			key = req.TailscaleAuthKey
		} else {
			// Fetch from secrets provider
			var err error
			key, err = tm.daemon.secrets.GetSecret(ctx, "tailscale-auth-key")
			if err != nil {
				log.Printf("Warning: could not get Tailscale auth key: %v", err)
				// Continue without Tailscale
			}
		}
		if key != "" {
			if err := tailscale.ValidateAuthKey(key); err != nil {
				log.Printf("Warning: invalid Tailscale auth key: %v", err)
				// Continue without Tailscale
			} else {
				tailscaleAuthKey = key
				tailscaleHostname = tailscale.BuildHostname(taskID)
			}
		}
	}

	// Generate hostname
	hostname := fmt.Sprintf("stockyard-%s", taskID)

	// Generate cloud-init config (Firecracker only)
	var cloudInitData string
	if tm.daemon.cfg.Backend == "" || tm.daemon.cfg.Backend == "firecracker" {
		cloudInitCfg := &firecracker.CloudInitConfig{
			Hostname:      hostname,
			Environment:   env,
			WorkspacePath: workspacePath,
		}

		var err error
		cloudInitData, err = cloudInitCfg.Generate()
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("failed to generate cloud-init config: %w", err),
				tm.cleanupCreateTask(ctx, taskID, "", datasetCreated),
			)
		}
	}

	// Resolve per-image snapshot path and kernel (Firecracker + registry only).
	// Per-image kernel applies at CREATE only; restart uses the shared kernel.
	// See docs/image-contract.md and the Task 5 commit for the known limitation.
	var rootfsSnapshot, imageKernel string
	if tm.daemon.images != nil {
		rec, err := tm.daemon.state.GetImage(resolvedImage)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("image %q disappeared during task creation: %w", resolvedImage, err),
				tm.cleanupCreateTask(ctx, taskID, "", datasetCreated),
			)
		}
		rootfsSnapshot = tm.daemon.images.snapshotPathFor(rec)
		imageKernel = rec.KernelPath
	}

	// Create VM if backend is available
	var vmID string
	var vmCID uint32
	var vmVsockPath string
	var vmIP string
	if tm.backend != nil {
		// Build backend-agnostic VM config
		vmEnv, vmMetadata := buildVMEnvMetadata(tm.daemon.cfg.Backend, taskID, req.Name,
			env, tailscaleAuthKey, hostname, staticIPArgs, networkConfig, req.DotEnv)

		vmCfg := &vmbackend.VMConfig{
			ID:                taskID,
			VCPU:              req.CPUs,
			MemoryMB:          req.MemoryMB,
			KernelPath:        imageKernel,
			RootfsSnapshot:    rootfsSnapshot,
			CloudInitData:     cloudInitData,
			SSHAuthorizedKeys: req.SSHAuthorizedKeys,
			DotEnv:            req.DotEnv,
			Env:               vmEnv,
			Metadata:          vmMetadata,
			Image:             resolvedImage,
		}

		vm, err := tm.backend.CreateVM(ctx, vmCfg)
		if err != nil {
			return nil, errors.Join(
				fmt.Errorf("failed to create VM: %w", err),
				tm.cleanupCreateTask(ctx, taskID, "", datasetCreated),
			)
		}
		vmID = vm.ID
		vmCID = vm.CID
		vmVsockPath = vm.VsockPath
		vmIP = vm.IP
	}

	// Determine command string for storage
	commandStr := ""
	if len(req.Command) > 0 {
		commandStr = strings.Join(req.Command, " ")
	}

	// Record task in database
	task := &Task{
		ID:                taskID,
		Name:              req.Name,
		Command:           commandStr,
		Status:            "running",
		VMID:              vmID,
		CID:               vmCID,
		VsockPath:         vmVsockPath,
		IP:                vmIP,
		TailscaleHostname: tailscaleHostname,
		Image:             resolvedImage,
		CreatedAt:         time.Now(),
	}

	if err := tm.daemon.state.CreateTask(task); err != nil {
		return nil, errors.Join(
			fmt.Errorf("failed to record task: %w", err),
			tm.cleanupCreateTask(ctx, taskID, vmID, datasetCreated),
		)
	}

	// Record activity event for VM started
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMStarted(taskID, req.Name, "")
	}

	// Start log tailing if dashboard is enabled
	if tm.daemon.logTailer != nil && vmID != "" {
		vmDir := filepath.Join(tm.daemon.cfg.Daemon.DataDir, "vms", "stockyard", vmID)
		tm.daemon.logTailer.TailFile(taskID, "stdout", filepath.Join(vmDir, "stdout.log"))
		tm.daemon.logTailer.TailFile(taskID, "stderr", filepath.Join(vmDir, "stderr.log"))
	}

	// Wait for Tailscale peer to come online before returning.
	// This ensures the caller can SSH to the Tailscale hostname immediately.
	if tailscaleHostname != "" {
		log.Printf("Waiting for Tailscale peer %s...", tailscaleHostname)
		waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
		defer waitCancel()
		// apple-container access is via `container exec`, not SSH, and the
		// container's in-netstack Tailscale SSH listener can lag well behind
		// "node online". Gate on online-only there; keep the SSH probe for the
		// Firecracker path, where sshd readiness is what callers wait on.
		var waitErr error
		if tm.daemon.cfg.Backend == "apple-container" {
			waitErr = tailscale.WaitForPeerOnline(waitCtx, tailscaleHostname, 60*time.Second)
		} else {
			waitErr = tailscale.WaitForPeer(waitCtx, tailscaleHostname, 60*time.Second)
		}
		if waitErr != nil {
			log.Printf("Warning: Tailscale peer %s not ready: %v", tailscaleHostname, waitErr)
			// Don't fail — VM is running and accessible via direct IP
		} else {
			log.Printf("Tailscale peer %s is online", tailscaleHostname)
		}
	}

	return task, nil
}

func (tm *TaskManager) cleanupCreateTask(ctx context.Context, taskID, vmID string, datasetCreated bool) error {
	var cleanupErrs []error
	canReleaseIP := true
	if tm.backend != nil && vmID != "" {
		if err := tm.backend.DeleteVM(ctx, vmID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete VM during task creation cleanup: %w", err))
			canReleaseIP = false
		}
	}
	if datasetCreated && tm.daemon.zfs != nil {
		if err := tm.daemon.zfs.DestroyDataset(ctx, taskID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("destroy workspace during task creation cleanup: %w", err))
		}
	}
	if canReleaseIP && tm.daemon.IPPool() != nil {
		if err := tm.daemon.IPPool().Release(taskID); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("release IP allocation during task creation cleanup: %w", err))
		}
	}
	return errors.Join(cleanupErrs...)
}

// parseDotEnv parses raw .env file bytes into a key→value map.
//
// Supported syntax:
//   - KEY=VALUE
//   - export KEY=VALUE  (optional "export " prefix)
//   - # comment lines   (ignored)
//   - blank lines        (ignored)
//   - Values may be optionally surrounded by single or double quotes, which
//     are stripped. No escape processing is performed inside quoted values.
func parseDotEnv(data []byte) map[string]string {
	result := make(map[string]string)
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip optional "export " prefix.
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue // no '=' or starts with '=' — skip
		}
		key := strings.TrimSpace(line[:idx])
		val := line[idx+1:]
		// Strip surrounding quotes (single or double) when they match.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key != "" {
			result[key] = val
		}
	}
	return result
}

// buildVMEnvMetadata constructs the backend-specific Env and Metadata maps for a
// VMConfig. The apple-container backend has no cloud-init or MMDS, so the entire
// workload environment must be delivered through Env (forwarded as `container run
// --env` flags) — including the secrets and the Tailscale auth key, which the
// container entrypoint reads as TAILSCALE_AUTH_KEY. Firecracker instead
// receives adapter-private underscore-prefixed keys that its adapter extracts;
// those keys are deliberately NOT set on the apple-container path so they cannot
// leak into `container inspect`.
//
// dotEnv contains the raw bytes of an optional .env file. For the
// apple-container path its entries are applied first (lowest precedence) and
// then overridden by the explicit task env (env), so that secrets and other
// caller-supplied values always win. dotEnv is ignored on the
// Firecracker path because that backend receives DotEnv via MMDS.
func buildVMEnvMetadata(backend, taskID, taskName string, env map[string]string,
	tailscaleAuthKey, hostname, staticIPArgs string,
	networkConfig *network.StaticNetworkConfig, dotEnv []byte) (map[string]string, map[string]string) {

	vmEnv := make(map[string]string)
	vmMetadata := map[string]string{
		"task-id":   taskID,
		"task-name": taskName,
	}

	if backend == "apple-container" {
		// Apply .env entries first (lowest precedence).
		if len(dotEnv) > 0 {
			for k, v := range parseDotEnv(dotEnv) {
				vmEnv[k] = v
			}
		}
		// Deliver the real workload environment directly, overriding .env values.
		for k, v := range env {
			vmEnv[k] = v
		}
		if tailscaleAuthKey != "" {
			vmEnv["TAILSCALE_AUTH_KEY"] = tailscaleAuthKey
		}
		vmEnv["STOCKYARD_HOSTNAME"] = hostname
		return vmEnv, vmMetadata
	}

	// Firecracker: pass adapter-private fields the Firecracker adapter extracts.
	// DotEnv is forwarded via VMConfig.DotEnv → MMDS, not parsed here.
	if tailscaleAuthKey != "" {
		vmEnv["_tailscale_auth_key"] = tailscaleAuthKey
	}
	if staticIPArgs != "" {
		vmEnv["_static_ip_args"] = staticIPArgs
	}
	if networkConfig != nil {
		vmMetadata["_network_ip"] = networkConfig.IP
		vmMetadata["_network_netmask"] = networkConfig.Netmask
		vmMetadata["_network_gateway"] = networkConfig.Gateway
		vmMetadata["_network_dns"] = networkConfig.DNS
	}
	return vmEnv, vmMetadata
}

// RestartTask restarts a stopped task by starting its VM again.
func (tm *TaskManager) RestartTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	if task.Status != "stopped" && task.Status != "failed" {
		return fmt.Errorf("%w: %s (status: %s)", ErrTaskNotStopped, taskID, task.Status)
	}

	// Update status to starting
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "starting"); err != nil {
		return err
	}

	// Start VM using existing workspace and rootfs.
	//
	// Apple-container note: StartVM calls `container start <name>`, which
	// restarts the *same* container. The container's environment (including
	// TAILSCALE_AUTH_KEY) was baked in at `container run` time and is
	// preserved across stop/start cycles; tailscaled's node state also
	// persists in the container's writable layer. Therefore no fresh
	// Tailscale auth key is needed here. Known limitation: if the container
	// is stopped for longer than Tailscale's node-key expiry the node will
	// need re-authorization — that edge case is not handled automatically.
	var vmInfo *vmbackend.VMInfo
	if tm.backend != nil && task.VMID != "" {
		vmCfg := &vmbackend.VMConfig{
			ID:       task.VMID,
			VCPU:     2,    // Default
			MemoryMB: 1024, // Default
		}

		var err error
		vmInfo, err = tm.backend.StartVM(ctx, vmCfg)
		if err != nil {
			// Revert status on failure
			tm.daemon.state.UpdateTaskStatus(taskID, "failed")
			return fmt.Errorf("failed to start VM: %w", err)
		}
	}

	// Update status to running
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "running"); err != nil {
		return err
	}

	// Store the VM's connection info
	if vmInfo != nil {
		if vmInfo.CID != 0 {
			if err := tm.daemon.state.UpdateTaskCID(taskID, vmInfo.CID); err != nil {
				log.Printf("Warning: failed to store CID for task %s: %v", taskID, err)
			}
		}
		if vmInfo.VsockPath != "" {
			if err := tm.daemon.state.UpdateTaskVsockPath(taskID, vmInfo.VsockPath); err != nil {
				log.Printf("Warning: failed to store vsock path for task %s: %v", taskID, err)
			}
		}
		if vmInfo.IP != "" {
			if err := tm.daemon.state.UpdateTaskIP(taskID, vmInfo.IP); err != nil {
				log.Printf("Warning: failed to store IP for task %s: %v", taskID, err)
			}
		}
	}

	// Start log tailing if dashboard is enabled
	if tm.daemon.logTailer != nil && task.VMID != "" {
		// VM logs are in the backend's state directory
		vmDir := filepath.Join(tm.daemon.cfg.Daemon.DataDir, "vms", "stockyard", task.VMID)
		tm.daemon.logTailer.TailFile(taskID, "stdout", filepath.Join(vmDir, "stdout.log"))
		tm.daemon.logTailer.TailFile(taskID, "stderr", filepath.Join(vmDir, "stderr.log"))
	}

	// Record activity event for VM started
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMStarted(taskID, task.Name, "")
	}

	return nil
}

// StopTask stops a running task by its ID.
func (tm *TaskManager) StopTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	// Stop log tailing
	if tm.daemon.logTailer != nil {
		tm.daemon.logTailer.StopTask(taskID)
	}

	// Stop metrics collection
	if tm.daemon.metricsPoller != nil {
		tm.daemon.metricsPoller.StopTaskMetrics(taskID)
	}

	// Stop VM if backend is available and task has a VM
	if tm.backend != nil && task.VMID != "" {
		if err := tm.backend.StopVM(ctx, task.VMID); err != nil {
			fmt.Printf("Warning: failed to stop VM %s: %v\n", task.VMID, err)
		}
	}

	// Update task status
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "stopped"); err != nil {
		return err
	}

	// Record activity event for VM stopped
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMStopped(taskID, task.Name)
	}

	return nil
}

// FailTask marks a task as failed with a reason.
// This is called when a VM crashes or becomes unresponsive.
func (tm *TaskManager) FailTask(ctx context.Context, taskID string, reason string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	// Stop log tailing
	if tm.daemon.logTailer != nil {
		tm.daemon.logTailer.StopTask(taskID)
	}

	// Stop metrics collection
	if tm.daemon.metricsPoller != nil {
		tm.daemon.metricsPoller.StopTaskMetrics(taskID)
	}

	// Update task status to failed
	if err := tm.daemon.state.UpdateTaskStatus(taskID, "failed"); err != nil {
		return err
	}

	// Record activity event for VM failed with specific reason
	if af := tm.daemon.ActivityFeed(); af != nil {
		af.VMFailed(taskID, task.Name, reason)
	}

	return nil
}

// DestroyTask destroys a task and its associated resources.
func (tm *TaskManager) DestroyTask(ctx context.Context, taskID string) error {
	task, err := tm.daemon.state.GetTask(taskID)
	if err != nil {
		return err
	}

	// Stop log tailing
	if tm.daemon.logTailer != nil {
		tm.daemon.logTailer.StopTask(taskID)
	}

	// Stop metrics collection
	if tm.daemon.metricsPoller != nil {
		tm.daemon.metricsPoller.StopTaskMetrics(taskID)
	}

	// Delete VM if backend is available and task has a VM
	if tm.backend != nil && task.VMID != "" {
		if err := tm.backend.DeleteVM(ctx, task.VMID); err != nil {
			fmt.Printf("Warning: failed to delete VM %s: %v\n", task.VMID, err)
		}
	}

	// Destroy ZFS dataset (Firecracker backend only)
	if tm.daemon.zfs != nil && (tm.daemon.cfg.Backend == "" || tm.daemon.cfg.Backend == "firecracker") {
		if err := tm.daemon.zfs.DestroyDataset(ctx, taskID); err != nil {
			fmt.Printf("Warning: failed to destroy ZFS dataset for %s: %v\n", taskID, err)
		}
	}

	// Record activity event for VM stopped (if it was running)
	if task.Status == "running" {
		if af := tm.daemon.ActivityFeed(); af != nil {
			af.VMStopped(taskID, task.Name)
		}
	}

	// Log Tailscale device cleanup (relies on ephemeral key expiration)
	if task.TailscaleHostname != "" {
		if err := tailscale.RemoveDevice(ctx, task.TailscaleHostname); err != nil {
			log.Printf("Warning: Tailscale cleanup for %s: %v", task.TailscaleHostname, err)
			// Don't fail - ephemeral keys handle cleanup
		}
	}

	// Release the static IP allocation
	if tm.daemon.IPPool() != nil {
		if err := tm.daemon.IPPool().Release(taskID); err != nil {
			return fmt.Errorf("release IP allocation: %w", err)
		}
	}

	// Delete task from database
	return tm.daemon.state.DeleteTask(taskID)
}

// DestroyTasksByImage destroys every task whose resolved image is name.
// Used by the image registry's scoped scorched-earth replace/remove.
func (tm *TaskManager) DestroyTasksByImage(ctx context.Context, image string) error {
	tasks, err := tm.daemon.state.ListTasks("")
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.Image != image {
			continue
		}
		if err := tm.DestroyTask(ctx, t.ID); err != nil {
			return fmt.Errorf("destroy task %s: %w", t.ID, err)
		}
	}
	return nil
}

// Close closes the task manager and releases resources.
func (tm *TaskManager) Close() error {
	if tm.backend != nil {
		return tm.backend.Close()
	}
	return nil
}

// GetVMMAC reads the MAC address for a VM from its state directory.
func (tm *TaskManager) GetVMMAC(namespace, vmID string) (string, error) {
	macPath := filepath.Join(tm.daemon.cfg.Daemon.DataDir, "vms", namespace, vmID, "mac_addr")
	data, err := os.ReadFile(macPath)
	if err != nil {
		return "", fmt.Errorf("failed to read MAC address: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// GetVMIP looks up a VM's IP address via DHCP leases.
func (tm *TaskManager) GetVMIP(namespace, vmID string) (string, error) {
	mac, err := tm.GetVMMAC(namespace, vmID)
	if err != nil {
		return "", err
	}

	if tm.daemon.DHCP() == nil {
		return "", fmt.Errorf("DHCP server not available")
	}

	ip, found := tm.daemon.DHCP().GetIPForMAC(mac)
	if !found {
		return "", fmt.Errorf("no DHCP lease found for MAC %s", mac)
	}
	return ip, nil
}

// parseMemory parses a memory string like "512m", "2g", "2GB" into megabytes.
// Returns 1024 (1GB) as default if the string is empty or invalid.
func parseMemory(s string) int32 {
	if s == "" {
		return 1024
	}

	s = strings.TrimSpace(strings.ToLower(s))

	// Check for gigabyte suffix
	if strings.HasSuffix(s, "gb") {
		s = strings.TrimSuffix(s, "gb")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val * 1024)
		}
		return 1024
	}
	if strings.HasSuffix(s, "g") {
		s = strings.TrimSuffix(s, "g")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val * 1024)
		}
		return 1024
	}

	// Check for megabyte suffix
	if strings.HasSuffix(s, "mb") {
		s = strings.TrimSuffix(s, "mb")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val)
		}
		return 1024
	}
	if strings.HasSuffix(s, "m") {
		s = strings.TrimSuffix(s, "m")
		if val, err := strconv.ParseInt(s, 10, 32); err == nil {
			return int32(val)
		}
		return 1024
	}

	// Plain number (assume megabytes)
	if val, err := strconv.ParseInt(s, 10, 32); err == nil {
		return int32(val)
	}

	return 1024
}

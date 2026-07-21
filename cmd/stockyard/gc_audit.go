package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/firecracker"
)

type orphanAuditTask struct {
	ID   string `json:"id"`
	VMID string `json:"vm_id"`
}

type orphanAuditResource struct {
	OwnerID string `json:"owner_id"`
	Name    string `json:"name"`
}

type orphanAuditProcess struct {
	OwnerID string `json:"owner_id"`
	PID     int    `json:"pid"`
	Running bool   `json:"running"`
}

type orphanAuditMismatch struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	OwnerID  string `json:"owner_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Running  bool   `json:"running,omitempty"`
}

type orphanAuditReadError struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// orphanAudit is the closed, read-only evidence document emitted by gc --json.
type orphanAudit struct {
	Tasks             []orphanAuditTask      `json:"tasks"`
	VMDirectories     []orphanAuditResource  `json:"vm_directories"`
	Processes         []orphanAuditProcess   `json:"processes"`
	RootfsDatasets    []orphanAuditResource  `json:"rootfs_datasets"`
	WorkspaceDatasets []orphanAuditResource  `json:"workspace_datasets"`
	Taps              []orphanAuditResource  `json:"taps"`
	IPAllocations     map[string]string      `json:"ip_allocations"`
	Mismatches        []orphanAuditMismatch  `json:"mismatches"`
	UnknownReads      []orphanAuditReadError `json:"unknown_reads"`
}

type orphanAuditReaders struct {
	listTasks             func(context.Context) ([]orphanAuditTask, error)
	listVMDirs            func(context.Context) ([]orphanAuditResource, error)
	listProcesses         func(context.Context) ([]orphanAuditProcess, error)
	listRootfsDatasets    func(context.Context) ([]orphanAuditResource, error)
	listWorkspaceDatasets func(context.Context) ([]orphanAuditResource, error)
	listTaps              func(context.Context) ([]orphanAuditResource, error)
	listIPAllocations     func(context.Context) (map[string]string, error)
}

func validateGCAuditFlags(orphans, dryRun, jsonOutput bool) error {
	if jsonOutput && (!orphans || !dryRun) {
		return errors.New("gc --json requires --orphans --dry-run")
	}
	return nil
}

func runOrphanAudit(ctx context.Context, readers orphanAuditReaders, output io.Writer) error {
	audit := orphanAudit{IPAllocations: make(map[string]string)}
	audit.Tasks = readAuditTasks(ctx, readers.listTasks, &audit)
	audit.VMDirectories = readAuditResources(ctx, "vm_directories", readers.listVMDirs, &audit)
	audit.Processes = readAuditProcesses(ctx, readers.listProcesses, &audit)
	audit.RootfsDatasets = readAuditResources(ctx, "rootfs_datasets", readers.listRootfsDatasets, &audit)
	audit.WorkspaceDatasets = readAuditResources(ctx, "workspace_datasets", readers.listWorkspaceDatasets, &audit)
	audit.Taps = readAuditResources(ctx, "taps", readers.listTaps, &audit)
	audit.IPAllocations = readAuditAllocations(ctx, readers.listIPAllocations, &audit)

	if !hasUnknownRead(audit.UnknownReads, "tasks") {
		reconcileOrphanAudit(&audit)
	}
	if err := json.NewEncoder(output).Encode(audit); err != nil {
		return fmt.Errorf("encode orphan audit: %w", err)
	}
	if len(audit.Mismatches) != 0 || len(audit.UnknownReads) != 0 {
		return fmt.Errorf("orphan audit found %d mismatch(es) and %d unknown read(s)", len(audit.Mismatches), len(audit.UnknownReads))
	}
	return nil
}

func readAuditTasks(ctx context.Context, read func(context.Context) ([]orphanAuditTask, error), audit *orphanAudit) []orphanAuditTask {
	if read == nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: "tasks", Error: "reader is not configured"})
		return []orphanAuditTask{}
	}
	items, err := read(ctx)
	if err != nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: "tasks", Error: err.Error()})
		return []orphanAuditTask{}
	}
	return items
}

func readAuditResources(ctx context.Context, source string, read func(context.Context) ([]orphanAuditResource, error), audit *orphanAudit) []orphanAuditResource {
	if read == nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: source, Error: "reader is not configured"})
		return []orphanAuditResource{}
	}
	items, err := read(ctx)
	if err != nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: source, Error: err.Error()})
		return []orphanAuditResource{}
	}
	return items
}

func readAuditProcesses(ctx context.Context, read func(context.Context) ([]orphanAuditProcess, error), audit *orphanAudit) []orphanAuditProcess {
	if read == nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: "processes", Error: "reader is not configured"})
		return []orphanAuditProcess{}
	}
	items, err := read(ctx)
	if err != nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: "processes", Error: err.Error()})
		return []orphanAuditProcess{}
	}
	return items
}

func readAuditAllocations(ctx context.Context, read func(context.Context) (map[string]string, error), audit *orphanAudit) map[string]string {
	if read == nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: "ip_allocations", Error: "reader is not configured"})
		return map[string]string{}
	}
	items, err := read(ctx)
	if err != nil {
		audit.UnknownReads = append(audit.UnknownReads, orphanAuditReadError{Source: "ip_allocations", Error: err.Error()})
		return map[string]string{}
	}
	if items == nil {
		return map[string]string{}
	}
	return items
}

func hasUnknownRead(reads []orphanAuditReadError, source string) bool {
	for _, read := range reads {
		if read.Source == source {
			return true
		}
	}
	return false
}

func reconcileOrphanAudit(audit *orphanAudit) {
	tasksByID := make(map[string]orphanAuditTask, len(audit.Tasks))
	tasksByVMID := make(map[string]orphanAuditTask, len(audit.Tasks))
	for _, task := range audit.Tasks {
		if task.ID == "" || task.VMID == "" {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "malformed", Resource: "task_row", OwnerID: task.ID, Detail: "task id and vm id are required"})
			continue
		}
		if _, exists := tasksByID[task.ID]; exists {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "duplicate", Resource: "task_row", OwnerID: task.ID})
			continue
		}
		if existing, exists := tasksByVMID[task.VMID]; exists {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "duplicate", Resource: "task_vm", OwnerID: task.VMID, Detail: existing.ID})
			continue
		}
		tasksByID[task.ID] = task
		tasksByVMID[task.VMID] = task
	}

	reconcileResources(audit, "vm_directory", audit.VMDirectories, tasksByID, tasksByVMID, true)
	reconcileProcesses(audit, audit.Processes, tasksByID, tasksByVMID)
	reconcileResources(audit, "rootfs_dataset", audit.RootfsDatasets, tasksByID, tasksByVMID, true)
	reconcileResources(audit, "workspace_dataset", audit.WorkspaceDatasets, tasksByID, tasksByVMID, false)
	reconcileResources(audit, "tap", audit.Taps, tasksByID, tasksByVMID, true)
	reconcileAllocations(audit, tasksByID, tasksByVMID)
	sort.Slice(audit.Mismatches, func(i, j int) bool {
		left, right := audit.Mismatches[i], audit.Mismatches[j]
		if left.Resource != right.Resource {
			return left.Resource < right.Resource
		}
		if left.OwnerID != right.OwnerID {
			return left.OwnerID < right.OwnerID
		}
		return left.Kind < right.Kind
	})
}

func reconcileResources(audit *orphanAudit, resource string, resources []orphanAuditResource, tasksByID, tasksByVMID map[string]orphanAuditTask, useVMID bool) {
	seen := make(map[string]struct{}, len(resources))
	for _, item := range resources {
		if item.OwnerID == "" {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "malformed", Resource: resource, Detail: item.Name})
			continue
		}
		if _, exists := seen[item.OwnerID]; exists {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "duplicate", Resource: resource, OwnerID: item.OwnerID, Detail: item.Name})
			continue
		}
		seen[item.OwnerID] = struct{}{}
		if useVMID {
			if _, exists := tasksByVMID[item.OwnerID]; exists {
				continue
			}
			if _, exists := tasksByID[item.OwnerID]; exists {
				audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "mismatched", Resource: resource, OwnerID: item.OwnerID, Detail: item.Name})
				continue
			}
		} else if _, exists := tasksByID[item.OwnerID]; exists {
			continue
		} else if _, exists := tasksByVMID[item.OwnerID]; exists {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "mismatched", Resource: resource, OwnerID: item.OwnerID, Detail: item.Name})
			continue
		}
		audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "orphan", Resource: resource, OwnerID: item.OwnerID, Detail: item.Name})
	}
}

func reconcileProcesses(audit *orphanAudit, processes []orphanAuditProcess, tasksByID, tasksByVMID map[string]orphanAuditTask) {
	seen := make(map[string]struct{}, len(processes))
	for _, process := range processes {
		if process.OwnerID == "" || process.PID <= 0 {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "malformed", Resource: "process", OwnerID: process.OwnerID, Detail: strconv.Itoa(process.PID), Running: process.Running})
			continue
		}
		key := process.OwnerID + "/" + strconv.Itoa(process.PID)
		if _, exists := seen[key]; exists {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "duplicate", Resource: "process", OwnerID: process.OwnerID, Detail: strconv.Itoa(process.PID), Running: process.Running})
			continue
		}
		seen[key] = struct{}{}
		if _, exists := tasksByVMID[process.OwnerID]; exists {
			continue
		}
		kind := "orphan"
		if _, exists := tasksByID[process.OwnerID]; exists {
			kind = "mismatched"
		}
		audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: kind, Resource: "process", OwnerID: process.OwnerID, Detail: strconv.Itoa(process.PID), Running: process.Running})
	}
}

func reconcileAllocations(audit *orphanAudit, tasksByID, tasksByVMID map[string]orphanAuditTask) {
	byIP := make(map[string]string, len(audit.IPAllocations))
	for taskID, ip := range audit.IPAllocations {
		if taskID == "" || net.ParseIP(ip) == nil {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "malformed", Resource: "ip_allocation", OwnerID: taskID, Detail: ip})
			continue
		}
		if otherTaskID, exists := byIP[ip]; exists {
			audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: "duplicate", Resource: "ip_allocation", OwnerID: taskID, Detail: otherTaskID + "=" + ip})
			continue
		}
		byIP[ip] = taskID
		if _, exists := tasksByID[taskID]; exists {
			continue
		}
		kind := "orphan"
		if _, exists := tasksByVMID[taskID]; exists {
			kind = "mismatched"
		}
		audit.Mismatches = append(audit.Mismatches, orphanAuditMismatch{Kind: kind, Resource: "ip_allocation", OwnerID: taskID, Detail: ip})
	}
}

func newSystemOrphanAuditReaders(cfg *config.Config, c *client.Client) orphanAuditReaders {
	vmDir := cfg.VMDir()
	return orphanAuditReaders{
		listTasks: func(ctx context.Context) ([]orphanAuditTask, error) {
			tasks, err := c.ListTasks(ctx, "")
			if err != nil {
				return nil, err
			}
			items := make([]orphanAuditTask, 0, len(tasks))
			for _, task := range tasks {
				if task == nil {
					items = append(items, orphanAuditTask{})
					continue
				}
				items = append(items, orphanAuditTask{ID: task.GetId(), VMID: task.GetVmId()})
			}
			return items, nil
		},
		listVMDirs: func(ctx context.Context) ([]orphanAuditResource, error) {
			return listAuditVMDirs(ctx, vmDir)
		},
		listProcesses: func(ctx context.Context) ([]orphanAuditProcess, error) {
			return listAuditProcesses(ctx, vmDir, firecracker.DefaultFirecrackerBin)
		},
		listRootfsDatasets: func(ctx context.Context) ([]orphanAuditResource, error) {
			return listAuditDatasets(ctx, cfg.ZFS.Pool+"/"+cfg.ZFS.VMsPath)
		},
		listWorkspaceDatasets: func(ctx context.Context) ([]orphanAuditResource, error) {
			return listAuditDatasets(ctx, cfg.ZFS.Pool+"/"+cfg.ZFS.BasePath)
		},
		listTaps: func(ctx context.Context) ([]orphanAuditResource, error) {
			return listAuditTaps(ctx, vmDir)
		},
		listIPAllocations: func(context.Context) (map[string]string, error) {
			return listAuditIPAllocations(filepath.Join(cfg.Daemon.DataDir, "ip_pool.json"))
		},
	}
}

func listAuditVMDirs(ctx context.Context, vmDir string) ([]orphanAuditResource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(vmDir)
	if err != nil {
		return nil, err
	}
	items := make([]orphanAuditResource, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() {
			items = append(items, orphanAuditResource{OwnerID: entry.Name(), Name: filepath.Join(vmDir, entry.Name())})
		}
	}
	return items, nil
}

func listAuditDatasets(ctx context.Context, base string) ([]orphanAuditResource, error) {
	cmd := exec.CommandContext(ctx, "zfs", "list", "-H", "-r", "-d", "1", "-o", "name", base)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	items := []orphanAuditResource{}
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if line == "" || line == base {
			continue
		}
		prefix := base + "/"
		if !strings.HasPrefix(line, prefix) || strings.Contains(strings.TrimPrefix(line, prefix), "/") {
			items = append(items, orphanAuditResource{Name: line})
			continue
		}
		items = append(items, orphanAuditResource{OwnerID: strings.TrimPrefix(line, prefix), Name: line})
	}
	return items, nil
}

func listAuditTaps(ctx context.Context, vmDir string) ([]orphanAuditResource, error) {
	entries, err := os.ReadDir(vmDir)
	if err != nil {
		return nil, err
	}
	tapOwners := make(map[string]string)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(vmDir, entry.Name(), "tap_name"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		tapName := strings.TrimSpace(string(data))
		if tapName == "" {
			return nil, fmt.Errorf("empty tap name for VM %s", entry.Name())
		}
		if _, exists := tapOwners[tapName]; exists {
			return nil, fmt.Errorf("duplicate tap name %s", tapName)
		}
		tapOwners[tapName] = entry.Name()
	}
	cmd := exec.CommandContext(ctx, "ip", "-o", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	items := []orphanAuditResource{}
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("malformed link row %q", line)
		}
		name := strings.TrimSuffix(fields[1], ":")
		if !strings.HasPrefix(name, "tap-") {
			continue
		}
		items = append(items, orphanAuditResource{OwnerID: tapOwners[name], Name: name})
	}
	return items, nil
}

func listAuditIPAllocations(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var persisted struct {
		Allocated map[string]string `json:"allocated"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("allocation state contains multiple JSON documents")
		}
		return nil, err
	}
	if persisted.Allocated == nil {
		return nil, errors.New("allocation state omits allocated map")
	}
	return persisted.Allocated, nil
}

func listAuditProcesses(ctx context.Context, vmDir, firecrackerBin string) ([]orphanAuditProcess, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("exact Firecracker process identity is unsupported on this platform")
	}
	expectedPath, err := exec.LookPath(firecrackerBin)
	if err != nil {
		return nil, err
	}
	expectedInfo, err := os.Stat(expectedPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	items := []orphanAuditProcess{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		arguments, err := readAuditProcessArguments(pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		socketPath, foundSocket, socketErr := auditProcessSocket(arguments)
		info, err := os.Stat(filepath.Join("/proc", entry.Name(), "exe"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !os.SameFile(expectedInfo, info) {
			continue
		}
		if !foundSocket || socketErr != nil {
			items = append(items, orphanAuditProcess{PID: pid, Running: true})
			continue
		}
		ownerID, ok := auditSocketOwner(vmDir, socketPath)
		if !ok {
			items = append(items, orphanAuditProcess{PID: pid, Running: true})
			continue
		}
		items = append(items, orphanAuditProcess{OwnerID: ownerID, PID: pid, Running: true})
	}
	return items, nil
}

func readAuditProcessArguments(pid int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	if len(fields) < 2 {
		return nil, nil
	}
	return fields[1:], nil
}

func auditProcessSocket(arguments []string) (string, bool, error) {
	var socket string
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] != "--api-sock" {
			continue
		}
		if arguments[index+1] == "" {
			return "", true, errors.New("empty --api-sock value")
		}
		if socket != "" {
			return "", true, errors.New("multiple --api-sock values")
		}
		socket = arguments[index+1]
	}
	if len(arguments) > 0 && arguments[len(arguments)-1] == "--api-sock" {
		return "", true, errors.New("missing --api-sock value")
	}
	return socket, socket != "", nil
}

func auditSocketOwner(vmDir, socketPath string) (string, bool) {
	relative, err := filepath.Rel(vmDir, socketPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	if len(parts) != 2 || parts[1] != "api.sock" || parts[0] == "." || parts[0] == "" {
		return "", false
	}
	return parts[0], true
}

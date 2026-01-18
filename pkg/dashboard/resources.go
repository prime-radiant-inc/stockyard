package dashboard

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Resource represents a system resource.
type Resource struct {
	ID      string
	Type    string // "vm", "zfs_vm", "zfs_workspace", "tap", "dhcp"
	Status  string // "active", "running", "stopped", "orphan"
	TaskID  string
	Details string
	Size    string // For ZFS datasets
}

// ResourceCollector gathers system resources.
type ResourceCollector struct {
	vmDir     string
	leaseFile string
	zfsPool   string
}

// NewResourceCollector creates a resource collector with the given paths.
func NewResourceCollector(vmDir, leaseFile, zfsPool string) *ResourceCollector {
	return &ResourceCollector{
		vmDir:     vmDir,
		leaseFile: leaseFile,
		zfsPool:   zfsPool,
	}
}

// Collect gathers all system resources.
func (rc *ResourceCollector) Collect(tasks []Task) ([]Resource, error) {
	taskStatus := make(map[string]string)
	for _, t := range tasks {
		taskStatus[t.ID] = t.Status
	}

	resources := []Resource{}
	resources = append(resources, rc.collectVMDirs(taskStatus)...)
	resources = append(resources, rc.collectTapInterfaces(taskStatus)...)
	resources = append(resources, rc.collectZFSDatasets(taskStatus)...)
	resources = append(resources, rc.collectDHCPLeases(taskStatus)...)

	return resources, nil
}

// collectVMDirs reads VM directories and returns resources.
func (rc *ResourceCollector) collectVMDirs(taskStatus map[string]string) []Resource {
	var resources []Resource

	entries, err := os.ReadDir(rc.vmDir)
	if err != nil {
		return resources
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		vmDir := filepath.Join(rc.vmDir, id)

		status := "stopped"
		if st, known := taskStatus[id]; known {
			status = st
		} else {
			status = "orphan"
		}

		details := vmDir
		macFile := filepath.Join(vmDir, "mac_addr")
		if data, err := os.ReadFile(macFile); err == nil {
			mac := strings.TrimSpace(string(data))
			details = vmDir + " (MAC: " + mac + ")"
		}

		resources = append(resources, Resource{
			ID:      id,
			Type:    "vm",
			Status:  status,
			Details: details,
		})
	}

	return resources
}

// collectTapInterfaces runs ip link show and finds tap-* interfaces.
func (rc *ResourceCollector) collectTapInterfaces(taskStatus map[string]string) []Resource {
	// Build map of tap name -> task ID from VM directories
	tapToTask := rc.buildTapToTaskMap()

	cmd := exec.Command("ip", "-o", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return []Resource{}
	}

	return parseTapOutput(string(output), tapToTask, taskStatus)
}

// parseTapOutput parses the output of "ip -o link show" and extracts tap interfaces.
// This is separated from collectTapInterfaces for testability.
func parseTapOutput(output string, tapToTask, taskStatus map[string]string) []Resource {
	var resources []Resource

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		// Format: "N: tap-xxxxxxxx: <FLAGS> ..."
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		name := strings.TrimSuffix(fields[1], ":")
		if !strings.HasPrefix(name, "tap-") {
			continue
		}

		status := "orphan"
		taskID := ""
		if tid, known := tapToTask[name]; known {
			taskID = tid
			if ts, ok := taskStatus[tid]; ok {
				if ts == "running" {
					status = "active"
				} else {
					status = ts
				}
			}
		}

		resources = append(resources, Resource{
			ID:     name,
			Type:   "tap",
			Status: status,
			TaskID: taskID,
		})
	}

	return resources
}

// buildTapToTaskMap reads tap_name files from VM directories.
func (rc *ResourceCollector) buildTapToTaskMap() map[string]string {
	tapToTask := make(map[string]string)

	entries, err := os.ReadDir(rc.vmDir)
	if err != nil {
		return tapToTask
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskID := entry.Name()
		tapFile := filepath.Join(rc.vmDir, taskID, "tap_name")
		if data, err := os.ReadFile(tapFile); err == nil {
			tapName := strings.TrimSpace(string(data))
			tapToTask[tapName] = taskID
		}
	}

	return tapToTask
}

// collectZFSDatasets runs zfs list for VM and workspace datasets.
func (rc *ResourceCollector) collectZFSDatasets(taskStatus map[string]string) []Resource {
	var resources []Resource

	// Collect VM datasets
	resources = append(resources, rc.collectZFSDatasetsFromPath(rc.zfsPool+"/vms", "zfs_vm", taskStatus)...)

	// Collect workspace datasets
	resources = append(resources, rc.collectZFSDatasetsFromPath(rc.zfsPool+"/workspaces", "zfs_workspace", taskStatus)...)

	return resources
}

// collectZFSDatasetsFromPath collects ZFS datasets from a specific path.
func (rc *ResourceCollector) collectZFSDatasetsFromPath(basePath, resourceType string, taskStatus map[string]string) []Resource {
	cmd := exec.Command("zfs", "list", "-H", "-r", "-d", "1", "-o", "name,used", basePath)
	output, err := cmd.Output()
	if err != nil {
		return []Resource{}
	}

	return parseZfsOutput(string(output), basePath, resourceType, taskStatus)
}

// parseZfsOutput parses the output of "zfs list" and extracts dataset information.
// This is separated from collectZFSDatasetsFromPath for testability.
func parseZfsOutput(output, basePath, resourceType string, taskStatus map[string]string) []Resource {
	var resources []Resource

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		name := fields[0]
		used := fields[1]

		// Skip the base path itself
		if name == basePath {
			continue
		}

		// Extract ID from dataset name
		parts := strings.Split(name, "/")
		if len(parts) == 0 {
			continue
		}
		id := parts[len(parts)-1]

		status := "orphan"
		if ts, known := taskStatus[id]; known {
			if ts == "running" {
				status = "active"
			} else {
				status = ts
			}
		}

		resources = append(resources, Resource{
			ID:      id,
			Type:    resourceType,
			Status:  status,
			TaskID:  id,
			Details: name,
			Size:    used,
		})
	}

	return resources
}

// collectDHCPLeases reads the dnsmasq lease file.
func (rc *ResourceCollector) collectDHCPLeases(taskStatus map[string]string) []Resource {
	var resources []Resource

	file, err := os.Open(rc.leaseFile)
	if err != nil {
		return resources
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// dnsmasq lease format: <expiry> <MAC> <IP> <hostname> <client-id>
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		expiry, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}

		mac := fields[1]
		ip := fields[2]
		hostname := fields[3]

		expiryTime := time.Unix(expiry, 0)
		status := "active"
		if time.Now().After(expiryTime) {
			status = "expired"
		}

		// Try to match hostname to a task
		taskID := ""
		if _, known := taskStatus[hostname]; known {
			taskID = hostname
		}

		resources = append(resources, Resource{
			ID:      ip,
			Type:    "dhcp",
			Status:  status,
			TaskID:  taskID,
			Details: "MAC: " + mac + ", Hostname: " + hostname,
		})
	}

	return resources
}

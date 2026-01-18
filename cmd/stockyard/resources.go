// cmd/stockyard/resources.go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

// Resource represents a stockyard resource (VM, dataset, etc.)
type Resource struct {
	ID       string
	Type     string // "vm", "zfs-vm", "zfs-workspace", "tap", "task"
	Status   string // "running", "stopped", "orphan"
	Size     string // For ZFS datasets
	Detail   string // Additional info
}

var resourcesCmd = &cobra.Command{
	Use:   "resources",
	Short: "Show all stockyard resources",
	Long: `Show all stockyard resources including VMs, ZFS datasets, tap interfaces,
and identify orphaned resources that are not tracked by the daemon.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Collect resources from various sources
		resources := &ResourceCollector{
			cfg:      cfg,
			vmDir:    "/var/lib/stockyard/vms/stockyard",
			taskIDs:  make(map[string]string), // id -> status
		}

		// Try to get tasks from daemon (optional - might not be running)
		if err := resources.collectFromDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not connect to daemon: %v\n", err)
		}

		// Collect from filesystem and ZFS
		resources.collectVMDirs()
		resources.collectZFSDatasets()
		resources.collectTapInterfaces()

		// Print results
		resources.print()

		return nil
	},
}

type ResourceCollector struct {
	cfg       *config.Config
	vmDir     string
	taskIDs   map[string]string // Known task IDs from daemon -> status
	resources []Resource
}

func (rc *ResourceCollector) collectFromDaemon() error {
	c, err := client.New(rc.cfg.Daemon.SocketPath)
	if err != nil {
		return err
	}
	defer c.Close()

	tasks, err := c.ListTasks(context.Background(), "")
	if err != nil {
		return err
	}

	for _, t := range tasks {
		rc.taskIDs[t.Id] = t.Status
		rc.resources = append(rc.resources, Resource{
			ID:     t.Id,
			Type:   "task",
			Status: t.Status,
			Detail: t.Repo,
		})
	}

	return nil
}

func (rc *ResourceCollector) collectVMDirs() {
	entries, err := os.ReadDir(rc.vmDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		vmDir := filepath.Join(rc.vmDir, id)

		status := "stopped"
		if rc.isVMRunning(vmDir) {
			status = "running"
		}

		// Check if this is an orphan (not in daemon state)
		if _, known := rc.taskIDs[id]; !known {
			status = "orphan"
		}

		rc.resources = append(rc.resources, Resource{
			ID:     id,
			Type:   "vm-dir",
			Status: status,
			Detail: vmDir,
		})
	}
}

func (rc *ResourceCollector) isVMRunning(vmDir string) bool {
	pidFile := filepath.Join(vmDir, "firecracker.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}

	// Check if process exists by sending signal 0
	// On Unix, this returns nil if process exists and we have permission to signal it
	err = syscall.Kill(pid, 0)
	return err == nil
}

func (rc *ResourceCollector) collectZFSDatasets() {
	pool := rc.cfg.ZFS.Pool

	// Collect VM datasets
	rc.collectZFSDatasetsFromPath(pool+"/"+rc.cfg.ZFS.VMsPath, "zfs-vm")

	// Collect workspace datasets
	rc.collectZFSDatasetsFromPath(pool+"/"+rc.cfg.ZFS.BasePath, "zfs-workspace")
}

func (rc *ResourceCollector) collectZFSDatasetsFromPath(basePath, resourceType string) {
	cmd := exec.Command("zfs", "list", "-H", "-r", "-o", "name,used", basePath)
	output, err := cmd.Output()
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
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

		// Check if orphan
		status := "active"
		if _, known := rc.taskIDs[id]; !known {
			status = "orphan"
		} else if rc.taskIDs[id] == "stopped" {
			status = "stopped"
		}

		rc.resources = append(rc.resources, Resource{
			ID:     id,
			Type:   resourceType,
			Status: status,
			Size:   used,
			Detail: name,
		})
	}
}

func (rc *ResourceCollector) collectTapInterfaces() {
	cmd := exec.Command("ip", "-o", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
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

		// Extract VM ID from tap name (tap-xxxxxxxx -> xxxxxxxx)
		id := strings.TrimPrefix(name, "tap-")

		// Check if orphan
		status := "active"
		found := false
		for taskID := range rc.taskIDs {
			if strings.HasPrefix(taskID, id) {
				found = true
				if rc.taskIDs[taskID] == "stopped" {
					status = "stopped"
				}
				break
			}
		}
		if !found {
			status = "orphan"
		}

		rc.resources = append(rc.resources, Resource{
			ID:     name,
			Type:   "tap",
			Status: status,
		})
	}
}

func (rc *ResourceCollector) print() {
	// Group by type
	byType := make(map[string][]Resource)
	for _, r := range rc.resources {
		byType[r.Type] = append(byType[r.Type], r)
	}

	typeOrder := []string{"task", "vm-dir", "zfs-vm", "zfs-workspace", "tap"}
	typeNames := map[string]string{
		"task":          "Tasks (from daemon)",
		"vm-dir":        "VM Directories",
		"zfs-vm":        "ZFS VM Datasets",
		"zfs-workspace": "ZFS Workspace Datasets",
		"tap":           "TAP Interfaces",
	}

	orphanCount := 0
	stoppedCount := 0
	runningCount := 0

	for _, typ := range typeOrder {
		resources := byType[typ]
		if len(resources) == 0 {
			continue
		}

		fmt.Printf("\n%s:\n", typeNames[typ])
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

		switch typ {
		case "task":
			fmt.Fprintln(w, "  ID\tSTATUS\tREPO")
			for _, r := range resources {
				fmt.Fprintf(w, "  %s\t%s\t%s\n", r.ID, r.Status, r.Detail)
			}
		case "vm-dir":
			fmt.Fprintln(w, "  ID\tSTATUS\tPATH")
			for _, r := range resources {
				fmt.Fprintf(w, "  %s\t%s\t%s\n", r.ID, r.Status, r.Detail)
			}
		case "zfs-vm", "zfs-workspace":
			fmt.Fprintln(w, "  ID\tSTATUS\tSIZE\tDATASET")
			for _, r := range resources {
				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", r.ID, r.Status, r.Size, r.Detail)
			}
		case "tap":
			fmt.Fprintln(w, "  INTERFACE\tSTATUS")
			for _, r := range resources {
				fmt.Fprintf(w, "  %s\t%s\n", r.ID, r.Status)
			}
		}
		w.Flush()

		for _, r := range resources {
			switch r.Status {
			case "orphan":
				orphanCount++
			case "stopped":
				stoppedCount++
			case "running":
				runningCount++
			}
		}
	}

	fmt.Printf("\nSummary: %d running, %d stopped, %d orphan resources\n", runningCount, stoppedCount, orphanCount)
	if orphanCount > 0 || stoppedCount > 0 {
		fmt.Println("Run 'stockyard gc' to clean up stopped resources")
		fmt.Println("Run 'stockyard gc --orphans' to clean up orphan resources")
	}
}

func init() {
	rootCmd.AddCommand(resourcesCmd)
}

// cmd/stockyard/gc.go
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

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmutil"
	"github.com/spf13/cobra"
)

var (
	gcAll        bool
	gcOrphans    bool
	gcEverything bool
	gcForce      bool
	gcDryRun     bool
)

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Clean up stopped and orphaned resources",
	Long: `Garbage collect stockyard resources.

By default, shows what would be cleaned up without making changes.

Use --all to clean up all stopped tasks (VMs, ZFS datasets, tap interfaces).
Use --orphans to clean up orphaned resources not tracked by the daemon.
Use --everything to clean up both stopped tasks and orphans.
Use --force to skip confirmation prompts.

Note: This command will NOT clean up running VMs - they must be stopped first.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check for root privileges
		if os.Geteuid() != 0 {
			fmt.Fprintln(os.Stderr, "Warning: gc requires root privileges for most operations (ZFS, network, process management)")
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// --everything implies both --all and --orphans
		if gcEverything {
			gcAll = true
			gcOrphans = true
		}

		gc := &GarbageCollector{
			cfg:          cfg,
			vmDir:        cfg.VMDir(),
			taskIDs:      make(map[string]string),
			dryRun:       gcDryRun || (!gcAll && !gcOrphans),
			cleanAll:     gcAll,
			cleanOrphans: gcOrphans,
		}

		// Get task list from daemon
		if err := gc.loadTasks(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not connect to daemon: %v\n", err)
			fmt.Fprintf(os.Stderr, "Only orphan cleanup is available without daemon.\n\n")
			if !gcOrphans {
				return fmt.Errorf("daemon connection required for --all; use --orphans for orphan cleanup")
			}
		}

		// Find resources to clean
		gc.findResources()

		if len(gc.toClean) == 0 {
			fmt.Println("No resources to clean up.")
			return nil
		}

		// Print what we'll clean
		gc.printPlan()

		if gc.dryRun {
			fmt.Println("\nDry run - no changes made.")
			fmt.Println("Use --all to clean stopped tasks, --orphans to clean orphans.")
			return nil
		}

		// Confirm unless --force
		if !gcForce {
			fmt.Printf("\nProceed with cleanup? [y/N] ")
			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		// Perform cleanup
		return gc.clean()
	},
}

type CleanupItem struct {
	ID       string
	Type     string // "task", "vm-dir", "zfs-vm", "zfs-workspace", "tap"
	Path     string // Dataset name or directory path
	IsOrphan bool
}

type GarbageCollector struct {
	cfg          *config.Config
	vmDir        string
	taskIDs      map[string]string // id -> status from daemon
	toClean      []CleanupItem
	dryRun       bool
	cleanAll     bool
	cleanOrphans bool
	client       *client.Client
}

func (gc *GarbageCollector) loadTasks() error {
	c, err := client.New(gc.cfg.Daemon.SocketPath)
	if err != nil {
		return err
	}
	gc.client = c

	tasks, err := c.ListTasks(context.Background(), "")
	if err != nil {
		c.Close()
		gc.client = nil
		return err
	}

	for _, t := range tasks {
		gc.taskIDs[t.Id] = t.Status
	}

	return nil
}

func (gc *GarbageCollector) findResources() {
	// Find stopped tasks (from daemon)
	if gc.cleanAll {
		for id, status := range gc.taskIDs {
			if status == "stopped" {
				gc.toClean = append(gc.toClean, CleanupItem{
					ID:       id,
					Type:     "task",
					IsOrphan: false,
				})
			}
		}
	}

	// Find orphan VM directories
	if gc.cleanOrphans {
		gc.findOrphanVMDirs()
		gc.findOrphanZFSDatasets()
		gc.findOrphanTaps()
	}
}

func (gc *GarbageCollector) findOrphanVMDirs() {
	entries, err := os.ReadDir(gc.vmDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()

		// Skip if known to daemon
		if _, known := gc.taskIDs[id]; known {
			continue
		}

		// Check if VM is running - skip if so
		vmDir := filepath.Join(gc.vmDir, id)
		if vmutil.IsVMRunning(vmDir) {
			fmt.Fprintf(os.Stderr, "Warning: orphan VM %s is running, skipping\n", id)
			continue
		}

		gc.toClean = append(gc.toClean, CleanupItem{
			ID:       id,
			Type:     "vm-dir",
			Path:     vmDir,
			IsOrphan: true,
		})
	}
}

func (gc *GarbageCollector) findOrphanZFSDatasets() {
	pool := gc.cfg.ZFS.Pool

	// Find orphan VM datasets
	gc.findOrphanDatasetsIn(pool+"/"+gc.cfg.ZFS.VMsPath, "zfs-vm")

	// Find orphan workspace datasets
	gc.findOrphanDatasetsIn(pool+"/"+gc.cfg.ZFS.BasePath, "zfs-workspace")
}

func (gc *GarbageCollector) findOrphanDatasetsIn(basePath, resourceType string) {
	cmd := exec.Command("zfs", "list", "-H", "-r", "-o", "name", basePath)
	output, err := cmd.Output()
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name == basePath {
			continue
		}

		// Extract ID
		parts := strings.Split(name, "/")
		if len(parts) == 0 {
			continue
		}
		id := parts[len(parts)-1]

		// Skip if known to daemon
		if _, known := gc.taskIDs[id]; known {
			continue
		}

		gc.toClean = append(gc.toClean, CleanupItem{
			ID:       id,
			Type:     resourceType,
			Path:     name,
			IsOrphan: true,
		})
	}
}

func (gc *GarbageCollector) findOrphanTaps() {
	// Build map of tap name -> task ID from VM directories
	tapToTask := gc.buildTapToTaskMap()

	cmd := exec.Command("ip", "-o", "link", "show")
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

		name := strings.TrimSuffix(fields[1], ":")
		if !strings.HasPrefix(name, "tap-") {
			continue
		}

		// Look up tap name in our map - if not found or task unknown, it's orphan
		if taskID, known := tapToTask[name]; known {
			if _, taskExists := gc.taskIDs[taskID]; taskExists {
				continue // tap belongs to a known task
			}
		}

		gc.toClean = append(gc.toClean, CleanupItem{
			ID:       name,
			Type:     "tap",
			IsOrphan: true,
		})
	}
}

// buildTapToTaskMap reads tap_name files from VM directories to build
// an exact mapping of tap interface names to task IDs.
func (gc *GarbageCollector) buildTapToTaskMap() map[string]string {
	tapToTask := make(map[string]string)
	entries, err := os.ReadDir(gc.vmDir)
	if err != nil {
		return tapToTask
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskID := entry.Name()
		tapFile := filepath.Join(gc.vmDir, taskID, "tap_name")
		if data, err := os.ReadFile(tapFile); err == nil {
			tapName := strings.TrimSpace(string(data))
			tapToTask[tapName] = taskID
		}
	}

	return tapToTask
}

func (gc *GarbageCollector) printPlan() {
	fmt.Println("Resources to clean up:")
	fmt.Println()

	// Group by type
	tasks := []CleanupItem{}
	vmDirs := []CleanupItem{}
	zfsVM := []CleanupItem{}
	zfsWorkspace := []CleanupItem{}
	taps := []CleanupItem{}

	for _, item := range gc.toClean {
		switch item.Type {
		case "task":
			tasks = append(tasks, item)
		case "vm-dir":
			vmDirs = append(vmDirs, item)
		case "zfs-vm":
			zfsVM = append(zfsVM, item)
		case "zfs-workspace":
			zfsWorkspace = append(zfsWorkspace, item)
		case "tap":
			taps = append(taps, item)
		}
	}

	if len(tasks) > 0 {
		fmt.Println("Stopped Tasks (will destroy via daemon):")
		for _, t := range tasks {
			fmt.Printf("  - %s\n", t.ID)
		}
		fmt.Println()
	}

	if len(vmDirs) > 0 {
		fmt.Println("Orphan VM Directories:")
		for _, t := range vmDirs {
			fmt.Printf("  - %s (%s)\n", t.ID, t.Path)
		}
		fmt.Println()
	}

	if len(zfsVM) > 0 {
		fmt.Println("Orphan ZFS VM Datasets:")
		for _, t := range zfsVM {
			fmt.Printf("  - %s\n", t.Path)
		}
		fmt.Println()
	}

	if len(zfsWorkspace) > 0 {
		fmt.Println("Orphan ZFS Workspace Datasets:")
		for _, t := range zfsWorkspace {
			fmt.Printf("  - %s\n", t.Path)
		}
		fmt.Println()
	}

	if len(taps) > 0 {
		fmt.Println("Orphan TAP Interfaces:")
		for _, t := range taps {
			fmt.Printf("  - %s\n", t.ID)
		}
		fmt.Println()
	}
}

func (gc *GarbageCollector) clean() error {
	var errors []string

	for _, item := range gc.toClean {
		var err error
		switch item.Type {
		case "task":
			err = gc.cleanTask(item)
		case "vm-dir":
			err = gc.cleanVMDir(item)
		case "zfs-vm", "zfs-workspace":
			err = gc.cleanZFSDataset(item)
		case "tap":
			err = gc.cleanTap(item)
		}

		if err != nil {
			errors = append(errors, fmt.Sprintf("%s %s: %v", item.Type, item.ID, err))
		} else {
			fmt.Printf("Cleaned: %s %s\n", item.Type, item.ID)
		}
	}

	if gc.client != nil {
		gc.client.Close()
	}

	if len(errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range errors {
			fmt.Printf("  - %s\n", e)
		}
		return fmt.Errorf("%d cleanup errors", len(errors))
	}

	fmt.Println("\nCleanup complete.")
	return nil
}

func (gc *GarbageCollector) cleanTask(item CleanupItem) error {
	if gc.client == nil {
		return fmt.Errorf("daemon not connected")
	}
	return gc.client.DestroyTask(context.Background(), item.ID)
}

func (gc *GarbageCollector) cleanVMDir(item CleanupItem) error {
	// First kill any running process
	pidFile := filepath.Join(item.Path, "firecracker.pid")
	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Kill()
			}
		}
	}

	// Delete tap if present
	tapFile := filepath.Join(item.Path, "tap_name")
	if data, err := os.ReadFile(tapFile); err == nil {
		tapName := strings.TrimSpace(string(data))
		_ = exec.Command("ip", "link", "delete", tapName).Run()
	}

	// Remove directory
	return os.RemoveAll(item.Path)
}

func (gc *GarbageCollector) cleanZFSDataset(item CleanupItem) error {
	// Use -r to recursively destroy snapshots
	cmd := exec.Command("zfs", "destroy", "-r", item.Path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(output))
	}
	return nil
}

func (gc *GarbageCollector) cleanTap(item CleanupItem) error {
	cmd := exec.Command("ip", "link", "delete", item.ID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore "Cannot find device" - already deleted
		if strings.Contains(string(output), "Cannot find device") {
			return nil
		}
		return fmt.Errorf("%v: %s", err, string(output))
	}
	return nil
}

func init() {
	gcCmd.Flags().BoolVar(&gcAll, "all", false, "Clean up all stopped tasks")
	gcCmd.Flags().BoolVar(&gcOrphans, "orphans", false, "Clean up orphaned resources")
	gcCmd.Flags().BoolVar(&gcEverything, "everything", false, "Clean up both stopped tasks and orphans")
	gcCmd.Flags().BoolVarP(&gcForce, "force", "f", false, "Skip confirmation prompts")
	gcCmd.Flags().BoolVar(&gcDryRun, "dry-run", false, "Show what would be cleaned without making changes")
	rootCmd.AddCommand(gcCmd)
}

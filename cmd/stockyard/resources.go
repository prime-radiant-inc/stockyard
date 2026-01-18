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
	"text/tabwriter"
	"time"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmutil"
	"github.com/spf13/cobra"
)

// Resource represents a stockyard resource (VM, dataset, etc.)
type Resource struct {
	ID       string
	Type     string // "vm", "zfs-vm", "zfs-workspace", "tap", "task", "dhcp-lease"
	Status   string // "running", "stopped", "orphan", "active"
	Size     string // For ZFS datasets
	Detail   string // Additional info
}

// TaskInfo holds extended task information
type TaskInfo struct {
	ID        string
	Status    string
	Repo      string
	CreatedAt time.Time
	IP        string
	MAC       string
}

// DHCPLease represents a DHCP lease entry
type DHCPLease struct {
	Expiry   time.Time
	MAC      string
	IP       string
	Hostname string
}

var resourcesVerbose bool

var resourcesCmd = &cobra.Command{
	Use:   "resources",
	Short: "Show all stockyard resources",
	Long: `Show all stockyard resources including VMs, ZFS datasets, tap interfaces,
DHCP leases, and identify orphaned resources that are not tracked by the daemon.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Collect resources from various sources
		rc := &ResourceCollector{
			cfg:       cfg,
			vmDir:     cfg.VMDir(),
			leaseFile: cfg.DHCPLeaseFile(),
			taskIDs:   make(map[string]string),
			taskInfo:  make(map[string]*TaskInfo),
			macToIP:   make(map[string]string),
			verbose:   resourcesVerbose,
		}

		// Load DHCP leases first (for IP lookup)
		rc.loadDHCPLeases()

		// Try to get tasks from daemon (optional - might not be running)
		if err := rc.collectFromDaemon(); err != nil {
			if resourcesVerbose {
				fmt.Fprintf(os.Stderr, "Warning: could not connect to daemon: %v\n", err)
			}
		}

		// Collect from filesystem and ZFS
		rc.collectVMDirs()
		rc.collectZFSDatasets()
		rc.collectTapInterfaces()

		// Print results
		rc.print()

		return nil
	},
}

type ResourceCollector struct {
	cfg       *config.Config
	vmDir     string
	leaseFile string
	taskIDs   map[string]string    // Known task IDs from daemon -> status
	taskInfo  map[string]*TaskInfo // Extended task info
	resources []Resource
	leases    []DHCPLease
	macToIP   map[string]string // MAC -> IP mapping from DHCP leases
	verbose   bool
}

func (rc *ResourceCollector) loadDHCPLeases() {
	file, err := os.Open(rc.leaseFile)
	if err != nil {
		if rc.verbose {
			fmt.Fprintf(os.Stderr, "Warning: could not open DHCP lease file: %v\n", err)
		}
		return
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

		lease := DHCPLease{
			Expiry:   time.Unix(expiry, 0),
			MAC:      strings.ToLower(fields[1]),
			IP:       fields[2],
			Hostname: fields[3],
		}
		rc.leases = append(rc.leases, lease)
		rc.macToIP[lease.MAC] = lease.IP
	}
}

func (rc *ResourceCollector) collectFromDaemon() error {
	c, err := getClient()
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

		// Parse created time
		createdAt, _ := time.Parse(time.RFC3339, t.CreatedAt)

		info := &TaskInfo{
			ID:        t.Id,
			Status:    t.Status,
			Repo:      t.Repo,
			CreatedAt: createdAt,
		}

		// Try to get MAC and IP for this task
		macFile := filepath.Join(rc.vmDir, t.Id, "mac_addr")
		if data, err := os.ReadFile(macFile); err == nil {
			mac := strings.ToLower(strings.TrimSpace(string(data)))
			info.MAC = mac
			if ip, ok := rc.macToIP[mac]; ok {
				info.IP = ip
			}
		}

		rc.taskInfo[t.Id] = info
	}

	return nil
}

func (rc *ResourceCollector) collectVMDirs() {
	entries, err := os.ReadDir(rc.vmDir)
	if err != nil {
		if rc.verbose {
			fmt.Fprintf(os.Stderr, "Warning: could not read VM directory %s: %v\n", rc.vmDir, err)
		}
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		vmDir := filepath.Join(rc.vmDir, id)

		status := "stopped"
		if vmutil.IsVMRunning(vmDir) {
			status = "running"
		}

		// Check if this is an orphan (not in daemon state)
		if _, known := rc.taskIDs[id]; !known {
			status = "orphan"
		}

		// Get additional VM info
		detail := vmDir
		macFile := filepath.Join(vmDir, "mac_addr")
		if data, err := os.ReadFile(macFile); err == nil {
			mac := strings.ToLower(strings.TrimSpace(string(data)))
			if ip, ok := rc.macToIP[mac]; ok {
				detail = fmt.Sprintf("%s (IP: %s)", vmDir, ip)
			}
		}

		rc.resources = append(rc.resources, Resource{
			ID:     id,
			Type:   "vm-dir",
			Status: status,
			Detail: detail,
		})
	}
}

func (rc *ResourceCollector) collectZFSDatasets() {
	pool := rc.cfg.ZFS.Pool

	// Collect VM datasets
	rc.collectZFSDatasetsFromPath(pool+"/"+rc.cfg.ZFS.VMsPath, "zfs-vm")

	// Collect workspace datasets
	rc.collectZFSDatasetsFromPath(pool+"/"+rc.cfg.ZFS.BasePath, "zfs-workspace")
}

func (rc *ResourceCollector) collectZFSDatasetsFromPath(basePath, resourceType string) {
	cmd := exec.Command("zfs", "list", "-H", "-r", "-d", "1", "-o", "name,used", basePath)
	output, err := cmd.Output()
	if err != nil {
		if rc.verbose {
			fmt.Fprintf(os.Stderr, "Warning: could not list ZFS datasets %s: %v\n", basePath, err)
		}
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
	// Build map of tap name -> task ID from VM directories
	tapToTask := rc.buildTapToTaskMap()

	cmd := exec.Command("ip", "-o", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		if rc.verbose {
			fmt.Fprintf(os.Stderr, "Warning: could not list network interfaces: %v\n", err)
		}
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

		// Look up tap name in our map
		status := "orphan"
		if taskID, known := tapToTask[name]; known {
			if taskStatus, ok := rc.taskIDs[taskID]; ok {
				status = taskStatus
				if status == "running" {
					status = "active"
				}
			}
		}

		rc.resources = append(rc.resources, Resource{
			ID:     name,
			Type:   "tap",
			Status: status,
		})
	}
}

// buildTapToTaskMap reads tap_name files from VM directories to build
// an exact mapping of tap interface names to task IDs.
func (rc *ResourceCollector) buildTapToTaskMap() map[string]string {
	tapToTask := make(map[string]string)
	entries, err := os.ReadDir(rc.vmDir)
	if err != nil {
		if rc.verbose {
			fmt.Fprintf(os.Stderr, "Warning: could not read VM directory for tap mapping: %v\n", err)
		}
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

func (rc *ResourceCollector) print() {
	orphanCount := 0
	stoppedCount := 0
	runningCount := 0

	// Print tasks with enhanced info
	if len(rc.taskInfo) > 0 {
		fmt.Printf("\nTasks (from daemon):\n")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  ID\tSTATUS\tIP\tUPTIME\tREPO")
		for _, info := range rc.taskInfo {
			uptime := "-"
			if !info.CreatedAt.IsZero() {
				uptime = formatDuration(time.Since(info.CreatedAt))
			}
			ip := info.IP
			if ip == "" {
				ip = "-"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n", info.ID, info.Status, ip, uptime, info.Repo)

			switch info.Status {
			case "running":
				runningCount++
			case "stopped":
				stoppedCount++
			}
		}
		w.Flush()
	}

	// Print DHCP leases
	if len(rc.leases) > 0 {
		fmt.Printf("\nDHCP Leases:\n")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  IP\tMAC\tHOSTNAME\tEXPIRES")
		for _, lease := range rc.leases {
			expires := formatDuration(time.Until(lease.Expiry))
			if time.Until(lease.Expiry) < 0 {
				expires = "expired"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", lease.IP, lease.MAC, lease.Hostname, expires)
		}
		w.Flush()
	}

	// Group remaining resources by type
	byType := make(map[string][]Resource)
	for _, r := range rc.resources {
		byType[r.Type] = append(byType[r.Type], r)
	}

	typeOrder := []string{"vm-dir", "zfs-vm", "zfs-workspace", "tap"}
	typeNames := map[string]string{
		"vm-dir":        "VM Directories",
		"zfs-vm":        "ZFS VM Datasets",
		"zfs-workspace": "ZFS Workspace Datasets",
		"tap":           "TAP Interfaces",
	}

	for _, typ := range typeOrder {
		resources := byType[typ]
		if len(resources) == 0 {
			continue
		}

		fmt.Printf("\n%s:\n", typeNames[typ])
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

		switch typ {
		case "vm-dir":
			fmt.Fprintln(w, "  ID\tSTATUS\tDETAIL")
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
		fmt.Println("Run 'stockyard gc --everything' to clean up all stopped and orphan resources")
	}
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		hours := int(d.Hours())
		mins := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func init() {
	resourcesCmd.Flags().BoolVarP(&resourcesVerbose, "verbose", "v", false, "Show verbose output including warnings")
	rootCmd.AddCommand(resourcesCmd)
}

package daemon

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// HostMetrics contains host system metrics.
type HostMetrics struct {
	CPUPercent       float64
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	NetworkRxBytes   int64
	NetworkTxBytes   int64
	DiskReadBytes    int64
	DiskWriteBytes   int64
}

// HostMetricsCollector collects host system metrics from /proc.
type HostMetricsCollector struct {
	lastCPUStats  cpuStats
	lastTimestamp time.Time
}

type cpuStats struct {
	user, nice, system, idle, iowait, irq, softirq int64
}

// NewHostMetricsCollector creates a new host metrics collector.
func NewHostMetricsCollector() *HostMetricsCollector {
	return &HostMetricsCollector{}
}

// Collect gathers current host metrics.
func (c *HostMetricsCollector) Collect() (*HostMetrics, error) {
	metrics := &HostMetrics{}
	var criticalErrors []string

	// CPU from /proc/stat (critical)
	cpuPercent, err := c.collectCPU()
	if err != nil {
		criticalErrors = append(criticalErrors, fmt.Sprintf("cpu: %v", err))
	} else {
		metrics.CPUPercent = cpuPercent
	}

	// Memory from /proc/meminfo (critical)
	memUsed, memTotal, err := c.collectMemory()
	if err != nil {
		criticalErrors = append(criticalErrors, fmt.Sprintf("memory: %v", err))
	} else {
		metrics.MemoryUsedBytes = memUsed
		metrics.MemoryTotalBytes = memTotal
	}

	// Network from /proc/net/dev (optional - just log errors)
	rxBytes, txBytes, err := c.collectNetwork()
	if err == nil {
		metrics.NetworkRxBytes = rxBytes
		metrics.NetworkTxBytes = txBytes
	}

	// Disk I/O from /proc/diskstats (optional - just log errors)
	readBytes, writeBytes, err := c.collectDiskIO()
	if err == nil {
		metrics.DiskReadBytes = readBytes
		metrics.DiskWriteBytes = writeBytes
	}

	if len(criticalErrors) > 0 {
		return metrics, fmt.Errorf("failed to collect critical metrics: %s", strings.Join(criticalErrors, "; "))
	}

	return metrics, nil
}

// collectCPU reads /proc/stat and calculates CPU usage percentage.
func (c *HostMetricsCollector) collectCPU() (float64, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}

			user, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				fmt.Printf("Warning: failed to parse CPU user: %v\n", err)
				continue
			}
			nice, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil {
				fmt.Printf("Warning: failed to parse CPU nice: %v\n", err)
				continue
			}
			system, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil {
				fmt.Printf("Warning: failed to parse CPU system: %v\n", err)
				continue
			}
			idle, err := strconv.ParseInt(fields[4], 10, 64)
			if err != nil {
				fmt.Printf("Warning: failed to parse CPU idle: %v\n", err)
				continue
			}
			iowait, err := strconv.ParseInt(fields[5], 10, 64)
			if err != nil {
				fmt.Printf("Warning: failed to parse CPU iowait: %v\n", err)
				continue
			}
			irq, err := strconv.ParseInt(fields[6], 10, 64)
			if err != nil {
				fmt.Printf("Warning: failed to parse CPU irq: %v\n", err)
				continue
			}
			softirq, err := strconv.ParseInt(fields[7], 10, 64)
			if err != nil {
				fmt.Printf("Warning: failed to parse CPU softirq: %v\n", err)
				continue
			}

			current := cpuStats{user, nice, system, idle, iowait, irq, softirq}
			now := time.Now()

			// Calculate delta from previous reading
			var cpuPercent float64
			if !c.lastTimestamp.IsZero() {
				deltaUser := current.user - c.lastCPUStats.user
				deltaNice := current.nice - c.lastCPUStats.nice
				deltaSystem := current.system - c.lastCPUStats.system
				deltaIdle := current.idle - c.lastCPUStats.idle
				deltaIowait := current.iowait - c.lastCPUStats.iowait
				deltaIrq := current.irq - c.lastCPUStats.irq
				deltaSoftirq := current.softirq - c.lastCPUStats.softirq

				totalDelta := deltaUser + deltaNice + deltaSystem + deltaIdle + deltaIowait + deltaIrq + deltaSoftirq
				activeDelta := deltaUser + deltaNice + deltaSystem + deltaIrq + deltaSoftirq

				if totalDelta > 0 {
					cpuPercent = float64(activeDelta) / float64(totalDelta) * 100.0
				}
			}

			c.lastCPUStats = current
			c.lastTimestamp = now
			return cpuPercent, nil
		}
	}

	return 0, scanner.Err()
}

// collectMemory reads /proc/meminfo and returns used and total memory.
func (c *HostMetricsCollector) collectMemory() (used, total int64, err error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	var memTotal, memAvailable int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			fmt.Printf("Warning: failed to parse memory value for %s: %v\n", fields[0], err)
			continue
		}
		// Values in /proc/meminfo are in kB
		valueBytes := value * 1024

		switch fields[0] {
		case "MemTotal:":
			memTotal = valueBytes
		case "MemAvailable:":
			memAvailable = valueBytes
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}

	return memTotal - memAvailable, memTotal, nil
}

// collectNetwork reads /proc/net/dev and returns total RX/TX bytes.
func (c *HostMetricsCollector) collectNetwork() (rx, tx int64, err error) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip header lines
		if strings.Contains(line, "|") {
			continue
		}

		// Format: interface: rx_bytes rx_packets ... tx_bytes tx_packets ...
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		// Skip loopback
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}

		rxBytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			fmt.Printf("Warning: failed to parse network RX bytes for %s: %v\n", iface, err)
			continue
		}
		txBytes, err := strconv.ParseInt(fields[8], 10, 64)
		if err != nil {
			fmt.Printf("Warning: failed to parse network TX bytes for %s: %v\n", iface, err)
			continue
		}

		rx += rxBytes
		tx += txBytes
	}

	return rx, tx, scanner.Err()
}

// collectDiskIO reads /proc/diskstats and returns total read/write bytes.
func (c *HostMetricsCollector) collectDiskIO() (read, write int64, err error) {
	file, err := os.Open("/proc/diskstats")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}

		// fields[2] is device name
		deviceName := fields[2]

		// Only count physical disks (sda, nvme0n1, vda, etc.)
		// Skip partitions (sda1, nvme0n1p1) and loop devices
		if !isPhysicalDisk(deviceName) {
			continue
		}

		// fields[5] = sectors read, fields[9] = sectors written
		// Each sector is typically 512 bytes
		sectorsRead, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			fmt.Printf("Warning: failed to parse disk read sectors for %s: %v\n", deviceName, err)
			continue
		}
		sectorsWritten, err := strconv.ParseInt(fields[9], 10, 64)
		if err != nil {
			fmt.Printf("Warning: failed to parse disk write sectors for %s: %v\n", deviceName, err)
			continue
		}

		read += sectorsRead * 512
		write += sectorsWritten * 512
	}

	return read, write, scanner.Err()
}

// isPhysicalDisk returns true if the device name appears to be a physical disk.
func isPhysicalDisk(name string) bool {
	// Skip loop devices
	if strings.HasPrefix(name, "loop") {
		return false
	}
	// Skip dm- (device mapper) devices
	if strings.HasPrefix(name, "dm-") {
		return false
	}
	// Skip ram disks
	if strings.HasPrefix(name, "ram") {
		return false
	}
	// Skip zram
	if strings.HasPrefix(name, "zram") {
		return false
	}

	// For sd* devices, only include if no trailing digits (sda not sda1)
	if strings.HasPrefix(name, "sd") {
		// sda, sdb, etc. have length 3
		return len(name) == 3
	}

	// For nvme*n* devices, exclude partitions (nvme0n1p1)
	if strings.HasPrefix(name, "nvme") {
		return !strings.Contains(name, "p")
	}

	// For vd* (virtio) devices
	if strings.HasPrefix(name, "vd") {
		return len(name) == 3
	}

	// For xvd* (Xen) devices
	if strings.HasPrefix(name, "xvd") {
		return len(name) == 4
	}

	return false
}

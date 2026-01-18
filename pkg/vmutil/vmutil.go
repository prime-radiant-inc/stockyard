// Package vmutil provides shared utilities for VM management.
package vmutil

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// IsVMRunning checks if a VM in the given directory is running by checking
// if the process in firecracker.pid exists and is signalable.
func IsVMRunning(vmDir string) bool {
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
	err = syscall.Kill(pid, 0)
	return err == nil
}

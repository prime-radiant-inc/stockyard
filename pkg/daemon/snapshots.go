// Package daemon provides the snapshot service for handling VM snapshot requests.
package daemon

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/obra/stockyard/pkg/vsock"
)

// SnapshotService handles snapshot requests from VMs
type SnapshotService struct {
	daemon *Daemon
	server *vsock.SnapshotServer
	ctx    context.Context
}

// NewSnapshotService creates a new snapshot service
func NewSnapshotService(d *Daemon) *SnapshotService {
	ss := &SnapshotService{daemon: d}
	ss.server = vsock.NewSnapshotServer(ss.handleSnapshot)
	return ss
}

// Start starts the snapshot service
func (ss *SnapshotService) Start(ctx context.Context) error {
	ss.ctx = ctx
	return ss.server.Listen(ctx)
}

// handleSnapshot handles a snapshot request from a VM
func (ss *SnapshotService) handleSnapshot(vmID, label string) error {
	// Map VM ID (CID) to task ID
	taskID, err := ss.resolveTaskID(vmID)
	if err != nil {
		return fmt.Errorf("unknown VM: %w", err)
	}

	log.Printf("Creating snapshot for task %s: %s", taskID, label)

	// Create ZFS snapshot
	snapName, err := ss.daemon.zfs.CreateSnapshot(ss.ctx, taskID, label)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	log.Printf("Created snapshot: %s", snapName)

	// Record in database. We log but don't fail here because:
	// 1. The ZFS snapshot (the actual data) was successfully created
	// 2. The database is just an index/cache that can be rebuilt
	// 3. Returning an error would tell the VM to retry, but the snapshot already exists
	if err := ss.daemon.state.RecordSnapshot(taskID, snapName); err != nil {
		log.Printf("Warning: failed to record snapshot in database: %v (ZFS snapshot %s was created)", err, snapName)
	}

	return nil
}

// resolveTaskID maps a VM identifier to a task ID
func (ss *SnapshotService) resolveTaskID(vmID string) (string, error) {
	// For vsock, vmID is like "cid-123"
	// For unix fallback, vmID is "unix-client" (for testing)

	if vmID == "unix-client" {
		// Testing fallback
		tasks, err := ss.daemon.state.ListTasks("running")
		if err != nil || len(tasks) == 0 {
			return "", fmt.Errorf("no running tasks")
		}
		return tasks[0].ID, nil
	}

	if strings.HasPrefix(vmID, "cid-") {
		cidStr := strings.TrimPrefix(vmID, "cid-")
		cid, err := strconv.ParseUint(cidStr, 10, 32)
		if err != nil {
			return "", fmt.Errorf("invalid CID: %s", vmID)
		}

		task, err := ss.daemon.state.GetTaskByCID(uint32(cid))
		if err != nil {
			return "", err
		}
		return task.ID, nil
	}

	return "", fmt.Errorf("unknown VM ID format: %s", vmID)
}

// Package daemon provides the snapshot service for handling VM snapshot requests.
package daemon

import (
	"context"
	"fmt"
	"log"
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
		// In testing, just use the first running task
		tasks, err := ss.daemon.state.ListTasks("running")
		if err != nil || len(tasks) == 0 {
			return "", fmt.Errorf("no running tasks")
		}
		return tasks[0].ID, nil
	}

	// Extract CID and look up in task table
	// The VM CID is stored when we create the VM
	if strings.HasPrefix(vmID, "cid-") {
		// TODO: Implement CID to task ID mapping
		// For now, scan running tasks
		tasks, err := ss.daemon.state.ListTasks("running")
		if err != nil {
			return "", err
		}
		if len(tasks) == 0 {
			return "", fmt.Errorf("no running tasks")
		}
		// TODO: Match by CID
		return tasks[0].ID, nil
	}

	return "", fmt.Errorf("unknown VM ID format: %s", vmID)
}

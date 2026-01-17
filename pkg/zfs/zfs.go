package zfs

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Manager handles ZFS operations for a specific pool and base path.
type Manager struct {
	PoolName string
	BasePath string
}

// NewManager creates a new ZFS manager for the given pool and base path.
func NewManager(pool, basePath string) *Manager {
	return &Manager{
		PoolName: pool,
		BasePath: basePath,
	}
}

// ParseDatasetName splits a ZFS dataset name into pool and path components.
func ParseDatasetName(name string) (pool, path string) {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// BuildSnapshotName creates a safe snapshot name from a task ID and label.
func BuildSnapshotName(taskID, label string) string {
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	safeLabel := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, label)

	if safeLabel == "" {
		return fmt.Sprintf("%s-%s", taskID, timestamp)
	}
	return fmt.Sprintf("%s-%s-%s", taskID, timestamp, safeLabel)
}

// DatasetPath returns the full ZFS dataset path for a given task ID.
func (m *Manager) DatasetPath(taskID string) string {
	return fmt.Sprintf("%s/%s/%s", m.PoolName, m.BasePath, taskID)
}

// CreateDataset creates a new ZFS dataset for the given task ID.
func (m *Manager) CreateDataset(ctx context.Context, taskID string) error {
	dataset := m.DatasetPath(taskID)
	return m.runZFS(ctx, "create", "-p", dataset)
}

// DestroyDataset destroys the ZFS dataset for the given task ID and all its children.
func (m *Manager) DestroyDataset(ctx context.Context, taskID string) error {
	dataset := m.DatasetPath(taskID)
	return m.runZFS(ctx, "destroy", "-r", dataset)
}

// CreateSnapshot creates a snapshot of the dataset for the given task ID.
// Returns the snapshot name (without the dataset prefix).
func (m *Manager) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
	dataset := m.DatasetPath(taskID)
	snapName := BuildSnapshotName(taskID, label)
	fullName := fmt.Sprintf("%s@%s", dataset, snapName)

	if err := m.runZFS(ctx, "snapshot", fullName); err != nil {
		return "", err
	}
	return snapName, nil
}

// ListSnapshots returns the names of all snapshots for the given task ID's dataset.
func (m *Manager) ListSnapshots(ctx context.Context, taskID string) ([]string, error) {
	dataset := m.DatasetPath(taskID)

	cmd := exec.CommandContext(ctx, "zfs", "list", "-t", "snapshot", "-H", "-o", "name", "-r", dataset)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("zfs list failed: %w: %s", err, stderr.String())
	}

	var snapshots []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line != "" {
			if idx := strings.LastIndex(line, "@"); idx != -1 {
				snapshots = append(snapshots, line[idx+1:])
			}
		}
	}
	return snapshots, nil
}

// DestroySnapshot destroys a specific snapshot.
func (m *Manager) DestroySnapshot(ctx context.Context, taskID, snapName string) error {
	dataset := m.DatasetPath(taskID)
	fullName := fmt.Sprintf("%s@%s", dataset, snapName)
	return m.runZFS(ctx, "destroy", fullName)
}

// RollbackSnapshot rolls back the dataset to a specific snapshot.
func (m *Manager) RollbackSnapshot(ctx context.Context, taskID, snapName string) error {
	dataset := m.DatasetPath(taskID)
	fullName := fmt.Sprintf("%s@%s", dataset, snapName)
	return m.runZFS(ctx, "rollback", "-r", fullName)
}

// GetMountpoint returns the mountpoint for the given task ID's dataset.
func (m *Manager) GetMountpoint(ctx context.Context, taskID string) (string, error) {
	dataset := m.DatasetPath(taskID)

	cmd := exec.CommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", dataset)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("zfs get mountpoint failed: %w: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Sync ensures all data is written to disk for the given task ID's dataset.
func (m *Manager) Sync(ctx context.Context, taskID string) error {
	mountpoint, err := m.GetMountpoint(ctx, taskID)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "sync", "-f", mountpoint)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sync failed: %w: %s", err, stderr.String())
	}
	return nil
}

// runZFS executes a zfs command with the given arguments.
func (m *Manager) runZFS(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "zfs", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("zfs %s failed: %w: %s", args[0], err, stderr.String())
	}
	return nil
}

// CloneTargetPath returns the full ZFS dataset path for a clone target.
// targetDataset is relative like "vms/abc123" -> becomes "tank/stockyard/vms/abc123"
func (m *Manager) CloneTargetPath(targetDataset string) string {
	return fmt.Sprintf("%s/%s/%s", m.PoolName, m.BasePath, targetDataset)
}

// CloneSnapshot creates a new dataset from an existing snapshot.
// snapshotPath is the full path like "tank/stockyard/images/rootfs@base"
// targetDataset is relative like "vms/abc123" -> becomes "tank/stockyard/vms/abc123"
func (m *Manager) CloneSnapshot(ctx context.Context, snapshotPath, targetDataset string) error {
	fullTarget := m.CloneTargetPath(targetDataset)
	return m.runZFS(ctx, "clone", snapshotPath, fullTarget)
}

// GetDatasetMountpoint returns the mountpoint for any dataset path relative to BasePath.
// datasetPath is relative like "vms/abc123" or "images/rootfs"
func (m *Manager) GetDatasetMountpoint(ctx context.Context, datasetPath string) (string, error) {
	fullPath := m.CloneTargetPath(datasetPath)

	cmd := exec.CommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", fullPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("zfs get mountpoint failed for %s: %w: %s", fullPath, err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

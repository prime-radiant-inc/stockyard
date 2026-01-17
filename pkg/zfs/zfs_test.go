package zfs

import (
	"strings"
	"testing"
)

func TestParseDatasetName(t *testing.T) {
	tests := []struct {
		input    string
		wantPool string
		wantPath string
	}{
		{"tank/stockyard/workspaces/task-123", "tank", "stockyard/workspaces/task-123"},
		{"rpool/data", "rpool", "data"},
		{"tank", "tank", ""},
	}

	for _, tt := range tests {
		pool, path := ParseDatasetName(tt.input)
		if pool != tt.wantPool || path != tt.wantPath {
			t.Errorf("ParseDatasetName(%q) = (%q, %q), want (%q, %q)",
				tt.input, pool, path, tt.wantPool, tt.wantPath)
		}
	}
}

func TestBuildSnapshotName(t *testing.T) {
	name := BuildSnapshotName("task-123", "edit-main.py")

	if name == "" {
		t.Error("snapshot name should not be empty")
	}

	if strings.Contains(name, " ") || strings.Contains(name, "@") {
		t.Errorf("invalid characters in snapshot name: %q", name)
	}

	if !strings.Contains(name, "task-123") {
		t.Errorf("snapshot name should contain task ID: %q", name)
	}
}

func TestBuildSnapshotName_Sanitization(t *testing.T) {
	name := BuildSnapshotName("task-123", "foo/bar:baz")

	// Verify problematic characters are replaced
	if strings.Contains(name, "/") {
		t.Errorf("snapshot name should not contain slashes: %q", name)
	}
	if strings.Contains(name, ":") {
		t.Errorf("snapshot name should not contain colons: %q", name)
	}

	// Verify the sanitized label is present (as foo-bar-baz)
	if !strings.Contains(name, "foo-bar-baz") {
		t.Errorf("snapshot name should contain sanitized label: %q", name)
	}
}

func TestDatasetPath(t *testing.T) {
	m := NewManager("tank", "stockyard/workspaces")
	path := m.DatasetPath("task-abc123")
	expected := "tank/stockyard/workspaces/task-abc123"

	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}

func TestCloneSnapshotTargetPath(t *testing.T) {
	m := NewManager("tank", "stockyard")

	// Test that CloneTargetPath builds correct full path from relative target
	tests := []struct {
		targetDataset string
		want          string
	}{
		{"vms/test-vm-123", "tank/stockyard/vms/test-vm-123"},
		{"vms/abc", "tank/stockyard/vms/abc"},
	}

	for _, tt := range tests {
		got := m.CloneTargetPath(tt.targetDataset)
		if got != tt.want {
			t.Errorf("CloneTargetPath(%q) = %q, want %q", tt.targetDataset, got, tt.want)
		}
	}
}

func TestCloneTargetPathForMountpoint(t *testing.T) {
	m := NewManager("tank", "stockyard")

	// Verify CloneTargetPath works for various dataset paths
	// This is used by both CloneSnapshot and GetDatasetMountpoint
	tests := []struct {
		datasetPath string
		want        string
	}{
		{"vms/test-vm-123", "tank/stockyard/vms/test-vm-123"},
		{"images/rootfs", "tank/stockyard/images/rootfs"},
		{"workspaces/task-abc", "tank/stockyard/workspaces/task-abc"},
	}

	for _, tt := range tests {
		got := m.CloneTargetPath(tt.datasetPath)
		if got != tt.want {
			t.Errorf("CloneTargetPath(%q) = %q, want %q", tt.datasetPath, got, tt.want)
		}
	}
}

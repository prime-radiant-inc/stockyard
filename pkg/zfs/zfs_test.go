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

func TestDatasetPath(t *testing.T) {
	m := NewManager("tank", "stockyard/workspaces")
	path := m.DatasetPath("task-abc123")
	expected := "tank/stockyard/workspaces/task-abc123"

	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}

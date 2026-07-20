package zfs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDatasetExistsClassifiesOnlyExactNotFoundAsAbsent(t *testing.T) {
	tests := []struct {
		name    string
		stderr  string
		err     error
		want    bool
		wantErr bool
	}{
		{name: "present", want: true},
		{name: "exact missing dataset", stderr: "cannot open 'tank/stockyard/workspaces/task-one': dataset does not exist\n", err: errors.New("exit status 1"), want: false},
		{name: "permission denied", stderr: "cannot open 'tank/stockyard/workspaces/task-one': permission denied\n", err: errors.New("exit status 1"), wantErr: true},
		{name: "malformed output", stderr: "dataset does not exist but unrelated\n", err: errors.New("exit status 1"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager("tank", "stockyard/workspaces")
			m.command = func(context.Context, ...string) ([]byte, []byte, error) {
				if tt.err == nil {
					return []byte("tank/stockyard/workspaces/task-one\n"), nil, nil
				}
				return nil, []byte(tt.stderr), tt.err
			}
			got, err := m.DatasetExists(context.Background(), "task-one")
			if tt.wantErr {
				if err == nil {
					t.Fatal("DatasetExists succeeded for an unknown result")
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("DatasetExists = (%v, %v), want (%v, nil)", got, err, tt.want)
			}
		})
	}
}

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

func TestSanitizeDatasetComponent(t *testing.T) {
	cases := map[string]string{
		"prudence-vm:1.2":           "prudence-vm-1.2",
		"docker.io/library/foo:dev": "docker.io-library-foo-dev",
		"simple":                    "simple",
		"UPPER_ok.too":              "UPPER_ok.too",
	}
	for in, want := range cases {
		if got := SanitizeDatasetComponent(in); got != want {
			t.Errorf("SanitizeDatasetComponent(%q) = %q, want %q", in, got, want)
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

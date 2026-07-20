package zfs

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDatasetExistsClassifiesOnlyExactNotFoundAsAbsent(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		stderr  string
		err     error
		want    bool
		wantErr bool
	}{
		{name: "present", stdout: "tank/stockyard/workspaces/task-one\n", want: true},
		{name: "successful malformed output", stdout: "tank/stockyard/workspaces/other\n", wantErr: true},
		{name: "exact missing dataset", stderr: "cannot open 'tank/stockyard/workspaces/task-one': dataset does not exist\n", err: zfsExitStatusOne(t), want: false},
		{name: "wrong exit code with missing text", stderr: "cannot open 'tank/stockyard/workspaces/task-one': dataset does not exist\n", err: zfsExitStatusTwo(t), wantErr: true},
		{name: "permission denied", stderr: "cannot open 'tank/stockyard/workspaces/task-one': permission denied\n", err: errors.New("exit status 1"), wantErr: true},
		{name: "malformed output", stderr: "dataset does not exist but unrelated\n", err: errors.New("exit status 1"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager("tank", "stockyard/workspaces")
			m.command = func(context.Context, ...string) ([]byte, []byte, error) {
				return []byte(tt.stdout), []byte(tt.stderr), tt.err
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

func zfsExitStatusOne(t *testing.T) error {
	t.Helper()
	return exec.Command("sh", "-c", "exit 1").Run()
}

func zfsExitStatusTwo(t *testing.T) error {
	t.Helper()
	return exec.Command("sh", "-c", "exit 2").Run()
}

func TestDatasetExistsRefusesNotFoundTextFromCanceledOrNonExitCommands(t *testing.T) {
	notFound := "cannot open 'tank/stockyard/workspaces/task-one': dataset does not exist\n"
	exitOne := func(t *testing.T) error {
		t.Helper()
		return exec.Command("sh", "-c", "exit 1").Run()
	}
	tests := []struct {
		name   string
		ctx    context.Context
		err    func(*testing.T) error
		stdout string
	}{
		{name: "canceled context", ctx: canceledContext(), err: exitOne},
		{name: "expired context", ctx: expiredContext(), err: exitOne},
		{name: "non exit error", ctx: context.Background(), err: func(*testing.T) error { return errors.New("transport failure") }},
		{name: "unexpected stdout", ctx: context.Background(), err: exitOne, stdout: "unexpected\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager("tank", "stockyard/workspaces")
			m.command = func(context.Context, ...string) ([]byte, []byte, error) {
				return []byte(tt.stdout), []byte(notFound), tt.err(t)
			}
			if _, err := m.DatasetExists(tt.ctx, "task-one"); err == nil {
				t.Fatal("DatasetExists accepted untrustworthy absence")
			}
		})
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func expiredContext() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	cancel()
	return ctx
}

func TestDestroyDatasetRecursiveRequiresPostDestroyAbsence(t *testing.T) {
	const dataset = "tank/stockyard/vms/task-one"
	tests := []struct {
		name      string
		responses []zfsCommandResponse
		wantErr   bool
	}{
		{
			name: "already absent",
			responses: []zfsCommandResponse{{
				args:   []string{"list", "-H", "-o", "name", dataset},
				stderr: "cannot open 'tank/stockyard/vms/task-one': dataset does not exist\n",
				err:    zfsExitStatusOne(t),
			}},
		},
		{
			name: "initial read unknown",
			responses: []zfsCommandResponse{{
				args: []string{"list", "-H", "-o", "name", dataset},
				err:  errors.New("permission denied"),
			}},
			wantErr: true,
		},
		{
			name: "destroy failure",
			responses: []zfsCommandResponse{
				{args: []string{"list", "-H", "-o", "name", dataset}, stdout: dataset + "\n"},
				{args: []string{"destroy", "-R", dataset}, err: errors.New("destroy failed")},
			},
			wantErr: true,
		},
		{
			name: "post destroy read unknown",
			responses: []zfsCommandResponse{
				{args: []string{"list", "-H", "-o", "name", dataset}, stdout: dataset + "\n"},
				{args: []string{"destroy", "-R", dataset}},
				{args: []string{"list", "-H", "-o", "name", dataset}, err: errors.New("readback failed")},
			},
			wantErr: true,
		},
		{
			name: "dataset remains",
			responses: []zfsCommandResponse{
				{args: []string{"list", "-H", "-o", "name", dataset}, stdout: dataset + "\n"},
				{args: []string{"destroy", "-R", dataset}},
				{args: []string{"list", "-H", "-o", "name", dataset}, stdout: dataset + "\n"},
			},
			wantErr: true,
		},
		{
			name: "verified absent",
			responses: []zfsCommandResponse{
				{args: []string{"list", "-H", "-o", "name", dataset}, stdout: dataset + "\n"},
				{args: []string{"destroy", "-R", dataset}},
				{args: []string{"list", "-H", "-o", "name", dataset}, stderr: "cannot open 'tank/stockyard/vms/task-one': dataset does not exist\n", err: zfsExitStatusOne(t)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager("tank", "stockyard/workspaces")
			responses := append([]zfsCommandResponse(nil), tt.responses...)
			manager.command = func(_ context.Context, args ...string) ([]byte, []byte, error) {
				if len(responses) == 0 {
					t.Fatalf("unexpected zfs command: %v", args)
				}
				response := responses[0]
				responses = responses[1:]
				if strings.Join(args, "\x00") != strings.Join(response.args, "\x00") {
					t.Fatalf("zfs command = %v, want %v", args, response.args)
				}
				return []byte(response.stdout), []byte(response.stderr), response.err
			}

			err := manager.DestroyDatasetRecursive(context.Background(), dataset)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DestroyDatasetRecursive error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(responses) != 0 {
				t.Fatalf("unused zfs responses = %d", len(responses))
			}
		})
	}
}

type zfsCommandResponse struct {
	args           []string
	stdout, stderr string
	err            error
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

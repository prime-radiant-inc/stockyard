package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/network"
	"github.com/obra/stockyard/pkg/secrets"
	"github.com/obra/stockyard/pkg/vmbackend"
)

type taskCreateTestBackend struct {
	createCalls int
	deleteCalls int
	createdID   string
	onCreate    func()
	createErr   error
	deleteErr   error
}

func (b *taskCreateTestBackend) CreateVM(_ context.Context, cfg *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	b.createCalls++
	b.createdID = cfg.ID
	if b.onCreate != nil {
		b.onCreate()
	}
	if b.createErr != nil {
		return nil, b.createErr
	}
	return &vmbackend.VMInfo{ID: cfg.ID}, nil
}

func (b *taskCreateTestBackend) StartVM(context.Context, *vmbackend.VMConfig) (*vmbackend.VMInfo, error) {
	return nil, nil
}
func (b *taskCreateTestBackend) StopVM(context.Context, string) error { return nil }
func (b *taskCreateTestBackend) DeleteVM(context.Context, string) error {
	b.deleteCalls++
	return b.deleteErr
}
func (b *taskCreateTestBackend) GetVM(context.Context, string) (*vmbackend.VMState, error) {
	return nil, nil
}
func (b *taskCreateTestBackend) ListVMs(context.Context) ([]*vmbackend.VMState, error) {
	return nil, nil
}
func (b *taskCreateTestBackend) Close() error { return nil }

func newTaskCreateTestManager(t *testing.T, pool *network.IPPool, backend vmbackend.Backend) (*TaskManager, *State) {
	t.Helper()
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("NewStateInMemory: %v", err)
	}
	d := &Daemon{
		cfg:     &config.Config{Backend: "apple-container"},
		secrets: &secrets.MockProvider{Secrets: map[string]string{}},
		state:   state,
		ipPool:  pool,
	}
	return NewTaskManager(d, backend), state
}

func TestParseMemory(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int32
	}{
		{"megabytes lowercase", "512m", 512},
		{"megabytes uppercase", "512M", 512},
		{"megabytes with MB", "512MB", 512},
		{"megabytes with mb", "512mb", 512},
		{"gigabytes lowercase", "2g", 2048},
		{"gigabytes uppercase", "2G", 2048},
		{"gigabytes with GB", "2GB", 2048},
		{"gigabytes with gb", "2gb", 2048},
		{"plain number", "1024", 1024},
		{"empty string defaults to 1024", "", 1024},
		{"invalid string defaults to 1024", "invalid", 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMemory(tt.input)
			if got != tt.expected {
				t.Errorf("parseMemory(%q) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNewTaskManager(t *testing.T) {
	cfg := &config.Config{
		ZFS: config.ZFSConfig{
			Pool:     "tank",
			BasePath: "stockyard/workspaces",
		},
	}
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	secretsProvider := &secrets.MockProvider{
		Secrets: map[string]string{},
	}

	d := &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		state:   state,
	}

	// Test with nil config (no firecracker client)
	tm := NewTaskManager(d, nil)
	if tm == nil {
		t.Fatal("NewTaskManager returned nil")
	}
	if tm.daemon != d {
		t.Error("TaskManager daemon reference is incorrect")
	}
}

func TestCreateTaskRequest_Defaults(t *testing.T) {
	req := &CreateTaskRequest{}
	// Defaults are zero-valued and applied during CreateTask
	if req.CPUs != 0 {
		t.Errorf("expected default CPUs to be 0, got %d", req.CPUs)
	}
	if req.MemoryMB != 0 {
		t.Errorf("expected default MemoryMB to be 0, got %d", req.MemoryMB)
	}
}

func TestTaskManager_CreateTaskIPAllocationPersistenceFailureCreatesNoTaskOrVM(t *testing.T) {
	pool, err := network.NewIPPool("10.0.100.0/24", "10.0.100.1")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, nil, 0644); err != nil {
		t.Fatalf("write blocked parent: %v", err)
	}
	pool.SetPersistPath(filepath.Join(blockedParent, "ip_pool.json"))
	backend := &taskCreateTestBackend{}
	manager, state := newTaskCreateTestManager(t, pool, backend)
	defer state.Close()

	if _, err := manager.CreateTask(context.Background(), &CreateTaskRequest{
		Name:        "allocation-failure",
		NoTailscale: true,
	}); err == nil {
		t.Fatal("CreateTask succeeded despite IP allocation persistence failure")
	}
	if backend.createCalls != 0 {
		t.Errorf("CreateVM calls = %d, want 0", backend.createCalls)
	}
	tasks, err := state.ListTasks("")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("task rows after failed allocation = %d, want 0", len(tasks))
	}
}

func TestTaskManager_CreateTaskCleanupReleaseFailureVisibleToCaller(t *testing.T) {
	pool, err := network.NewIPPool("10.0.100.0/24", "10.0.100.1")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}
	pool.SetPersistPath(filepath.Join(t.TempDir(), "ip_pool.json"))
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, nil, 0644); err != nil {
		t.Fatalf("write blocked parent: %v", err)
	}
	backend := &taskCreateTestBackend{
		createErr: errors.New("injected VM creation failure"),
		onCreate: func() {
			pool.SetPersistPath(filepath.Join(blockedParent, "ip_pool.json"))
		},
	}
	manager, state := newTaskCreateTestManager(t, pool, backend)
	defer state.Close()

	_, err = manager.CreateTask(context.Background(), &CreateTaskRequest{
		Name:        "cleanup-release-failure",
		NoTailscale: true,
	})
	if err == nil {
		t.Fatal("CreateTask succeeded despite VM and release failures")
	}
	if !strings.Contains(err.Error(), "injected VM creation failure") {
		t.Errorf("CreateTask error %q does not include VM creation failure", err)
	}
	if !strings.Contains(err.Error(), "create IP allocation directory") {
		t.Errorf("CreateTask error %q does not include release persistence failure", err)
	}
}

func TestTaskManager_CreateTaskCleanupRecordFailureRetainsIPWhenVMDeleteFails(t *testing.T) {
	pool, err := network.NewIPPool("10.0.100.0/24", "10.0.100.1")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}
	pool.SetPersistPath(filepath.Join(t.TempDir(), "ip_pool.json"))
	backend := &taskCreateTestBackend{deleteErr: errors.New("injected VM deletion failure")}
	manager, state := newTaskCreateTestManager(t, pool, backend)
	backend.onCreate = func() {
		if err := state.Close(); err != nil {
			t.Fatalf("close state before task insert: %v", err)
		}
	}

	_, err = manager.CreateTask(context.Background(), &CreateTaskRequest{
		Name:        "record-failure",
		NoTailscale: true,
	})
	if err == nil {
		t.Fatal("CreateTask succeeded despite task-record and VM deletion failures")
	}
	if !strings.Contains(err.Error(), "injected VM deletion failure") {
		t.Errorf("CreateTask error %q does not include VM deletion failure", err)
	}
	if backend.deleteCalls != 1 {
		t.Errorf("DeleteVM calls = %d, want 1", backend.deleteCalls)
	}
	if got := pool.GetAllocation(backend.createdID); got == "" {
		t.Error("IP allocation was released while the VM delete failed")
	}
}

func TestTaskManager_DestroyTaskIPReleaseFailureRetainsTask(t *testing.T) {
	pool, err := network.NewIPPool("10.0.100.0/24", "10.0.100.1")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}
	pool.SetPersistPath(filepath.Join(t.TempDir(), "ip_pool.json"))
	if _, err := pool.Allocate("task-1"); err != nil {
		t.Fatalf("Allocate task-1: %v", err)
	}
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, nil, 0644); err != nil {
		t.Fatalf("write blocked parent: %v", err)
	}
	pool.SetPersistPath(filepath.Join(blockedParent, "ip_pool.json"))
	manager, state := newTaskCreateTestManager(t, pool, nil)
	defer state.Close()
	if err := state.CreateTask(&Task{ID: "task-1", Status: "running"}); err != nil {
		t.Fatalf("CreateTask row: %v", err)
	}

	if err := manager.DestroyTask(context.Background(), "task-1"); err == nil {
		t.Fatal("DestroyTask succeeded despite release persistence failure")
	}
	if _, err := state.GetTask("task-1"); err != nil {
		t.Errorf("task row was removed after release failure: %v", err)
	}
}

func TestTaskManager_FailTask(t *testing.T) {
	cfg := &config.Config{
		ZFS: config.ZFSConfig{
			Pool:     "tank",
			BasePath: "stockyard/workspaces",
		},
	}
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	secretsProvider := &secrets.MockProvider{
		Secrets: map[string]string{},
	}

	d := &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		state:   state,
	}

	tm := NewTaskManager(d, nil)

	// Create a task in the database
	task := &Task{
		ID:     "test-fail-task",
		Name:   "Test Task",
		Status: "running",
	}
	if err := state.CreateTask(task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Fail the task
	err = tm.FailTask(context.Background(), "test-fail-task", "VM crashed unexpectedly")
	if err != nil {
		t.Fatalf("FailTask returned error: %v", err)
	}

	// Verify the status was updated to "failed"
	updatedTask, err := state.GetTask("test-fail-task")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}
	if updatedTask.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", updatedTask.Status)
	}
}

func TestTaskManager_FailTask_TaskNotFound(t *testing.T) {
	cfg := &config.Config{
		ZFS: config.ZFSConfig{
			Pool:     "tank",
			BasePath: "stockyard/workspaces",
		},
	}
	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	defer state.Close()

	secretsProvider := &secrets.MockProvider{
		Secrets: map[string]string{},
	}

	d := &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		state:   state,
	}

	tm := NewTaskManager(d, nil)

	// Try to fail a non-existent task
	err = tm.FailTask(context.Background(), "non-existent-task", "some reason")
	if err == nil {
		t.Error("expected error for non-existent task, got nil")
	}
}

func TestBuildVMEnvMetadata_AppleContainer(t *testing.T) {
	env := map[string]string{
		"ANTHROPIC_API_KEY": "sk-test",
		"GITHUB_TOKEN":      "ghp-test",
		"MY_VAR":            "v",
	}
	vmEnv, vmMeta := buildVMEnvMetadata("apple-container", "abc12345", "demo",
		env, "tskey-abc", "stockyard-abc12345", "ip=ignored", nil, nil)

	// The real workload environment must be delivered to the container — this is
	// the bug the C1 fix addresses (apple-container has no cloud-init/MMDS).
	for k, want := range env {
		if vmEnv[k] != want {
			t.Errorf("vmEnv[%q] = %q, want %q", k, vmEnv[k], want)
		}
	}
	// The Tailscale key must be named as the container entrypoint reads it.
	if vmEnv["TAILSCALE_AUTH_KEY"] != "tskey-abc" {
		t.Errorf("TAILSCALE_AUTH_KEY = %q, want tskey-abc", vmEnv["TAILSCALE_AUTH_KEY"])
	}
	if vmEnv["STOCKYARD_HOSTNAME"] != "stockyard-abc12345" {
		t.Errorf("STOCKYARD_HOSTNAME = %q, want stockyard-abc12345", vmEnv["STOCKYARD_HOSTNAME"])
	}
	// Firecracker-adapter-private underscore keys must NOT leak onto this path.
	if _, ok := vmEnv["_tailscale_auth_key"]; ok {
		t.Error("_tailscale_auth_key must not be set on the apple-container path")
	}
	if _, ok := vmEnv["_static_ip_args"]; ok {
		t.Error("_static_ip_args must not be set on the apple-container path")
	}
	if vmMeta["task-id"] != "abc12345" || vmMeta["task-name"] != "demo" {
		t.Errorf("metadata labels wrong: %v", vmMeta)
	}
}

func TestBuildVMEnvMetadata_FirecrackerUnchanged(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	vmEnv, _ := buildVMEnvMetadata("firecracker", "abc12345", "demo",
		env, "tskey-abc", "stockyard-abc12345", "ip=1.2.3.4", nil, nil)

	// Firecracker delivers the workload env via cloud-init, not VMConfig.Env —
	// the env map must NOT be copied into vmEnv on this path.
	if _, ok := vmEnv["ANTHROPIC_API_KEY"]; ok {
		t.Error("firecracker path must not copy workload env into vmEnv")
	}
	// The adapter-private keys the Firecracker adapter extracts must be present.
	if vmEnv["_tailscale_auth_key"] != "tskey-abc" {
		t.Errorf("_tailscale_auth_key = %q, want tskey-abc", vmEnv["_tailscale_auth_key"])
	}
	if vmEnv["_static_ip_args"] != "ip=1.2.3.4" {
		t.Errorf("_static_ip_args = %q, want ip=1.2.3.4", vmEnv["_static_ip_args"])
	}
	// The plain (entrypoint-facing) names must NOT be set on the firecracker path.
	if _, ok := vmEnv["TAILSCALE_AUTH_KEY"]; ok {
		t.Error("plain TAILSCALE_AUTH_KEY must not be set on the firecracker path")
	}
}

func TestParseDotEnv(t *testing.T) {
	input := []byte(`# comment
KEY1=value1
KEY2=value with spaces
KEY3="quoted double"
KEY4='quoted single'
export KEY5=exported
KEY6=

BLANK_AFTER_BLANK=yes
# another comment

KEY7="hello=world"
=invalid_no_key
invalid_no_equals
`)
	got := parseDotEnv(input)

	cases := []struct{ key, want string }{
		{"KEY1", "value1"},
		{"KEY2", "value with spaces"},
		{"KEY3", "quoted double"},
		{"KEY4", "quoted single"},
		{"KEY5", "exported"},
		{"KEY6", ""},
		{"BLANK_AFTER_BLANK", "yes"},
		{"KEY7", "hello=world"},
	}
	for _, c := range cases {
		if got[c.key] != c.want {
			t.Errorf("parseDotEnv[%q] = %q, want %q", c.key, got[c.key], c.want)
		}
	}
	// Lines without a key should not be present.
	if _, ok := got[""]; ok {
		t.Error("parseDotEnv should not produce empty key")
	}
	if _, ok := got["invalid_no_equals"]; ok {
		t.Error("parseDotEnv should not produce key for line without '='")
	}
}

func TestBuildVMEnvMetadata_AppleContainer_DotEnvPrecedence(t *testing.T) {
	// .env file sets FOO=from_dotenv; explicit env sets FOO=from_env.
	// Explicit env must win (higher precedence).
	dotEnv := []byte("FOO=from_dotenv\nBAR=only_in_dotenv\n")
	env := map[string]string{"FOO": "from_env"}

	vmEnv, _ := buildVMEnvMetadata("apple-container", "abc12345", "demo",
		env, "", "stockyard-abc12345", "", nil, dotEnv)

	if vmEnv["FOO"] != "from_env" {
		t.Errorf("FOO = %q, want 'from_env' (explicit env must override .env)", vmEnv["FOO"])
	}
	if vmEnv["BAR"] != "only_in_dotenv" {
		t.Errorf("BAR = %q, want 'only_in_dotenv' (.env-only key must be present)", vmEnv["BAR"])
	}
}

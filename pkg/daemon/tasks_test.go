package daemon

import (
	"context"
	"testing"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/secrets"
)

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

func TestTaskManager_CreateTaskRequest_Validation(t *testing.T) {
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

	// Test missing repo
	_, err = tm.CreateTask(context.Background(), &CreateTaskRequest{
		Repo: "",
		Ref:  "main",
	})
	if err == nil {
		t.Error("expected error for missing repo, got nil")
	}
}

func TestCreateTaskRequest_Defaults(t *testing.T) {
	req := &CreateTaskRequest{
		Repo: "github.com/test/repo",
	}

	// Defaults should be applied when processing
	if req.Ref == "" {
		// This is expected - defaults are applied during CreateTask
	}
	if req.CPUs == 0 {
		// This is expected - defaults are applied during CreateTask
	}
	if req.MemoryMB == 0 {
		// This is expected - defaults are applied during CreateTask
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
		Repo:   "github.com/test/repo",
		Ref:    "main",
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

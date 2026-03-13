// Package daemon provides state management for the stockyard daemon.
package daemon

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// QueueManager manages command queues and coordinates execution.
// It wraps the State layer and adds scheduling logic.
type QueueManager struct {
	state   *State
	dataDir string
	mu      sync.Mutex
}

// NewQueueManager creates a new QueueManager.
func NewQueueManager(state *State, dataDir string) *QueueManager {
	return &QueueManager{state: state, dataDir: dataDir}
}

// generateCommandID creates a unique command ID using random bytes.
func generateCommandID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("cmd-%x", b)
}

// outputPath returns the path for a command's output log file.
func (qm *QueueManager) outputPath(taskID, commandID string) string {
	return filepath.Join(qm.dataDir, "tasks", taskID, "commands", commandID, "output.log")
}

// InitQueues creates the built-in queues (default + admin) for a task.
// "default" is a serial protected queue; "admin" is a concurrent protected queue.
func (qm *QueueManager) InitQueues(taskID string) error {
	now := time.Now()

	defaultQueue := &Queue{
		TaskID:    taskID,
		Name:      "default",
		Mode:      "serial",
		Protected: true,
		Status:    "active",
		CreatedAt: now,
	}
	if err := qm.state.CreateQueue(defaultQueue); err != nil {
		return fmt.Errorf("create default queue: %w", err)
	}

	adminQueue := &Queue{
		TaskID:    taskID,
		Name:      "admin",
		Mode:      "concurrent",
		Protected: true,
		Status:    "active",
		CreatedAt: now.Add(time.Nanosecond), // ensure stable ordering
	}
	if err := qm.state.CreateQueue(adminQueue); err != nil {
		return fmt.Errorf("create admin queue: %w", err)
	}

	return nil
}

// CreateQueue creates a custom (non-protected) queue for a task.
func (qm *QueueManager) CreateQueue(taskID, name, mode string) error {
	q := &Queue{
		TaskID:    taskID,
		Name:      name,
		Mode:      mode,
		Protected: false,
		Status:    "active",
		CreatedAt: time.Now(),
	}
	return qm.state.CreateQueue(q)
}

// QueueCommand appends a command to a queue and returns the command ID.
// For serial queues, triggers execution if no command is currently running.
// For concurrent queues, triggers execution immediately.
func (qm *QueueManager) QueueCommand(taskID, queueName string, command []string, env map[string]string, stopOnFailure bool) (string, error) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	// Verify queue exists
	queue, err := qm.state.GetQueue(taskID, queueName)
	if err != nil {
		return "", fmt.Errorf("get queue: %w", err)
	}

	commandID := generateCommandID()
	outputPath := qm.outputPath(taskID, commandID)

	cmd := &Command{
		ID:            commandID,
		TaskID:        taskID,
		QueueName:     queueName,
		Command:       command,
		Env:           env,
		Status:        "pending",
		StopOnFailure: stopOnFailure,
		OutputPath:    outputPath,
		CreatedAt:     time.Now(),
	}

	if err := qm.state.CreateCommand(cmd); err != nil {
		return "", fmt.Errorf("create command: %w", err)
	}

	// Decide whether to start immediately
	switch queue.Mode {
	case "concurrent":
		go qm.executeCommand(taskID, commandID)
	case "serial":
		// Start only if nothing is currently running in this queue
		if !qm.isRunning(taskID, queueName) {
			go qm.executeCommand(taskID, commandID)
		}
	}

	return commandID, nil
}

// isRunning reports whether any command in the given queue has status "running".
// Caller must hold qm.mu.
func (qm *QueueManager) isRunning(taskID, queueName string) bool {
	cmds, err := qm.state.ListCommands(taskID, queueName)
	if err != nil {
		return false
	}
	for _, c := range cmds {
		if c.Status == "running" {
			return true
		}
	}
	return false
}

// GetCommandStatus returns command info by ID.
func (qm *QueueManager) GetCommandStatus(commandID string) (*Command, error) {
	return qm.state.GetCommand(commandID)
}

// GetQueueStatus returns queue info along with all its commands.
func (qm *QueueManager) GetQueueStatus(taskID, queueName string) (*Queue, []*Command, error) {
	queue, err := qm.state.GetQueue(taskID, queueName)
	if err != nil {
		return nil, nil, err
	}
	cmds, err := qm.state.ListCommands(taskID, queueName)
	if err != nil {
		return nil, nil, err
	}
	return queue, cmds, nil
}

// ListQueues returns all queues for a task.
func (qm *QueueManager) ListQueues(taskID string) ([]*Queue, error) {
	return qm.state.ListQueues(taskID)
}

// FlushQueue clears all pending commands from a queue.
// Commands that are running, completed, or failed are left intact.
func (qm *QueueManager) FlushQueue(taskID, queueName string) error {
	return qm.state.FlushQueueCommands(taskID, queueName)
}

// DestroyQueue removes a non-protected queue.
// Returns an error if the queue is protected.
func (qm *QueueManager) DestroyQueue(taskID, queueName string) error {
	return qm.state.DestroyQueue(taskID, queueName)
}

// CleanupTask removes all queues, commands, and output files for a task.
func (qm *QueueManager) CleanupTask(taskID string) error {
	// Remove output files directory
	taskDir := filepath.Join(qm.dataDir, "tasks", taskID)
	if err := os.RemoveAll(taskDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove task dir: %w", err)
	}

	if err := qm.state.DeleteCommandsByTask(taskID); err != nil {
		return fmt.Errorf("delete commands: %w", err)
	}
	if err := qm.state.DeleteQueuesByTask(taskID); err != nil {
		return fmt.Errorf("delete queues: %w", err)
	}

	return nil
}

// executeCommand opens a vsock connection, runs a command, and persists output.
// Runs in a goroutine spawned by QueueCommand or triggerNext.
// Vsock execution is implemented in this method; it will fail gracefully if the
// task has no vsock path (e.g., in tests where no VM is running).
func (qm *QueueManager) executeCommand(taskID, commandID string) {
	cmd, err := qm.state.GetCommand(commandID)
	if err != nil {
		return
	}

	task, err := qm.state.GetTask(taskID)
	if err != nil {
		return
	}

	// Mark as running
	if err := qm.state.UpdateCommandStatus(commandID, "running"); err != nil {
		return
	}

	// Run the command; if vsock is unavailable it will return exit code 1
	exitCode, _ := qm.runVsockCommand(task, cmd)

	// Persist exit code and terminal status (completed / failed)
	qm.state.UpdateCommandExit(commandID, exitCode)

	// Post-run queue management for serial queues
	queue, err := qm.state.GetQueue(taskID, cmd.QueueName)
	if err != nil {
		return
	}

	if queue.Mode == "serial" {
		if cmd.StopOnFailure && exitCode != 0 {
			qm.state.UpdateQueueStatus(taskID, cmd.QueueName, "stopped")
			return
		}
		qm.triggerNext(taskID, cmd.QueueName)
	}
}

// triggerNext checks if the next pending command in a serial queue should run.
// It does nothing when the queue is in "stopped" status.
func (qm *QueueManager) triggerNext(taskID, queueName string) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	queue, err := qm.state.GetQueue(taskID, queueName)
	if err != nil {
		return
	}
	if queue.Status == "stopped" {
		return
	}

	cmds, err := qm.state.ListCommands(taskID, queueName)
	if err != nil {
		return
	}

	for _, c := range cmds {
		if c.Status == "pending" {
			go qm.executeCommand(taskID, c.ID)
			return
		}
	}
}

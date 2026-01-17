// Package daemon provides state management for the stockyard daemon.
package daemon

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Task represents a running or completed task in the system.
type Task struct {
	ID        string
	Name      string
	Repo      string
	Ref       string
	Command   string
	Status    string
	VMID      string
	CreatedAt time.Time
	StoppedAt *time.Time
}

// State manages persistent state for the daemon using SQLite.
type State struct {
	db *sql.DB
}

// DataDir returns the XDG-compliant data directory for stockyard.
func DataDir() string {
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		return filepath.Join(xdgData, "stockyard")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".local", "share", "stockyard")
	}
	return filepath.Join(home, ".local", "share", "stockyard")
}

// NewState creates a new State instance with a file-based SQLite database.
func NewState() (*State, error) {
	dataDir := DataDir()
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dbPath := filepath.Join(dataDir, "state.db")
	return newState(dbPath)
}

// NewStateInMemory creates a new State instance with an in-memory SQLite database.
// This is useful for testing.
func NewStateInMemory() (*State, error) {
	return newState(":memory:")
}

func newState(dbPath string) (*State, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	state := &State{db: db}
	if err := state.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return state, nil
}

func (s *State) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		name TEXT,
		repo TEXT NOT NULL,
		ref TEXT NOT NULL,
		command TEXT NOT NULL,
		status TEXT NOT NULL,
		vmid TEXT,
		created_at DATETIME NOT NULL,
		stopped_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		snapshot_name TEXT NOT NULL,
		created_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_snapshots_task_id ON snapshots(task_id);
	`

	_, err := s.db.Exec(schema)
	return err
}

// Close closes the database connection.
func (s *State) Close() error {
	return s.db.Close()
}

// CreateTask creates a new task in the database.
func (s *State) CreateTask(task *Task) error {
	query := `
	INSERT INTO tasks (id, name, repo, ref, command, status, vmid, created_at, stopped_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		task.ID,
		task.Name,
		task.Repo,
		task.Ref,
		task.Command,
		task.Status,
		task.VMID,
		task.CreatedAt,
		task.StoppedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}
	return nil
}

// GetTask retrieves a task by ID.
func (s *State) GetTask(id string) (*Task, error) {
	query := `
	SELECT id, name, repo, ref, command, status, vmid, created_at, stopped_at
	FROM tasks
	WHERE id = ?
	`
	row := s.db.QueryRow(query, id)

	task := &Task{}
	var stoppedAt sql.NullTime
	var vmid sql.NullString
	var name sql.NullString

	err := row.Scan(
		&task.ID,
		&name,
		&task.Repo,
		&task.Ref,
		&task.Command,
		&task.Status,
		&vmid,
		&task.CreatedAt,
		&stoppedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	if name.Valid {
		task.Name = name.String
	}
	if vmid.Valid {
		task.VMID = vmid.String
	}
	if stoppedAt.Valid {
		task.StoppedAt = &stoppedAt.Time
	}

	return task, nil
}

// ListTasks returns all tasks, optionally filtered by status.
// If status is empty, all tasks are returned.
func (s *State) ListTasks(status string) ([]*Task, error) {
	var query string
	var args []interface{}

	if status == "" {
		query = `
		SELECT id, name, repo, ref, command, status, vmid, created_at, stopped_at
		FROM tasks
		ORDER BY created_at DESC
		`
	} else {
		query = `
		SELECT id, name, repo, ref, command, status, vmid, created_at, stopped_at
		FROM tasks
		WHERE status = ?
		ORDER BY created_at DESC
		`
		args = append(args, status)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		task := &Task{}
		var stoppedAt sql.NullTime
		var vmid sql.NullString
		var name sql.NullString

		err := rows.Scan(
			&task.ID,
			&name,
			&task.Repo,
			&task.Ref,
			&task.Command,
			&task.Status,
			&vmid,
			&task.CreatedAt,
			&stoppedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		if name.Valid {
			task.Name = name.String
		}
		if vmid.Valid {
			task.VMID = vmid.String
		}
		if stoppedAt.Valid {
			task.StoppedAt = &stoppedAt.Time
		}

		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	return tasks, nil
}

// UpdateTaskStatus updates the status of a task.
// If the new status is "stopped", the stopped_at timestamp is also set.
func (s *State) UpdateTaskStatus(id, status string) error {
	var query string
	var args []interface{}

	if status == "stopped" {
		query = `
		UPDATE tasks
		SET status = ?, stopped_at = ?
		WHERE id = ?
		`
		now := time.Now()
		args = []interface{}{status, now, id}
	} else {
		query = `
		UPDATE tasks
		SET status = ?
		WHERE id = ?
		`
		args = []interface{}{status, id}
	}

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("task not found: %s", id)
	}

	return nil
}

// UpdateTaskVMID updates the VMID of a task.
func (s *State) UpdateTaskVMID(id, vmid string) error {
	query := `
	UPDATE tasks
	SET vmid = ?
	WHERE id = ?
	`

	result, err := s.db.Exec(query, vmid, id)
	if err != nil {
		return fmt.Errorf("failed to update task VMID: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("task not found: %s", id)
	}

	return nil
}

// DeleteTask removes a task from the database.
func (s *State) DeleteTask(id string) error {
	query := `DELETE FROM tasks WHERE id = ?`

	result, err := s.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("task not found: %s", id)
	}

	return nil
}

// RecordSnapshot records a snapshot associated with a task.
func (s *State) RecordSnapshot(taskID, snapshotName string) error {
	query := `
	INSERT INTO snapshots (task_id, snapshot_name, created_at)
	VALUES (?, ?, ?)
	`
	_, err := s.db.Exec(query, taskID, snapshotName, time.Now())
	if err != nil {
		return fmt.Errorf("failed to record snapshot: %w", err)
	}
	return nil
}

// SnapshotRecord represents a snapshot in the database
type SnapshotRecord struct {
	Name      string
	CreatedAt time.Time
}

// ListTaskSnapshots lists all snapshots for a task
func (s *State) ListTaskSnapshots(taskID string) ([]SnapshotRecord, error) {
	rows, err := s.db.Query(
		`SELECT snapshot_name, created_at FROM snapshots WHERE task_id = ? ORDER BY created_at DESC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []SnapshotRecord
	for rows.Next() {
		var snap SnapshotRecord
		if err := rows.Scan(&snap.Name, &snap.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan snapshot: %w", err)
		}
		snapshots = append(snapshots, snap)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating snapshots: %w", err)
	}

	return snapshots, nil
}

// Package daemon provides state management for the stockyard daemon.
package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Task represents a running or completed task in the system.
type Task struct {
	ID                string
	Name              string
	Command           string
	Status            string
	VMID              string
	CID               uint32 // Firecracker vsock Context ID
	VsockPath         string // Path to vsock UDS
	Owner             string // Username who created the task
	TailscaleHostname string
	CreatedAt         time.Time
	StoppedAt         *time.Time
}

// Queue represents a named command queue for a task.
type Queue struct {
	TaskID    string
	Name      string
	Mode      string // "serial" or "concurrent"
	Protected bool
	Status    string // "active" or "stopped"
	CreatedAt time.Time
}

// Command represents a queued command for execution.
type Command struct {
	ID            string
	TaskID        string
	QueueName     string
	Command       []string
	Env           map[string]string
	Status        string // "pending", "running", "completed", "failed"
	ExitCode      *int
	StopOnFailure bool
	OutputPath    string
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
}

// StatusChangeCallback is called when a task's status changes.
type StatusChangeCallback func(taskID, oldStatus, newStatus string)

// State manages persistent state for the daemon using SQLite.
type State struct {
	db             *sql.DB
	statusCallback StatusChangeCallback
	callbackMu     sync.RWMutex
}

// DataDir returns the data directory for stockyard state.
// It checks STOCKYARD_DATA_DIR env var, then /var/lib/stockyard if it exists,
// then falls back to XDG data directories.
func DataDir() string {
	// Explicit override
	if dir := os.Getenv("STOCKYARD_DATA_DIR"); dir != "" {
		return dir
	}

	// System-wide location (for daemon)
	systemDir := "/var/lib/stockyard"
	if _, err := os.Stat(systemDir); err == nil {
		return systemDir
	}

	// XDG data directory
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		return filepath.Join(xdgData, "stockyard")
	}

	// User home fallback
	home, err := os.UserHomeDir()
	if err != nil {
		return systemDir
	}
	return filepath.Join(home, ".local", "share", "stockyard")
}

// NewState creates a new State instance with a file-based SQLite database.
// If dataDir is empty, it uses DataDir() to determine the location.
func NewState(dataDir string) (*State, error) {
	if dataDir == "" {
		dataDir = DataDir()
	}
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
		command TEXT NOT NULL,
		status TEXT NOT NULL,
		vmid TEXT,
		cid INTEGER DEFAULT 0,
		owner TEXT DEFAULT '',
		tailscale_hostname TEXT,
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

	CREATE TABLE IF NOT EXISTS queues (
		task_id TEXT NOT NULL,
		name TEXT NOT NULL,
		mode TEXT NOT NULL DEFAULT 'serial',
		protected BOOLEAN NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'active',
		created_at DATETIME NOT NULL,
		PRIMARY KEY (task_id, name)
	);

	CREATE TABLE IF NOT EXISTS commands (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		queue_name TEXT NOT NULL,
		command TEXT NOT NULL,
		env TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		exit_code INTEGER,
		stop_on_failure BOOLEAN NOT NULL DEFAULT 1,
		output_path TEXT,
		created_at DATETIME NOT NULL,
		started_at DATETIME,
		finished_at DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_commands_task_queue ON commands(task_id, queue_name);
	CREATE INDEX IF NOT EXISTS idx_commands_status ON commands(status);
	`

	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}

	// Migration for existing databases: add columns if they don't exist
	// SQLite doesn't support IF NOT EXISTS for ALTER TABLE, so we ignore errors
	migrations := []string{
		`ALTER TABLE tasks ADD COLUMN tailscale_hostname TEXT`,
		`ALTER TABLE tasks ADD COLUMN cid INTEGER DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN owner TEXT DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN vsock_path TEXT DEFAULT ''`,
		`ALTER TABLE tasks DROP COLUMN repo`,
		`ALTER TABLE tasks DROP COLUMN ref`,
	}

	for _, migration := range migrations {
		// Ignore errors from ALTER TABLE - column may already exist or not exist
		s.db.Exec(migration)
	}

	return nil
}

// Close closes the database connection.
func (s *State) Close() error {
	return s.db.Close()
}

// SetStatusChangeCallback sets a callback that will be invoked when a task's status changes.
func (s *State) SetStatusChangeCallback(cb StatusChangeCallback) {
	s.callbackMu.Lock()
	defer s.callbackMu.Unlock()
	s.statusCallback = cb
}

// CreateTask creates a new task in the database.
func (s *State) CreateTask(task *Task) error {
	query := `
	INSERT INTO tasks (id, name, command, status, vmid, cid, vsock_path, owner, tailscale_hostname, created_at, stopped_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		task.ID,
		task.Name,
		task.Command,
		task.Status,
		task.VMID,
		task.CID,
		task.VsockPath,
		task.Owner,
		task.TailscaleHostname,
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
	SELECT id, name, command, status, vmid, cid, vsock_path, owner, tailscale_hostname, created_at, stopped_at
	FROM tasks
	WHERE id = ?
	`
	row := s.db.QueryRow(query, id)

	task := &Task{}
	var stoppedAt sql.NullTime
	var vmid sql.NullString
	var name sql.NullString
	var cid sql.NullInt64
	var vsockPath sql.NullString
	var owner sql.NullString
	var tailscaleHostname sql.NullString

	err := row.Scan(
		&task.ID,
		&name,
		&task.Command,
		&task.Status,
		&vmid,
		&cid,
		&vsockPath,
		&owner,
		&tailscaleHostname,
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
	if cid.Valid {
		task.CID = uint32(cid.Int64)
	}
	if vsockPath.Valid {
		task.VsockPath = vsockPath.String
	}
	if owner.Valid {
		task.Owner = owner.String
	}
	if tailscaleHostname.Valid {
		task.TailscaleHostname = tailscaleHostname.String
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
		SELECT id, name, command, status, vmid, cid, vsock_path, owner, tailscale_hostname, created_at, stopped_at
		FROM tasks
		ORDER BY created_at DESC
		`
	} else {
		query = `
		SELECT id, name, command, status, vmid, cid, vsock_path, owner, tailscale_hostname, created_at, stopped_at
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
		var cid sql.NullInt64
		var vsockPath sql.NullString
		var owner sql.NullString
		var tailscaleHostname sql.NullString

		err := rows.Scan(
			&task.ID,
			&name,
			&task.Command,
			&task.Status,
			&vmid,
			&cid,
			&vsockPath,
			&owner,
			&tailscaleHostname,
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
		if cid.Valid {
			task.CID = uint32(cid.Int64)
		}
		if vsockPath.Valid {
			task.VsockPath = vsockPath.String
		}
		if owner.Valid {
			task.Owner = owner.String
		}
		if tailscaleHostname.Valid {
			task.TailscaleHostname = tailscaleHostname.String
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
	// Get the old status before updating
	var oldStatus string
	err := s.db.QueryRow("SELECT status FROM tasks WHERE id = ?", id).Scan(&oldStatus)
	if err == sql.ErrNoRows {
		return fmt.Errorf("task not found: %s", id)
	}
	if err != nil {
		return fmt.Errorf("failed to get current task status: %w", err)
	}

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

	_, err = s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	// Call the callback if registered and status actually changed
	if oldStatus != status {
		s.callbackMu.RLock()
		cb := s.statusCallback
		s.callbackMu.RUnlock()
		if cb != nil {
			cb(id, oldStatus, status)
		}
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

// GetTaskByCID retrieves a running task by its Firecracker CID.
func (s *State) GetTaskByCID(cid uint32) (*Task, error) {
	query := `
	SELECT id, name, command, status, vmid, cid, vsock_path, owner, tailscale_hostname, created_at, stopped_at
	FROM tasks
	WHERE cid = ? AND status = 'running'
	`
	row := s.db.QueryRow(query, cid)

	task := &Task{}
	var stoppedAt sql.NullTime
	var vmid sql.NullString
	var name sql.NullString
	var cidVal sql.NullInt64
	var vsockPath sql.NullString
	var owner sql.NullString
	var tailscaleHostname sql.NullString

	err := row.Scan(
		&task.ID,
		&name,
		&task.Command,
		&task.Status,
		&vmid,
		&cidVal,
		&vsockPath,
		&owner,
		&tailscaleHostname,
		&task.CreatedAt,
		&stoppedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no running task with CID %d", cid)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get task by CID: %w", err)
	}

	if name.Valid {
		task.Name = name.String
	}
	if vmid.Valid {
		task.VMID = vmid.String
	}
	if cidVal.Valid {
		task.CID = uint32(cidVal.Int64)
	}
	if vsockPath.Valid {
		task.VsockPath = vsockPath.String
	}
	if owner.Valid {
		task.Owner = owner.String
	}
	if tailscaleHostname.Valid {
		task.TailscaleHostname = tailscaleHostname.String
	}
	if stoppedAt.Valid {
		task.StoppedAt = &stoppedAt.Time
	}

	return task, nil
}

// UpdateTaskCID updates the CID of a task.
func (s *State) UpdateTaskCID(id string, cid uint32) error {
	query := `UPDATE tasks SET cid = ? WHERE id = ?`
	result, err := s.db.Exec(query, cid, id)
	if err != nil {
		return fmt.Errorf("failed to update task CID: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("task not found: %s", id)
	}
	return nil
}

// UpdateTaskVsockPath updates the vsock path of a task.
func (s *State) UpdateTaskVsockPath(id string, vsockPath string) error {
	query := `UPDATE tasks SET vsock_path = ? WHERE id = ?`
	result, err := s.db.Exec(query, vsockPath, id)
	if err != nil {
		return fmt.Errorf("failed to update task vsock path: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
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

// CreateQueue inserts a new queue into the database.
func (s *State) CreateQueue(q *Queue) error {
	query := `
	INSERT INTO queues (task_id, name, mode, protected, status, created_at)
	VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query, q.TaskID, q.Name, q.Mode, q.Protected, q.Status, q.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create queue: %w", err)
	}
	return nil
}

// GetQueue retrieves a queue by task ID and name.
func (s *State) GetQueue(taskID, name string) (*Queue, error) {
	query := `
	SELECT task_id, name, mode, protected, status, created_at
	FROM queues
	WHERE task_id = ? AND name = ?
	`
	row := s.db.QueryRow(query, taskID, name)

	q := &Queue{}
	err := row.Scan(&q.TaskID, &q.Name, &q.Mode, &q.Protected, &q.Status, &q.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("queue not found: %s/%s", taskID, name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get queue: %w", err)
	}
	return q, nil
}

// ListQueues returns all queues for a given task.
func (s *State) ListQueues(taskID string) ([]*Queue, error) {
	query := `
	SELECT task_id, name, mode, protected, status, created_at
	FROM queues
	WHERE task_id = ?
	ORDER BY created_at ASC
	`
	rows, err := s.db.Query(query, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to list queues: %w", err)
	}
	defer rows.Close()

	var queues []*Queue
	for rows.Next() {
		q := &Queue{}
		if err := rows.Scan(&q.TaskID, &q.Name, &q.Mode, &q.Protected, &q.Status, &q.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan queue: %w", err)
		}
		queues = append(queues, q)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating queues: %w", err)
	}

	return queues, nil
}

// UpdateQueueStatus updates the status of a queue.
func (s *State) UpdateQueueStatus(taskID, name, status string) error {
	query := `UPDATE queues SET status = ? WHERE task_id = ? AND name = ?`
	result, err := s.db.Exec(query, status, taskID, name)
	if err != nil {
		return fmt.Errorf("failed to update queue status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("queue not found: %s/%s", taskID, name)
	}
	return nil
}

// DestroyQueue deletes a queue. Returns an error if the queue is protected.
func (s *State) DestroyQueue(taskID, name string) error {
	q, err := s.GetQueue(taskID, name)
	if err != nil {
		return err
	}
	if q.Protected {
		return fmt.Errorf("cannot destroy protected queue: %s/%s", taskID, name)
	}

	_, err = s.db.Exec(`DELETE FROM queues WHERE task_id = ? AND name = ?`, taskID, name)
	if err != nil {
		return fmt.Errorf("failed to destroy queue: %w", err)
	}
	return nil
}

// DeleteQueuesByTask removes all queues for a given task.
func (s *State) DeleteQueuesByTask(taskID string) error {
	_, err := s.db.Exec(`DELETE FROM queues WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("failed to delete queues by task: %w", err)
	}
	return nil
}

// CreateCommand inserts a new command into the database.
// The Command and Env fields are JSON-encoded.
func (s *State) CreateCommand(c *Command) error {
	cmdJSON, err := json.Marshal(c.Command)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	var envJSON []byte
	if c.Env != nil {
		envJSON, err = json.Marshal(c.Env)
		if err != nil {
			return fmt.Errorf("failed to marshal env: %w", err)
		}
	}

	query := `
	INSERT INTO commands (id, task_id, queue_name, command, env, status, exit_code, stop_on_failure, output_path, created_at, started_at, finished_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err = s.db.Exec(query,
		c.ID,
		c.TaskID,
		c.QueueName,
		string(cmdJSON),
		nullableString(envJSON),
		c.Status,
		c.ExitCode,
		c.StopOnFailure,
		c.OutputPath,
		c.CreatedAt,
		c.StartedAt,
		c.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create command: %w", err)
	}
	return nil
}

// nullableString converts a byte slice to sql.NullString.
func nullableString(b []byte) sql.NullString {
	if b == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

// GetCommand retrieves a command by ID.
func (s *State) GetCommand(id string) (*Command, error) {
	query := `
	SELECT id, task_id, queue_name, command, env, status, exit_code, stop_on_failure, output_path, created_at, started_at, finished_at
	FROM commands
	WHERE id = ?
	`
	row := s.db.QueryRow(query, id)
	return scanCommand(row)
}

func scanCommand(row *sql.Row) (*Command, error) {
	c := &Command{}
	var cmdJSON string
	var envJSON sql.NullString
	var exitCode sql.NullInt64
	var outputPath sql.NullString
	var startedAt sql.NullTime
	var finishedAt sql.NullTime

	err := row.Scan(
		&c.ID,
		&c.TaskID,
		&c.QueueName,
		&cmdJSON,
		&envJSON,
		&c.Status,
		&exitCode,
		&c.StopOnFailure,
		&outputPath,
		&c.CreatedAt,
		&startedAt,
		&finishedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("command not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get command: %w", err)
	}

	if err := json.Unmarshal([]byte(cmdJSON), &c.Command); err != nil {
		return nil, fmt.Errorf("failed to unmarshal command: %w", err)
	}
	if envJSON.Valid && envJSON.String != "" {
		if err := json.Unmarshal([]byte(envJSON.String), &c.Env); err != nil {
			return nil, fmt.Errorf("failed to unmarshal env: %w", err)
		}
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		c.ExitCode = &v
	}
	if outputPath.Valid {
		c.OutputPath = outputPath.String
	}
	if startedAt.Valid {
		c.StartedAt = &startedAt.Time
	}
	if finishedAt.Valid {
		c.FinishedAt = &finishedAt.Time
	}

	return c, nil
}

// ListCommands returns all commands for a given task and queue, ordered by created_at ASC.
func (s *State) ListCommands(taskID, queueName string) ([]*Command, error) {
	query := `
	SELECT id, task_id, queue_name, command, env, status, exit_code, stop_on_failure, output_path, created_at, started_at, finished_at
	FROM commands
	WHERE task_id = ? AND queue_name = ?
	ORDER BY created_at ASC
	`
	rows, err := s.db.Query(query, taskID, queueName)
	if err != nil {
		return nil, fmt.Errorf("failed to list commands: %w", err)
	}
	defer rows.Close()

	var cmds []*Command
	for rows.Next() {
		c := &Command{}
		var cmdJSON string
		var envJSON sql.NullString
		var exitCode sql.NullInt64
		var outputPath sql.NullString
		var startedAt sql.NullTime
		var finishedAt sql.NullTime

		err := rows.Scan(
			&c.ID,
			&c.TaskID,
			&c.QueueName,
			&cmdJSON,
			&envJSON,
			&c.Status,
			&exitCode,
			&c.StopOnFailure,
			&outputPath,
			&c.CreatedAt,
			&startedAt,
			&finishedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan command: %w", err)
		}

		if err := json.Unmarshal([]byte(cmdJSON), &c.Command); err != nil {
			return nil, fmt.Errorf("failed to unmarshal command: %w", err)
		}
		if envJSON.Valid && envJSON.String != "" {
			if err := json.Unmarshal([]byte(envJSON.String), &c.Env); err != nil {
				return nil, fmt.Errorf("failed to unmarshal env: %w", err)
			}
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			c.ExitCode = &v
		}
		if outputPath.Valid {
			c.OutputPath = outputPath.String
		}
		if startedAt.Valid {
			c.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			c.FinishedAt = &finishedAt.Time
		}

		cmds = append(cmds, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating commands: %w", err)
	}

	return cmds, nil
}

// UpdateCommandStatus updates the status of a command.
// If the status becomes "running", started_at is set to now.
func (s *State) UpdateCommandStatus(id, status string) error {
	var query string
	var args []interface{}

	if status == "running" {
		query = `UPDATE commands SET status = ?, started_at = ? WHERE id = ?`
		args = []interface{}{status, time.Now(), id}
	} else {
		query = `UPDATE commands SET status = ? WHERE id = ?`
		args = []interface{}{status, id}
	}

	result, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update command status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("command not found: %s", id)
	}
	return nil
}

// UpdateCommandExit sets the exit code, final status, and finished_at for a command.
// Status is set to "completed" if exitCode == 0, "failed" otherwise.
func (s *State) UpdateCommandExit(id string, exitCode int) error {
	status := "completed"
	if exitCode != 0 {
		status = "failed"
	}

	query := `UPDATE commands SET exit_code = ?, status = ?, finished_at = ? WHERE id = ?`
	result, err := s.db.Exec(query, exitCode, status, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update command exit: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("command not found: %s", id)
	}
	return nil
}

// FlushQueueCommands deletes all pending commands for a given task queue.
// Non-pending commands (running, completed, failed) are preserved.
func (s *State) FlushQueueCommands(taskID, queueName string) error {
	_, err := s.db.Exec(
		`DELETE FROM commands WHERE task_id = ? AND queue_name = ? AND status = 'pending'`,
		taskID, queueName,
	)
	if err != nil {
		return fmt.Errorf("failed to flush queue commands: %w", err)
	}
	return nil
}

// DeleteCommandsByTask removes all commands for a given task.
func (s *State) DeleteCommandsByTask(taskID string) error {
	_, err := s.db.Exec(`DELETE FROM commands WHERE task_id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("failed to delete commands by task: %w", err)
	}
	return nil
}

// Package daemon provides state management for the stockyard daemon.
package daemon

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Sentinel errors for the State layer. Use errors.Is to check these
// in callers (e.g. gRPC handlers) instead of string matching.
var (
	ErrTaskNotFound       = errors.New("task not found")
	ErrTaskNotStopped     = errors.New("task is not stopped")
	ErrTaskCleanupPending = errors.New("task cleanup is pending")
)

const TaskStatusCleanupPending = "cleanup_pending"

// Task represents a running or completed task in the system.
type Task struct {
	ID                string
	Name              string
	Command           string
	Status            string
	VMID              string
	CID               uint32 // Firecracker vsock Context ID
	VsockPath         string // Path to vsock UDS
	IP                string // Direct IP address (for macOS/non-Tailscale access)
	Owner             string // Username who created the task
	TailscaleHostname string
	Image             string // Resolved OCI image ref the task runs (PRI-2150)
	CreatedAt         time.Time
	StoppedAt         *time.Time
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
	// modernc's SQLite driver applies no busy_timeout unless one is set in the
	// DSN. The daemon shares one *sql.DB across many goroutines, so without it
	// a concurrent write fails immediately with SQLITE_BUSY ("database is
	// locked") instead of waiting for the lock to clear. The pragma is applied
	// to every connection the pool opens.
	dsn := dbPath + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
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
		image TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		stopped_at DATETIME
	);

	CREATE TABLE IF NOT EXISTS snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id TEXT NOT NULL,
		snapshot_name TEXT NOT NULL,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS images (
		name TEXT PRIMARY KEY,
		dataset TEXT NOT NULL UNIQUE,
		kernel_path TEXT DEFAULT '',
		size_bytes INTEGER DEFAULT 0,
		created_at DATETIME NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_snapshots_task_id ON snapshots(task_id);
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
		`ALTER TABLE tasks ADD COLUMN ip TEXT DEFAULT ''`,
		`ALTER TABLE tasks DROP COLUMN repo`,
		`ALTER TABLE tasks DROP COLUMN ref`,
		`ALTER TABLE tasks ADD COLUMN image TEXT DEFAULT ''`,
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
	if task.Status == TaskStatusCleanupPending {
		return fmt.Errorf("%w: only destruction may retain cleanup pending", ErrTaskCleanupPending)
	}
	query := `
	INSERT INTO tasks (id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, image, created_at, stopped_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := s.db.Exec(query,
		task.ID,
		task.Name,
		task.Command,
		task.Status,
		task.VMID,
		task.CID,
		task.VsockPath,
		task.IP,
		task.Owner,
		task.TailscaleHostname,
		task.Image,
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
	SELECT id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, image, created_at, stopped_at
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
	var ip sql.NullString
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
		&ip,
		&owner,
		&tailscaleHostname,
		&task.Image,
		&task.CreatedAt,
		&stoppedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, id)
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
	if ip.Valid {
		task.IP = ip.String
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
		SELECT id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, image, created_at, stopped_at
		FROM tasks
		ORDER BY created_at DESC
		`
	} else {
		query = `
		SELECT id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, image, created_at, stopped_at
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
		var ip sql.NullString
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
			&ip,
			&owner,
			&tailscaleHostname,
			&task.Image,
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
		if ip.Valid {
			task.IP = ip.String
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
	if status == TaskStatusCleanupPending {
		return fmt.Errorf("%w: use destruction transaction", ErrTaskCleanupPending)
	}
	// Get the old status before updating
	var oldStatus string
	err := s.db.QueryRow("SELECT status FROM tasks WHERE id = ?", id).Scan(&oldStatus)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
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

// MarkTaskCleanupPending records that resource cleanup is complete enough to
// release the retained allocation and remove the row on a later retry.
func (s *State) MarkTaskCleanupPending(id string) error {
	result, err := s.db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, TaskStatusCleanupPending, id)
	if err != nil {
		return fmt.Errorf("mark task cleanup pending: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark task cleanup pending rows: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
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
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}

	return nil
}

// GetTaskByCID retrieves a running task by its Firecracker CID.
func (s *State) GetTaskByCID(cid uint32) (*Task, error) {
	query := `
	SELECT id, name, command, status, vmid, cid, vsock_path, ip, owner, tailscale_hostname, image, created_at, stopped_at
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
	var ip sql.NullString
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
		&ip,
		&owner,
		&tailscaleHostname,
		&task.Image,
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
	if ip.Valid {
		task.IP = ip.String
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
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
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
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	return nil
}

// UpdateTaskIP updates the IP address of a task.
func (s *State) UpdateTaskIP(id string, ip string) error {
	query := `UPDATE tasks SET ip = ? WHERE id = ?`
	result, err := s.db.Exec(query, ip, id)
	if err != nil {
		return fmt.Errorf("failed to update task IP: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
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
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
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

// ImageRecord is one registered Firecracker image (PRI-2150 phase 2).
type ImageRecord struct {
	Name       string
	Dataset    string // ZFS dataset component under images path
	KernelPath string // empty = shared default kernel
	SizeBytes  int64
	CreatedAt  time.Time
}

// CreateImage inserts a new image record. Returns an error if name or dataset already exists.
func (s *State) CreateImage(rec *ImageRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO images (name, dataset, kernel_path, size_bytes, created_at) VALUES (?, ?, ?, ?, ?)`,
		rec.Name, rec.Dataset, rec.KernelPath, rec.SizeBytes, rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create image: %w", err)
	}
	return nil
}

// GetImage retrieves an image by name; returns an error when absent.
func (s *State) GetImage(name string) (*ImageRecord, error) {
	row := s.db.QueryRow(
		`SELECT name, dataset, kernel_path, size_bytes, created_at FROM images WHERE name = ?`,
		name,
	)
	rec := &ImageRecord{}
	err := row.Scan(&rec.Name, &rec.Dataset, &rec.KernelPath, &rec.SizeBytes, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("image not found: %s", name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}
	return rec, nil
}

// GetImageByDataset retrieves an image by its ZFS dataset component; returns an
// error when absent. Used as a collision pre-check before any ZFS mutation.
func (s *State) GetImageByDataset(dataset string) (*ImageRecord, error) {
	row := s.db.QueryRow(
		`SELECT name, dataset, kernel_path, size_bytes, created_at FROM images WHERE dataset = ?`,
		dataset,
	)
	rec := &ImageRecord{}
	err := row.Scan(&rec.Name, &rec.Dataset, &rec.KernelPath, &rec.SizeBytes, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("image with dataset %q not found", dataset)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get image by dataset: %w", err)
	}
	return rec, nil
}

// ListImages returns all registered images ordered by name.
func (s *State) ListImages() ([]*ImageRecord, error) {
	rows, err := s.db.Query(
		`SELECT name, dataset, kernel_path, size_bytes, created_at FROM images ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	defer rows.Close()

	var recs []*ImageRecord
	for rows.Next() {
		rec := &ImageRecord{}
		if err := rows.Scan(&rec.Name, &rec.Dataset, &rec.KernelPath, &rec.SizeBytes, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan image: %w", err)
		}
		recs = append(recs, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating images: %w", err)
	}
	return recs, nil
}

// UpdateImageSize updates the size_bytes field for a registered image.
// Used by EnsureDefault's self-heal branch to reflect the actual rootfs size
// after a re-import (the stored value may be stale if the prior import was
// interrupted before the row was updated).
func (s *State) UpdateImageSize(name string, sizeBytes int64) error {
	_, err := s.db.Exec(`UPDATE images SET size_bytes = ? WHERE name = ?`, sizeBytes, name)
	if err != nil {
		return fmt.Errorf("failed to update image size: %w", err)
	}
	return nil
}

// DeleteImage removes an image record by name.
func (s *State) DeleteImage(name string) error {
	result, err := s.db.Exec(`DELETE FROM images WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("image not found: %s", name)
	}
	return nil
}

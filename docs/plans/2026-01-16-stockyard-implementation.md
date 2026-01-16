# Stockyard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a CLI and daemon for running coding agents in isolated Firecracker micro-VMs with ZFS-based audit trail snapshots.

**Architecture:** Daemon-centric design where `stockyardd` manages VM lifecycle via Flintlock, ZFS datasets/snapshots, and exposes gRPC API. CLI (`stockyard`) is a thin client. VMs get workspaces via virtio-fs, trigger snapshots via vsock, and join Tailscale for SSH access.

**Tech Stack:** Go 1.21+, Cobra (CLI), gRPC, Flintlock, ZFS, Firecracker, Tailscale, 1Password CLI, SQLite

**Reference:** Design doc at `docs/plans/2026-01-16-stockyard-design.md`

---

## Phase 1: Project Foundation

### Task 1.1: Initialize Go Module and Directory Structure

**Files:**
- Create: `go.mod`
- Create: `cmd/stockyard/main.go`
- Create: `cmd/stockyardd/main.go`
- Create: `pkg/version/version.go`

**Step 1: Initialize Go module**

```bash
cd /home/jesse/git/stockyard
go mod init github.com/obra/stockyard
```

**Step 2: Create directory structure**

```bash
mkdir -p cmd/stockyard cmd/stockyardd
mkdir -p pkg/{api,config,daemon,flintlock,secrets,snapshots,tailscale,vm,vsock,zfs}
mkdir -p vm-image/scripts
```

**Step 3: Create version package**

```go
// pkg/version/version.go
package version

var (
    Version   = "0.1.0-dev"
    GitCommit = "unknown"
    BuildDate = "unknown"
)
```

**Step 4: Create CLI entry point**

```go
// cmd/stockyard/main.go
package main

import (
    "fmt"
    "os"

    "github.com/obra/stockyard/pkg/version"
)

func main() {
    if len(os.Args) > 1 && os.Args[1] == "version" {
        fmt.Printf("stockyard %s\n", version.Version)
        os.Exit(0)
    }
    fmt.Println("stockyard - coding agent VM orchestrator")
}
```

**Step 5: Create daemon entry point**

```go
// cmd/stockyardd/main.go
package main

import (
    "fmt"
    "os"

    "github.com/obra/stockyard/pkg/version"
)

func main() {
    if len(os.Args) > 1 && os.Args[1] == "version" {
        fmt.Printf("stockyardd %s\n", version.Version)
        os.Exit(0)
    }
    fmt.Println("stockyardd - stockyard daemon")
}
```

**Step 6: Verify builds**

```bash
go build -o bin/stockyard ./cmd/stockyard
go build -o bin/stockyardd ./cmd/stockyardd
./bin/stockyard version
./bin/stockyardd version
```

Expected: Both print version "0.1.0-dev"

**Step 7: Commit**

```bash
git add go.mod cmd/ pkg/version/ bin/
echo "bin/" >> .gitignore
git add .gitignore
git commit -m "feat: initialize Go project structure

- Go module github.com/obra/stockyard
- CLI and daemon entry points
- Version package
- Directory structure for all packages"
```

---

### Task 1.2: Add Cobra CLI Framework

**Files:**
- Modify: `cmd/stockyard/main.go`
- Create: `cmd/stockyard/root.go`
- Create: `cmd/stockyard/version.go`

**Step 1: Add Cobra dependency**

```bash
go get github.com/spf13/cobra@latest
```

**Step 2: Create root command**

```go
// cmd/stockyard/root.go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
    Use:   "stockyard",
    Short: "Coding agent VM orchestrator",
    Long:  `Stockyard runs coding agents in isolated Firecracker micro-VMs with ZFS-based audit trail snapshots.`,
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

**Step 3: Create version command**

```go
// cmd/stockyard/version.go
package main

import (
    "fmt"

    "github.com/obra/stockyard/pkg/version"
    "github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
    Use:   "version",
    Short: "Print version information",
    Run: func(cmd *cobra.Command, args []string) {
        fmt.Printf("stockyard %s\n", version.Version)
        fmt.Printf("  commit: %s\n", version.GitCommit)
        fmt.Printf("  built:  %s\n", version.BuildDate)
    },
}

func init() {
    rootCmd.AddCommand(versionCmd)
}
```

**Step 4: Update main.go**

```go
// cmd/stockyard/main.go
package main

func main() {
    Execute()
}
```

**Step 5: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard --help
./bin/stockyard version
```

Expected: Help shows available commands, version shows version info

**Step 6: Commit**

```bash
go mod tidy
git add go.mod go.sum cmd/stockyard/
git commit -m "feat: add Cobra CLI framework

- Root command with help text
- Version command"
```

---

### Task 1.3: Add Configuration Package

**Files:**
- Create: `pkg/config/config.go`
- Create: `pkg/config/config_test.go`

**Step 1: Write failing test**

```go
// pkg/config/config_test.go
package config

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLoadConfig_DefaultsWhenNoFile(t *testing.T) {
    // Use temp directory
    tmpDir := t.TempDir()

    cfg, err := LoadFromDir(tmpDir)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if cfg.InstanceID != "" {
        t.Errorf("expected empty instance ID, got %q", cfg.InstanceID)
    }

    if cfg.Secrets.Provider != "1password" {
        t.Errorf("expected default provider '1password', got %q", cfg.Secrets.Provider)
    }
}

func TestSaveAndLoadConfig(t *testing.T) {
    tmpDir := t.TempDir()

    cfg := &Config{
        InstanceID: "test-instance",
        Secrets: SecretsConfig{
            Provider: "1password",
            Vault:    "TestVault",
            Prefix:   "test-instance",
        },
    }

    err := cfg.SaveToDir(tmpDir)
    if err != nil {
        t.Fatalf("failed to save: %v", err)
    }

    // Verify file exists
    configPath := filepath.Join(tmpDir, "config.json")
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        t.Fatal("config file not created")
    }

    // Load it back
    loaded, err := LoadFromDir(tmpDir)
    if err != nil {
        t.Fatalf("failed to load: %v", err)
    }

    if loaded.InstanceID != cfg.InstanceID {
        t.Errorf("instance ID mismatch: got %q, want %q", loaded.InstanceID, cfg.InstanceID)
    }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/config/... -v
```

Expected: FAIL - package doesn't exist

**Step 3: Implement config package**

```go
// pkg/config/config.go
package config

import (
    "encoding/json"
    "os"
    "path/filepath"
)

// Config represents the stockyard configuration
type Config struct {
    InstanceID string        `json:"instance_id"`
    Secrets    SecretsConfig `json:"secrets"`
    Daemon     DaemonConfig  `json:"daemon"`
}

// SecretsConfig configures the secrets provider
type SecretsConfig struct {
    Provider string `json:"provider"` // "1password" or "aws"
    Vault    string `json:"vault"`    // 1Password vault name
    Prefix   string `json:"prefix"`   // Secret path prefix (instance ID)
}

// DaemonConfig configures the daemon connection
type DaemonConfig struct {
    SocketPath string `json:"socket_path"`
}

// DefaultConfig returns configuration with sensible defaults
func DefaultConfig() *Config {
    return &Config{
        Secrets: SecretsConfig{
            Provider: "1password",
            Vault:    "Stockyard",
        },
        Daemon: DaemonConfig{
            SocketPath: "/var/run/stockyard/stockyard.sock",
        },
    }
}

// LoadFromDir loads config from a directory, returns defaults if not found
func LoadFromDir(dir string) (*Config, error) {
    configPath := filepath.Join(dir, "config.json")

    data, err := os.ReadFile(configPath)
    if os.IsNotExist(err) {
        return DefaultConfig(), nil
    }
    if err != nil {
        return nil, err
    }

    cfg := DefaultConfig()
    if err := json.Unmarshal(data, cfg); err != nil {
        return nil, err
    }

    return cfg, nil
}

// SaveToDir saves config to a directory
func (c *Config) SaveToDir(dir string) error {
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }

    configPath := filepath.Join(dir, "config.json")

    data, err := json.MarshalIndent(c, "", "  ")
    if err != nil {
        return err
    }

    return os.WriteFile(configPath, data, 0644)
}

// Load loads config from the default location (~/.config/stockyard/)
func Load() (*Config, error) {
    configDir, err := ConfigDir()
    if err != nil {
        return nil, err
    }
    return LoadFromDir(configDir)
}

// Save saves config to the default location
func (c *Config) Save() error {
    configDir, err := ConfigDir()
    if err != nil {
        return err
    }
    return c.SaveToDir(configDir)
}

// ConfigDir returns the configuration directory path
func ConfigDir() (string, error) {
    if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
        return filepath.Join(xdg, "stockyard"), nil
    }

    home, err := os.UserHomeDir()
    if err != nil {
        return "", err
    }

    return filepath.Join(home, ".config", "stockyard"), nil
}
```

**Step 4: Run tests**

```bash
go test ./pkg/config/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/config/
git commit -m "feat: add configuration package

- Config struct with instance ID, secrets, daemon settings
- XDG-compliant config directory
- Load/save with JSON serialization
- Default values for new installs"
```

---

### Task 1.4: Add Init Command

**Files:**
- Create: `cmd/stockyard/init.go`

**Step 1: Create init command**

```go
// cmd/stockyard/init.go
package main

import (
    "fmt"
    "os"

    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var initInstanceName string

var initCmd = &cobra.Command{
    Use:   "init",
    Short: "Initialize stockyard configuration",
    Long:  `Initialize stockyard configuration with an instance name.`,
    RunE: func(cmd *cobra.Command, args []string) error {
        if initInstanceName == "" {
            return fmt.Errorf("--instance is required")
        }

        cfg, err := config.Load()
        if err != nil {
            return fmt.Errorf("failed to load config: %w", err)
        }

        if cfg.InstanceID != "" {
            fmt.Printf("Warning: overwriting existing instance ID %q\n", cfg.InstanceID)
        }

        cfg.InstanceID = initInstanceName
        cfg.Secrets.Prefix = initInstanceName

        if err := cfg.Save(); err != nil {
            return fmt.Errorf("failed to save config: %w", err)
        }

        configDir, _ := config.ConfigDir()
        fmt.Printf("Initialized stockyard instance %q\n", initInstanceName)
        fmt.Printf("Config saved to %s/config.json\n", configDir)
        fmt.Printf("\nNext steps:\n")
        fmt.Printf("  1. Create 1Password vault 'Stockyard' (if not exists)\n")
        fmt.Printf("  2. Add secrets under op://Stockyard/%s/\n", initInstanceName)
        fmt.Printf("     - anthropic-api-key\n")
        fmt.Printf("     - github-token\n")
        fmt.Printf("     - tailscale-auth-key\n")

        return nil
    },
}

func init() {
    initCmd.Flags().StringVar(&initInstanceName, "instance", "", "Instance name (required)")
    initCmd.MarkFlagRequired("instance")
    rootCmd.AddCommand(initCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard init --help
./bin/stockyard init --instance test-local
cat ~/.config/stockyard/config.json
```

Expected: Config file created with instance ID "test-local"

**Step 3: Commit**

```bash
git add cmd/stockyard/init.go
git commit -m "feat: add init command

- Initialize instance with --instance flag
- Configure secrets prefix
- Show next steps for 1Password setup"
```

---

## Phase 2: Secrets Integration

### Task 2.1: Create Secrets Provider Interface

**Files:**
- Create: `pkg/secrets/provider.go`
- Create: `pkg/secrets/provider_test.go`

**Step 1: Write failing test**

```go
// pkg/secrets/provider_test.go
package secrets

import (
    "context"
    "testing"
)

func TestMockProvider(t *testing.T) {
    mock := &MockProvider{
        Secrets: map[string]string{
            "api-key": "test-secret-value",
        },
    }

    val, err := mock.GetSecret(context.Background(), "api-key")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if val != "test-secret-value" {
        t.Errorf("got %q, want %q", val, "test-secret-value")
    }

    _, err = mock.GetSecret(context.Background(), "nonexistent")
    if err == nil {
        t.Error("expected error for nonexistent secret")
    }
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/secrets/... -v
```

Expected: FAIL

**Step 3: Implement provider interface**

```go
// pkg/secrets/provider.go
package secrets

import (
    "context"
    "fmt"
)

// Provider is the interface for secret retrieval
type Provider interface {
    // GetSecret retrieves a secret by name
    GetSecret(ctx context.Context, name string) (string, error)

    // Name returns the provider name
    Name() string
}

// MockProvider is a test provider
type MockProvider struct {
    Secrets map[string]string
}

func (m *MockProvider) GetSecret(ctx context.Context, name string) (string, error) {
    if val, ok := m.Secrets[name]; ok {
        return val, nil
    }
    return "", fmt.Errorf("secret %q not found", name)
}

func (m *MockProvider) Name() string {
    return "mock"
}
```

**Step 4: Run tests**

```bash
go test ./pkg/secrets/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/secrets/
git commit -m "feat: add secrets provider interface

- Provider interface with GetSecret method
- MockProvider for testing"
```

---

### Task 2.2: Implement 1Password Provider

**Files:**
- Create: `pkg/secrets/onepassword.go`
- Create: `pkg/secrets/onepassword_test.go`

**Step 1: Write test**

```go
// pkg/secrets/onepassword_test.go
package secrets

import (
    "testing"
)

func TestOnePasswordProvider_BuildPath(t *testing.T) {
    p := &OnePasswordProvider{
        Vault:  "Stockyard",
        Prefix: "flower-garden",
    }

    path := p.buildPath("anthropic-api-key")
    expected := "op://Stockyard/flower-garden/anthropic-api-key"

    if path != expected {
        t.Errorf("got %q, want %q", path, expected)
    }
}
```

**Step 2: Run test**

```bash
go test ./pkg/secrets/... -v -run TestOnePassword
```

Expected: FAIL

**Step 3: Implement 1Password provider**

```go
// pkg/secrets/onepassword.go
package secrets

import (
    "bytes"
    "context"
    "fmt"
    "os/exec"
    "strings"
)

// OnePasswordProvider retrieves secrets from 1Password CLI
type OnePasswordProvider struct {
    Vault  string
    Prefix string
}

// NewOnePasswordProvider creates a new 1Password provider
func NewOnePasswordProvider(vault, prefix string) *OnePasswordProvider {
    return &OnePasswordProvider{
        Vault:  vault,
        Prefix: prefix,
    }
}

func (p *OnePasswordProvider) buildPath(name string) string {
    return fmt.Sprintf("op://%s/%s/%s", p.Vault, p.Prefix, name)
}

func (p *OnePasswordProvider) GetSecret(ctx context.Context, name string) (string, error) {
    path := p.buildPath(name)

    cmd := exec.CommandContext(ctx, "op", "read", path)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("op read failed: %w: %s", err, stderr.String())
    }

    return strings.TrimSpace(stdout.String()), nil
}

func (p *OnePasswordProvider) Name() string {
    return "1password"
}
```

**Step 4: Run tests**

```bash
go test ./pkg/secrets/... -v
```

Expected: PASS (unit tests don't call actual `op` CLI)

**Step 5: Commit**

```bash
git add pkg/secrets/onepassword.go pkg/secrets/onepassword_test.go
git commit -m "feat: add 1Password secrets provider

- Builds op:// paths from vault and prefix
- Calls op read CLI command
- Trims whitespace from output"
```

---

## Phase 3: ZFS Management

### Task 3.1: Create ZFS Package

**Files:**
- Create: `pkg/zfs/zfs.go`
- Create: `pkg/zfs/zfs_test.go`

**Step 1: Write tests**

```go
// pkg/zfs/zfs_test.go
package zfs

import (
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

func TestSnapshotName(t *testing.T) {
    name := BuildSnapshotName("task-123", "edit-main.py")

    // Should contain task ID and label
    if name == "" {
        t.Error("snapshot name should not be empty")
    }

    // Should be valid ZFS snapshot name (no spaces, reasonable chars)
    for _, c := range name {
        if c == ' ' || c == '@' {
            t.Errorf("invalid character in snapshot name: %q", name)
        }
    }
}
```

**Step 2: Run tests**

```bash
go test ./pkg/zfs/... -v
```

Expected: FAIL

**Step 3: Implement ZFS package**

```go
// pkg/zfs/zfs.go
package zfs

import (
    "bytes"
    "context"
    "fmt"
    "os/exec"
    "strings"
    "time"
)

// Manager handles ZFS operations
type Manager struct {
    PoolName    string
    BasePath    string // e.g., "stockyard/workspaces"
}

// NewManager creates a new ZFS manager
func NewManager(pool, basePath string) *Manager {
    return &Manager{
        PoolName: pool,
        BasePath: basePath,
    }
}

// ParseDatasetName splits a dataset name into pool and path
func ParseDatasetName(name string) (pool, path string) {
    parts := strings.SplitN(name, "/", 2)
    if len(parts) == 1 {
        return parts[0], ""
    }
    return parts[0], parts[1]
}

// BuildSnapshotName creates a snapshot name from task ID and label
func BuildSnapshotName(taskID, label string) string {
    timestamp := time.Now().Format("2006-01-02T15-04-05")
    // Sanitize label for ZFS
    safeLabel := strings.Map(func(r rune) rune {
        if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
            (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
            return r
        }
        return '-'
    }, label)

    if safeLabel == "" {
        return fmt.Sprintf("%s-%s", taskID, timestamp)
    }
    return fmt.Sprintf("%s-%s-%s", taskID, timestamp, safeLabel)
}

// DatasetPath returns the full dataset path for a task
func (m *Manager) DatasetPath(taskID string) string {
    return fmt.Sprintf("%s/%s/%s", m.PoolName, m.BasePath, taskID)
}

// CreateDataset creates a new dataset for a task
func (m *Manager) CreateDataset(ctx context.Context, taskID string) error {
    dataset := m.DatasetPath(taskID)
    return m.runZFS(ctx, "create", "-p", dataset)
}

// DestroyDataset destroys a dataset and all its snapshots
func (m *Manager) DestroyDataset(ctx context.Context, taskID string) error {
    dataset := m.DatasetPath(taskID)
    return m.runZFS(ctx, "destroy", "-r", dataset)
}

// CreateSnapshot creates a snapshot with the given label
func (m *Manager) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
    dataset := m.DatasetPath(taskID)
    snapName := BuildSnapshotName(taskID, label)
    fullName := fmt.Sprintf("%s@%s", dataset, snapName)

    if err := m.runZFS(ctx, "snapshot", fullName); err != nil {
        return "", err
    }
    return snapName, nil
}

// ListSnapshots lists all snapshots for a task
func (m *Manager) ListSnapshots(ctx context.Context, taskID string) ([]string, error) {
    dataset := m.DatasetPath(taskID)

    cmd := exec.CommandContext(ctx, "zfs", "list", "-t", "snapshot", "-H", "-o", "name", "-r", dataset)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return nil, fmt.Errorf("zfs list failed: %w: %s", err, stderr.String())
    }

    var snapshots []string
    for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
        if line != "" {
            // Extract just the snapshot name (after @)
            if idx := strings.LastIndex(line, "@"); idx != -1 {
                snapshots = append(snapshots, line[idx+1:])
            }
        }
    }
    return snapshots, nil
}

// GetMountpoint returns the mountpoint for a dataset
func (m *Manager) GetMountpoint(ctx context.Context, taskID string) (string, error) {
    dataset := m.DatasetPath(taskID)

    cmd := exec.CommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", dataset)
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return "", fmt.Errorf("zfs get mountpoint failed: %w: %s", err, stderr.String())
    }

    return strings.TrimSpace(stdout.String()), nil
}

func (m *Manager) runZFS(ctx context.Context, args ...string) error {
    cmd := exec.CommandContext(ctx, "zfs", args...)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return fmt.Errorf("zfs %s failed: %w: %s", args[0], err, stderr.String())
    }
    return nil
}
```

**Step 4: Run tests**

```bash
go test ./pkg/zfs/... -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/zfs/
git commit -m "feat: add ZFS management package

- Dataset create/destroy operations
- Snapshot create/list operations
- Mountpoint retrieval
- Safe snapshot name generation"
```

---

## Phase 4: Daemon Foundation

### Task 4.1: Create Daemon Package Structure

**Files:**
- Create: `pkg/daemon/daemon.go`
- Create: `pkg/daemon/state.go`

**Step 1: Create daemon core**

```go
// pkg/daemon/daemon.go
package daemon

import (
    "context"
    "fmt"
    "net"
    "os"
    "path/filepath"
    "sync"

    "github.com/obra/stockyard/pkg/config"
    "github.com/obra/stockyard/pkg/secrets"
    "github.com/obra/stockyard/pkg/zfs"
)

// Daemon is the main stockyard daemon
type Daemon struct {
    cfg      *config.Config
    secrets  secrets.Provider
    zfs      *zfs.Manager
    state    *State

    listener net.Listener
    mu       sync.Mutex
    running  bool
}

// Config for daemon initialization
type Config struct {
    SocketPath string
    ZFSPool    string
    ZFSBase    string // e.g., "stockyard/workspaces"
}

// New creates a new daemon instance
func New(cfg *config.Config, secretsProvider secrets.Provider) (*Daemon, error) {
    zfsMgr := zfs.NewManager("tank", "stockyard/workspaces")

    state, err := NewState()
    if err != nil {
        return nil, fmt.Errorf("failed to initialize state: %w", err)
    }

    return &Daemon{
        cfg:     cfg,
        secrets: secretsProvider,
        zfs:     zfsMgr,
        state:   state,
    }, nil
}

// Start starts the daemon
func (d *Daemon) Start(ctx context.Context) error {
    d.mu.Lock()
    if d.running {
        d.mu.Unlock()
        return fmt.Errorf("daemon already running")
    }
    d.running = true
    d.mu.Unlock()

    // Ensure socket directory exists
    socketDir := filepath.Dir(d.cfg.Daemon.SocketPath)
    if err := os.MkdirAll(socketDir, 0755); err != nil {
        return fmt.Errorf("failed to create socket directory: %w", err)
    }

    // Remove stale socket
    os.Remove(d.cfg.Daemon.SocketPath)

    listener, err := net.Listen("unix", d.cfg.Daemon.SocketPath)
    if err != nil {
        return fmt.Errorf("failed to listen on socket: %w", err)
    }
    d.listener = listener

    fmt.Printf("Daemon listening on %s\n", d.cfg.Daemon.SocketPath)

    // TODO: Start gRPC server

    <-ctx.Done()
    return d.Stop()
}

// Stop stops the daemon
func (d *Daemon) Stop() error {
    d.mu.Lock()
    defer d.mu.Unlock()

    if !d.running {
        return nil
    }

    d.running = false

    if d.listener != nil {
        d.listener.Close()
    }

    if d.state != nil {
        d.state.Close()
    }

    return nil
}
```

**Step 2: Create state management**

```go
// pkg/daemon/state.go
package daemon

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    "time"

    _ "github.com/mattn/go-sqlite3"
)

// State manages daemon state in SQLite
type State struct {
    db *sql.DB
}

// Task represents a running or completed task
type Task struct {
    ID          string
    Name        string
    Repo        string
    Ref         string
    Command     string
    Status      string // "running", "stopped", "failed"
    VMID        string
    CreatedAt   time.Time
    StoppedAt   *time.Time
}

// NewState creates a new state manager
func NewState() (*State, error) {
    dataDir, err := dataDir()
    if err != nil {
        return nil, err
    }

    if err := os.MkdirAll(dataDir, 0755); err != nil {
        return nil, err
    }

    dbPath := filepath.Join(dataDir, "state.db")
    db, err := sql.Open("sqlite3", dbPath)
    if err != nil {
        return nil, fmt.Errorf("failed to open database: %w", err)
    }

    s := &State{db: db}
    if err := s.migrate(); err != nil {
        db.Close()
        return nil, fmt.Errorf("failed to migrate database: %w", err)
    }

    return s, nil
}

func (s *State) migrate() error {
    _, err := s.db.Exec(`
        CREATE TABLE IF NOT EXISTS tasks (
            id TEXT PRIMARY KEY,
            name TEXT,
            repo TEXT NOT NULL,
            ref TEXT NOT NULL,
            command TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'running',
            vm_id TEXT,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            stopped_at DATETIME
        );

        CREATE TABLE IF NOT EXISTS snapshots (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            task_id TEXT NOT NULL,
            name TEXT NOT NULL,
            label TEXT,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (task_id) REFERENCES tasks(id)
        );

        CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
        CREATE INDEX IF NOT EXISTS idx_snapshots_task ON snapshots(task_id);
    `)
    return err
}

// CreateTask creates a new task record
func (s *State) CreateTask(task *Task) error {
    _, err := s.db.Exec(
        `INSERT INTO tasks (id, name, repo, ref, command, status, vm_id, created_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        task.ID, task.Name, task.Repo, task.Ref, task.Command, task.Status, task.VMID, task.CreatedAt,
    )
    return err
}

// GetTask retrieves a task by ID
func (s *State) GetTask(id string) (*Task, error) {
    row := s.db.QueryRow(
        `SELECT id, name, repo, ref, command, status, vm_id, created_at, stopped_at
         FROM tasks WHERE id = ?`, id,
    )

    var task Task
    err := row.Scan(&task.ID, &task.Name, &task.Repo, &task.Ref, &task.Command,
        &task.Status, &task.VMID, &task.CreatedAt, &task.StoppedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    return &task, nil
}

// ListTasks lists all tasks, optionally filtered by status
func (s *State) ListTasks(status string) ([]*Task, error) {
    query := `SELECT id, name, repo, ref, command, status, vm_id, created_at, stopped_at FROM tasks`
    var args []interface{}

    if status != "" {
        query += ` WHERE status = ?`
        args = append(args, status)
    }
    query += ` ORDER BY created_at DESC`

    rows, err := s.db.Query(query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var tasks []*Task
    for rows.Next() {
        var task Task
        if err := rows.Scan(&task.ID, &task.Name, &task.Repo, &task.Ref, &task.Command,
            &task.Status, &task.VMID, &task.CreatedAt, &task.StoppedAt); err != nil {
            return nil, err
        }
        tasks = append(tasks, &task)
    }
    return tasks, nil
}

// UpdateTaskStatus updates a task's status
func (s *State) UpdateTaskStatus(id, status string) error {
    var stoppedAt interface{}
    if status == "stopped" || status == "failed" {
        stoppedAt = time.Now()
    }

    _, err := s.db.Exec(
        `UPDATE tasks SET status = ?, stopped_at = ? WHERE id = ?`,
        status, stoppedAt, id,
    )
    return err
}

// Close closes the database connection
func (s *State) Close() error {
    return s.db.Close()
}

func dataDir() (string, error) {
    if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
        return filepath.Join(xdg, "stockyard"), nil
    }

    home, err := os.UserHomeDir()
    if err != nil {
        return "", err
    }

    return filepath.Join(home, ".local", "share", "stockyard"), nil
}
```

**Step 3: Add SQLite dependency**

```bash
go get github.com/mattn/go-sqlite3
```

**Step 4: Verify compilation**

```bash
go build ./pkg/daemon/...
```

Expected: Compiles without errors

**Step 5: Commit**

```bash
go mod tidy
git add pkg/daemon/ go.mod go.sum
git commit -m "feat: add daemon foundation

- Daemon struct with lifecycle management
- Unix socket listener
- SQLite state management
- Task and snapshot tables"
```

---

### Task 4.2: Update Daemon Entry Point

**Files:**
- Modify: `cmd/stockyardd/main.go`

**Step 1: Update daemon main**

```go
// cmd/stockyardd/main.go
package main

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "syscall"

    "github.com/obra/stockyard/pkg/config"
    "github.com/obra/stockyard/pkg/daemon"
    "github.com/obra/stockyard/pkg/secrets"
    "github.com/obra/stockyard/pkg/version"
)

func main() {
    if len(os.Args) > 1 && os.Args[1] == "version" {
        fmt.Printf("stockyardd %s\n", version.Version)
        os.Exit(0)
    }

    if err := run(); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}

func run() error {
    cfg, err := config.Load()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    if cfg.InstanceID == "" {
        return fmt.Errorf("stockyard not initialized. Run: stockyard init --instance <name>")
    }

    // Create secrets provider
    secretsProvider := secrets.NewOnePasswordProvider(cfg.Secrets.Vault, cfg.Secrets.Prefix)

    // Create daemon
    d, err := daemon.New(cfg, secretsProvider)
    if err != nil {
        return fmt.Errorf("failed to create daemon: %w", err)
    }

    // Setup signal handling
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        <-sigCh
        fmt.Println("\nShutting down...")
        cancel()
    }()

    // Start daemon
    fmt.Printf("Starting stockyardd for instance %q\n", cfg.InstanceID)
    return d.Start(ctx)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyardd ./cmd/stockyardd
./bin/stockyardd version
```

Expected: Prints version

**Step 3: Commit**

```bash
git add cmd/stockyardd/
git commit -m "feat: update daemon entry point

- Load config and verify initialization
- Create secrets provider
- Signal handling for graceful shutdown"
```

---

## Phase 5: gRPC API

### Task 5.1: Define Protobuf API

**Files:**
- Create: `api/stockyard.proto`
- Create: `Makefile`

**Step 1: Create proto file**

```protobuf
// api/stockyard.proto
syntax = "proto3";

package stockyard.v1;

option go_package = "github.com/obra/stockyard/pkg/api/v1";

service Stockyard {
    // Task lifecycle
    rpc CreateTask(CreateTaskRequest) returns (CreateTaskResponse);
    rpc GetTask(GetTaskRequest) returns (GetTaskResponse);
    rpc ListTasks(ListTasksRequest) returns (ListTasksResponse);
    rpc StopTask(StopTaskRequest) returns (StopTaskResponse);
    rpc DestroyTask(DestroyTaskRequest) returns (DestroyTaskResponse);

    // Snapshots
    rpc CreateSnapshot(CreateSnapshotRequest) returns (CreateSnapshotResponse);
    rpc ListSnapshots(ListSnapshotsRequest) returns (ListSnapshotsResponse);
    rpc RestoreSnapshot(RestoreSnapshotRequest) returns (RestoreSnapshotResponse);
}

// Task messages
message CreateTaskRequest {
    string repo = 1;
    string ref = 2;
    string name = 3;  // optional human-readable name
    repeated string command = 4;  // command to run
    map<string, string> env = 5;  // additional environment variables
    int32 cpus = 6;
    string memory = 7;  // e.g., "4G"
    bool no_tailscale = 8;
}

message CreateTaskResponse {
    string task_id = 1;
    string tailscale_hostname = 2;  // if tailscale enabled
}

message GetTaskRequest {
    string task_id = 1;
}

message GetTaskResponse {
    Task task = 1;
}

message ListTasksRequest {
    string status = 1;  // optional filter: "running", "stopped", "failed"
}

message ListTasksResponse {
    repeated Task tasks = 1;
}

message StopTaskRequest {
    string task_id = 1;
}

message StopTaskResponse {}

message DestroyTaskRequest {
    string task_id = 1;
}

message DestroyTaskResponse {}

// Snapshot messages
message CreateSnapshotRequest {
    string task_id = 1;
    string label = 2;  // optional label
}

message CreateSnapshotResponse {
    string snapshot_name = 1;
}

message ListSnapshotsRequest {
    string task_id = 1;
}

message ListSnapshotsResponse {
    repeated Snapshot snapshots = 1;
}

message RestoreSnapshotRequest {
    string task_id = 1;
    string snapshot_name = 2;
}

message RestoreSnapshotResponse {}

// Common types
message Task {
    string id = 1;
    string name = 2;
    string repo = 3;
    string ref = 4;
    string status = 5;
    string tailscale_hostname = 6;
    string created_at = 7;  // RFC3339
    string stopped_at = 8;  // RFC3339, empty if running
}

message Snapshot {
    string name = 1;
    string label = 2;
    string created_at = 3;  // RFC3339
}
```

**Step 2: Create Makefile**

```makefile
# Makefile

.PHONY: all build proto clean test

all: proto build

build:
	go build -o bin/stockyard ./cmd/stockyard
	go build -o bin/stockyardd ./cmd/stockyardd

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/stockyard.proto

clean:
	rm -rf bin/

test:
	go test ./...
```

**Step 3: Install protoc tools**

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

**Step 4: Generate Go code**

```bash
mkdir -p pkg/api/v1
make proto
```

Note: This may fail if protoc isn't installed. Install with:
```bash
sudo apt-get install -y protobuf-compiler
```

**Step 5: Add gRPC dependencies**

```bash
go get google.golang.org/grpc
go get google.golang.org/protobuf
```

**Step 6: Commit**

```bash
git add api/ Makefile pkg/api/ go.mod go.sum
git commit -m "feat: add gRPC API definition

- Protobuf service definition
- Task lifecycle RPCs
- Snapshot management RPCs
- Makefile with proto target"
```

---

## Phase 6: CLI Commands (Continuing)

### Task 6.1: Add List Command

**Files:**
- Create: `cmd/stockyard/list.go`
- Create: `pkg/client/client.go`

**Step 1: Create client package**

```go
// pkg/client/client.go
package client

import (
    "context"
    "fmt"
    "net"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pb "github.com/obra/stockyard/pkg/api/v1"
)

// Client is a stockyard daemon client
type Client struct {
    conn   *grpc.ClientConn
    client pb.StockyardClient
}

// New creates a new client connected to the daemon
func New(socketPath string) (*Client, error) {
    conn, err := grpc.Dial(
        socketPath,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
            return net.Dial("unix", addr)
        }),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to daemon: %w", err)
    }

    return &Client{
        conn:   conn,
        client: pb.NewStockyardClient(conn),
    }, nil
}

// Close closes the client connection
func (c *Client) Close() error {
    return c.conn.Close()
}

// ListTasks lists all tasks
func (c *Client) ListTasks(ctx context.Context, status string) ([]*pb.Task, error) {
    resp, err := c.client.ListTasks(ctx, &pb.ListTasksRequest{Status: status})
    if err != nil {
        return nil, err
    }
    return resp.Tasks, nil
}

// CreateTask creates a new task
func (c *Client) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.CreateTaskResponse, error) {
    return c.client.CreateTask(ctx, req)
}

// StopTask stops a task
func (c *Client) StopTask(ctx context.Context, taskID string) error {
    _, err := c.client.StopTask(ctx, &pb.StopTaskRequest{TaskId: taskID})
    return err
}

// DestroyTask destroys a task
func (c *Client) DestroyTask(ctx context.Context, taskID string) error {
    _, err := c.client.DestroyTask(ctx, &pb.DestroyTaskRequest{TaskId: taskID})
    return err
}

// CreateSnapshot creates a snapshot
func (c *Client) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
    resp, err := c.client.CreateSnapshot(ctx, &pb.CreateSnapshotRequest{
        TaskId: taskID,
        Label:  label,
    })
    if err != nil {
        return "", err
    }
    return resp.SnapshotName, nil
}

// ListSnapshots lists snapshots for a task
func (c *Client) ListSnapshots(ctx context.Context, taskID string) ([]*pb.Snapshot, error) {
    resp, err := c.client.ListSnapshots(ctx, &pb.ListSnapshotsRequest{TaskId: taskID})
    if err != nil {
        return nil, err
    }
    return resp.Snapshots, nil
}
```

**Step 2: Create list command**

```go
// cmd/stockyard/list.go
package main

import (
    "context"
    "fmt"
    "os"
    "text/tabwriter"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var listStatus string

var listCmd = &cobra.Command{
    Use:   "list",
    Short: "List tasks",
    Aliases: []string{"ls"},
    RunE: func(cmd *cobra.Command, args []string) error {
        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        tasks, err := c.ListTasks(context.Background(), listStatus)
        if err != nil {
            return err
        }

        if len(tasks) == 0 {
            fmt.Println("No tasks found")
            return nil
        }

        w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
        fmt.Fprintln(w, "ID\tNAME\tREPO\tSTATUS\tCREATED")
        for _, t := range tasks {
            name := t.Name
            if name == "" {
                name = "-"
            }
            fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
                t.Id, name, t.Repo, t.Status, t.CreatedAt)
        }
        w.Flush()

        return nil
    },
}

func init() {
    listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (running, stopped, failed)")
    rootCmd.AddCommand(listCmd)
}
```

**Step 3: Verify compilation**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard list --help
```

Expected: Shows help for list command

**Step 4: Commit**

```bash
git add pkg/client/ cmd/stockyard/list.go
git commit -m "feat: add list command and client package

- gRPC client wrapper
- List command with status filter
- Tabular output"
```

---

## Remaining Phases (Summary)

The following phases are outlined but not fully detailed. Each would follow the same TDD pattern:

### Phase 7: Flintlock Integration
- Task 7.1: Create Flintlock client wrapper
- Task 7.2: Implement VM create/start/stop/destroy
- Task 7.3: Configure virtio-fs mounts
- Task 7.4: Configure networking (TAP devices)

### Phase 8: VM Image
- Task 8.1: Create base Dockerfile from packnplay
- Task 8.2: Add cloud-init configuration
- Task 8.3: Add stockyard-snapshot client
- Task 8.4: Add Claude Code hooks
- Task 8.5: Build and test VM image

### Phase 9: vsock Snapshot Service
- Task 9.1: Implement vsock listener in daemon
- Task 9.2: Implement stockyard-snapshot client (Go binary for VM)
- Task 9.3: Wire up snapshot creation on vsock request
- Task 9.4: Test end-to-end snapshot flow

### Phase 10: Tailscale Integration
- Task 10.1: Create Tailscale package
- Task 10.2: Generate auth keys from 1Password
- Task 10.3: Configure cloud-init for Tailscale join
- Task 10.4: Test SSH access via Tailscale

### Phase 11: Remaining CLI Commands
- Task 11.1: `run` command (creates task, starts VM)
- Task 11.2: `stop` command
- Task 11.3: `destroy` command
- Task 11.4: `attach` command (SSH wrapper)
- Task 11.5: `snapshot` command
- Task 11.6: `snapshots` command
- Task 11.7: `restore` command
- Task 11.8: `logs` command
- Task 11.9: `cp` command

### Phase 12: Integration Testing
- Task 12.1: End-to-end test: create task, run agent, snapshot
- Task 12.2: Test persistence across VM restart
- Task 12.3: Test Tailscale connectivity
- Task 12.4: Test snapshot restore

---

## Implementation Notes

### Testing Strategy
- Unit tests for pure functions (config parsing, ZFS path building, etc.)
- Integration tests require:
  - ZFS pool (can use file-backed for CI)
  - Flintlock daemon running
  - 1Password CLI authenticated
- Mock interfaces for unit testing (secrets.Provider, etc.)

### Build Requirements
- Go 1.21+
- protoc (protobuf compiler)
- ZFS utilities (`zfsutils-linux`)
- Flintlock daemon
- 1Password CLI (`op`)

### Development Environment
```bash
# Install dependencies
sudo apt-get install -y zfsutils-linux protobuf-compiler

# Create test ZFS pool (file-backed)
sudo dd if=/dev/zero of=/var/lib/stockyard/zpool.img bs=1G count=10
sudo zpool create tank /var/lib/stockyard/zpool.img
sudo zfs create tank/stockyard
sudo zfs create tank/stockyard/workspaces

# Run daemon
./bin/stockyardd

# In another terminal, use CLI
./bin/stockyard list
```

---

**End of Phase 6. Phases 7-12 would be detailed similarly when ready to implement.**

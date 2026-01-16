# Stockyard Implementation - Part 1: Foundation (Phases 1-3)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Set up project structure, configuration, secrets integration, and ZFS management.

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
mkdir -p pkg/{api,config,daemon,flintlock,secrets,snapshots,tailscale,vm,vsock,zfs,client}
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
echo "bin/" >> .gitignore
git add go.mod cmd/ pkg/version/ .gitignore
git commit -m "feat: initialize Go project structure"
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
git commit -m "feat: add Cobra CLI framework"
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

    configPath := filepath.Join(tmpDir, "config.json")
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        t.Fatal("config file not created")
    }

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

type Config struct {
    InstanceID string        `json:"instance_id"`
    Secrets    SecretsConfig `json:"secrets"`
    Daemon     DaemonConfig  `json:"daemon"`
    ZFS        ZFSConfig     `json:"zfs"`
}

type SecretsConfig struct {
    Provider string `json:"provider"`
    Vault    string `json:"vault"`
    Prefix   string `json:"prefix"`
}

type DaemonConfig struct {
    SocketPath string `json:"socket_path"`
}

type ZFSConfig struct {
    Pool     string `json:"pool"`
    BasePath string `json:"base_path"`
}

func DefaultConfig() *Config {
    return &Config{
        Secrets: SecretsConfig{
            Provider: "1password",
            Vault:    "Stockyard",
        },
        Daemon: DaemonConfig{
            SocketPath: "/var/run/stockyard/stockyard.sock",
        },
        ZFS: ZFSConfig{
            Pool:     "tank",
            BasePath: "stockyard/workspaces",
        },
    }
}

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

func Load() (*Config, error) {
    configDir, err := ConfigDir()
    if err != nil {
        return nil, err
    }
    return LoadFromDir(configDir)
}

func (c *Config) Save() error {
    configDir, err := ConfigDir()
    if err != nil {
        return err
    }
    return c.SaveToDir(configDir)
}

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
git commit -m "feat: add configuration package"
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
git commit -m "feat: add init command"
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

type Provider interface {
    GetSecret(ctx context.Context, name string) (string, error)
    Name() string
}

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
git commit -m "feat: add secrets provider interface"
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

type OnePasswordProvider struct {
    Vault  string
    Prefix string
}

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

Expected: PASS

**Step 5: Commit**

```bash
git add pkg/secrets/onepassword.go pkg/secrets/onepassword_test.go
git commit -m "feat: add 1Password secrets provider"
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

type Manager struct {
    PoolName string
    BasePath string
}

func NewManager(pool, basePath string) *Manager {
    return &Manager{
        PoolName: pool,
        BasePath: basePath,
    }
}

func ParseDatasetName(name string) (pool, path string) {
    parts := strings.SplitN(name, "/", 2)
    if len(parts) == 1 {
        return parts[0], ""
    }
    return parts[0], parts[1]
}

func BuildSnapshotName(taskID, label string) string {
    timestamp := time.Now().Format("2006-01-02T15-04-05")
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

func (m *Manager) DatasetPath(taskID string) string {
    return fmt.Sprintf("%s/%s/%s", m.PoolName, m.BasePath, taskID)
}

func (m *Manager) CreateDataset(ctx context.Context, taskID string) error {
    dataset := m.DatasetPath(taskID)
    return m.runZFS(ctx, "create", "-p", dataset)
}

func (m *Manager) DestroyDataset(ctx context.Context, taskID string) error {
    dataset := m.DatasetPath(taskID)
    return m.runZFS(ctx, "destroy", "-r", dataset)
}

func (m *Manager) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
    dataset := m.DatasetPath(taskID)
    snapName := BuildSnapshotName(taskID, label)
    fullName := fmt.Sprintf("%s@%s", dataset, snapName)

    if err := m.runZFS(ctx, "snapshot", fullName); err != nil {
        return "", err
    }
    return snapName, nil
}

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
            if idx := strings.LastIndex(line, "@"); idx != -1 {
                snapshots = append(snapshots, line[idx+1:])
            }
        }
    }
    return snapshots, nil
}

func (m *Manager) DestroySnapshot(ctx context.Context, taskID, snapName string) error {
    dataset := m.DatasetPath(taskID)
    fullName := fmt.Sprintf("%s@%s", dataset, snapName)
    return m.runZFS(ctx, "destroy", fullName)
}

func (m *Manager) RollbackSnapshot(ctx context.Context, taskID, snapName string) error {
    dataset := m.DatasetPath(taskID)
    fullName := fmt.Sprintf("%s@%s", dataset, snapName)
    return m.runZFS(ctx, "rollback", "-r", fullName)
}

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

func (m *Manager) Sync(ctx context.Context, taskID string) error {
    // Sync to ensure all writes are flushed before snapshot
    mountpoint, err := m.GetMountpoint(ctx, taskID)
    if err != nil {
        return err
    }
    cmd := exec.CommandContext(ctx, "sync", "-f", mountpoint)
    return cmd.Run()
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
git commit -m "feat: add ZFS management package"
```

---

**End of Part 1. Continue with Part 2: Daemon & gRPC.**

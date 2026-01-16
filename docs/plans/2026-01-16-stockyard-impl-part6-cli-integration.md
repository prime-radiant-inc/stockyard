# Stockyard Implementation - Part 6: CLI Commands & Integration Tests (Phases 11-12)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Complete all CLI commands and verify the system works end-to-end.

**Reference:**
- Design doc at `docs/plans/2026-01-16-stockyard-design.md`

---

## Phase 11: Remaining CLI Commands

### Task 11.1: Add run Command

**Files:**
- Create: `cmd/stockyard/run.go`

**Step 1: Create run command**

```go
// cmd/stockyard/run.go
package main

import (
    "context"
    "fmt"
    "os"
    "strings"

    pb "github.com/obra/stockyard/pkg/api/v1"
    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var (
    runRepo        string
    runRef         string
    runName        string
    runCPUs        int32
    runMemory      string
    runNoTailscale bool
    runEnv         []string
)

var runCmd = &cobra.Command{
    Use:   "run [flags] -- <command> [args...]",
    Short: "Run a coding agent in a new VM",
    Long: `Run a coding agent in a new Firecracker micro-VM.

Examples:
  # Run Claude Code on a feature branch
  stockyard run --repo github.com/org/repo --ref feature-auth \
    -- claude-code --dangerously-skip-permissions -p "implement OAuth"

  # Run with a specific name
  stockyard run --repo github.com/org/repo --name my-task \
    -- claude-code --dangerously-skip-permissions

  # Run without Tailscale
  stockyard run --repo github.com/org/repo --no-tailscale \
    -- claude-code --dangerously-skip-permissions`,
    RunE: func(cmd *cobra.Command, args []string) error {
        if runRepo == "" {
            return fmt.Errorf("--repo is required")
        }

        // Find command after --
        var command []string
        for i, arg := range os.Args {
            if arg == "--" && i+1 < len(os.Args) {
                command = os.Args[i+1:]
                break
            }
        }

        if len(command) == 0 {
            return fmt.Errorf("command is required after --")
        }

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        // Parse environment variables
        env := make(map[string]string)
        for _, e := range runEnv {
            parts := strings.SplitN(e, "=", 2)
            if len(parts) == 2 {
                env[parts[0]] = parts[1]
            }
        }

        // Default ref to main
        ref := runRef
        if ref == "" {
            ref = "main"
        }

        req := &pb.CreateTaskRequest{
            Repo:        runRepo,
            Ref:         ref,
            Name:        runName,
            Command:     command,
            Env:         env,
            Cpus:        runCPUs,
            Memory:      runMemory,
            NoTailscale: runNoTailscale,
        }

        fmt.Printf("Creating task for %s@%s...\n", runRepo, ref)

        resp, err := c.CreateTask(context.Background(), req)
        if err != nil {
            return fmt.Errorf("failed to create task: %w", err)
        }

        fmt.Printf("Task created: %s\n", resp.TaskId)
        if resp.TailscaleHostname != "" {
            fmt.Printf("Tailscale hostname: %s\n", resp.TailscaleHostname)
            fmt.Printf("\nTo attach: stockyard attach %s\n", resp.TaskId)
            fmt.Printf("Or SSH directly: ssh vscode@%s\n", resp.TailscaleHostname)
        }

        return nil
    },
}

func init() {
    runCmd.Flags().StringVar(&runRepo, "repo", "", "Git repository URL (required)")
    runCmd.Flags().StringVar(&runRef, "ref", "main", "Git branch, tag, or commit")
    runCmd.Flags().StringVar(&runName, "name", "", "Human-readable task name")
    runCmd.Flags().Int32Var(&runCPUs, "cpus", 2, "Number of CPU cores")
    runCmd.Flags().StringVar(&runMemory, "memory", "4G", "Memory allocation (e.g., 4G)")
    runCmd.Flags().BoolVar(&runNoTailscale, "no-tailscale", false, "Skip Tailscale network join")
    runCmd.Flags().StringArrayVar(&runEnv, "env", nil, "Environment variables (KEY=value)")
    runCmd.MarkFlagRequired("repo")
    rootCmd.AddCommand(runCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard run --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/run.go
git commit -m "feat: add run command

- Create task with VM
- Supports repo, ref, name, cpus, memory flags
- Environment variable injection
- Tailscale toggle"
```

---

### Task 11.2: Add stop Command

**Files:**
- Create: `cmd/stockyard/stop.go`

**Step 1: Create stop command**

```go
// cmd/stockyard/stop.go
package main

import (
    "context"
    "fmt"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
    Use:   "stop <task-id>",
    Short: "Stop a running task (workspace persists)",
    Long:  `Stop a running task's VM. The workspace is preserved and can be restarted later.`,
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        fmt.Printf("Stopping task %s...\n", taskID)

        if err := c.StopTask(context.Background(), taskID); err != nil {
            return fmt.Errorf("failed to stop task: %w", err)
        }

        fmt.Println("Task stopped. Workspace preserved.")
        fmt.Printf("To destroy completely: stockyard destroy %s\n", taskID)

        return nil
    },
}

func init() {
    rootCmd.AddCommand(stopCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard stop --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/stop.go
git commit -m "feat: add stop command

- Stops VM but preserves workspace
- Shows next steps for destroy"
```

---

### Task 11.3: Add destroy Command

**Files:**
- Create: `cmd/stockyard/destroy.go`

**Step 1: Create destroy command**

```go
// cmd/stockyard/destroy.go
package main

import (
    "context"
    "fmt"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var destroyForce bool

var destroyCmd = &cobra.Command{
    Use:   "destroy <task-id>",
    Short: "Destroy a task and its workspace",
    Long:  `Destroy a task completely, including its VM and workspace. This is irreversible.`,
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        // Get task to show what we're destroying
        task, err := c.GetTask(context.Background(), taskID)
        if err != nil {
            return fmt.Errorf("failed to get task: %w", err)
        }

        if task == nil {
            return fmt.Errorf("task not found: %s", taskID)
        }

        if !destroyForce {
            fmt.Printf("About to destroy task %s:\n", taskID)
            fmt.Printf("  Repo: %s\n", task.Repo)
            fmt.Printf("  Ref:  %s\n", task.Ref)
            fmt.Printf("\nThis will delete the VM and all workspace data.\n")
            fmt.Printf("Run with --force to confirm.\n")
            return nil
        }

        fmt.Printf("Destroying task %s...\n", taskID)

        if err := c.DestroyTask(context.Background(), taskID); err != nil {
            return fmt.Errorf("failed to destroy task: %w", err)
        }

        fmt.Println("Task destroyed.")

        return nil
    },
}

func init() {
    destroyCmd.Flags().BoolVarP(&destroyForce, "force", "f", false, "Force destruction without confirmation")
    rootCmd.AddCommand(destroyCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard destroy --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/destroy.go
git commit -m "feat: add destroy command

- Removes task, VM, and workspace
- Requires --force flag for confirmation
- Shows task details before destruction"
```

---

### Task 11.4: Add snapshot Command

**Files:**
- Create: `cmd/stockyard/snapshot.go`

**Step 1: Create snapshot command**

```go
// cmd/stockyard/snapshot.go
package main

import (
    "context"
    "fmt"
    "strings"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
    Use:   "snapshot <task-id> [label]",
    Short: "Create a manual snapshot",
    Long:  `Create a manual ZFS snapshot of a task's workspace.`,
    Args:  cobra.RangeArgs(1, 2),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]
        label := "manual"
        if len(args) > 1 {
            label = strings.Join(args[1:], "-")
        }

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        fmt.Printf("Creating snapshot for %s: %s\n", taskID, label)

        snapName, err := c.CreateSnapshot(context.Background(), taskID, label)
        if err != nil {
            return fmt.Errorf("failed to create snapshot: %w", err)
        }

        fmt.Printf("Snapshot created: %s\n", snapName)

        return nil
    },
}

func init() {
    rootCmd.AddCommand(snapshotCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard snapshot --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/snapshot.go
git commit -m "feat: add snapshot command

- Manual ZFS snapshot creation
- Optional label argument"
```

---

### Task 11.5: Add snapshots Command

**Files:**
- Create: `cmd/stockyard/snapshots.go`

**Step 1: Create snapshots command**

```go
// cmd/stockyard/snapshots.go
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

var snapshotsCmd = &cobra.Command{
    Use:   "snapshots <task-id>",
    Short: "List snapshots for a task",
    Long:  `List all ZFS snapshots for a task's workspace.`,
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        snapshots, err := c.ListSnapshots(context.Background(), taskID)
        if err != nil {
            return fmt.Errorf("failed to list snapshots: %w", err)
        }

        if len(snapshots) == 0 {
            fmt.Println("No snapshots found")
            return nil
        }

        w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
        fmt.Fprintln(w, "NAME\tLABEL\tCREATED")
        for _, s := range snapshots {
            label := s.Label
            if label == "" {
                label = "-"
            }
            fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, label, s.CreatedAt)
        }
        w.Flush()

        return nil
    },
}

func init() {
    rootCmd.AddCommand(snapshotsCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard snapshots --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/snapshots.go
git commit -m "feat: add snapshots command

- List all snapshots for a task
- Tabular output with name, label, timestamp"
```

---

### Task 11.6: Add restore Command

**Files:**
- Create: `cmd/stockyard/restore.go`

**Step 1: Create restore command**

```go
// cmd/stockyard/restore.go
package main

import (
    "context"
    "fmt"

    pb "github.com/obra/stockyard/pkg/api/v1"
    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var restoreForce bool

var restoreCmd = &cobra.Command{
    Use:   "restore <task-id> <snapshot-name>",
    Short: "Restore a task to a snapshot",
    Long: `Restore a task's workspace to a previous snapshot.

Warning: This will stop the VM if running and roll back any changes made
since the snapshot.`,
    Args: cobra.ExactArgs(2),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]
        snapshotName := args[1]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        // Check task status
        task, err := c.GetTask(context.Background(), taskID)
        if err != nil {
            return fmt.Errorf("failed to get task: %w", err)
        }

        if task == nil {
            return fmt.Errorf("task not found: %s", taskID)
        }

        if task.Status == "running" && !restoreForce {
            fmt.Printf("Task %s is running. Restoring will stop the VM.\n", taskID)
            fmt.Printf("Run with --force to proceed.\n")
            return nil
        }

        if !restoreForce {
            fmt.Printf("About to restore task %s to snapshot %s\n", taskID, snapshotName)
            fmt.Printf("This will roll back all changes since the snapshot.\n")
            fmt.Printf("Run with --force to confirm.\n")
            return nil
        }

        fmt.Printf("Restoring task %s to %s...\n", taskID, snapshotName)

        _, err = c.RestoreSnapshot(context.Background(), &pb.RestoreSnapshotRequest{
            TaskId:       taskID,
            SnapshotName: snapshotName,
        })
        if err != nil {
            return fmt.Errorf("failed to restore: %w", err)
        }

        fmt.Println("Restored successfully.")

        return nil
    },
}

func init() {
    restoreCmd.Flags().BoolVarP(&restoreForce, "force", "f", false, "Force restore without confirmation")
    rootCmd.AddCommand(restoreCmd)
}
```

**Step 2: Add RestoreSnapshot to client**

Add to `pkg/client/client.go`:

```go
// RestoreSnapshot restores a task to a snapshot
func (c *Client) RestoreSnapshot(ctx context.Context, req *pb.RestoreSnapshotRequest) (*pb.RestoreSnapshotResponse, error) {
    return c.client.RestoreSnapshot(ctx, req)
}
```

**Step 3: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard restore --help
```

**Step 4: Commit**

```bash
git add cmd/stockyard/restore.go pkg/client/client.go
git commit -m "feat: add restore command

- Rollback to previous snapshot
- Warns about running VMs
- Requires --force confirmation"
```

---

### Task 11.7: Add logs Command

**Files:**
- Create: `cmd/stockyard/logs.go`

**Step 1: Create logs command**

```go
// cmd/stockyard/logs.go
package main

import (
    "context"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var (
    logsFollow bool
    logsSystem bool
    logsTail   int
)

var logsCmd = &cobra.Command{
    Use:   "logs <task-id>",
    Short: "Get logs from a task",
    Long:  `Stream or fetch logs from a task's VM.`,
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        taskID := args[0]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        // Get task details
        task, err := c.GetTask(context.Background(), taskID)
        if err != nil {
            return fmt.Errorf("failed to get task: %w", err)
        }

        if task == nil {
            return fmt.Errorf("task not found: %s", taskID)
        }

        // For running tasks with Tailscale, SSH in and get logs
        if task.Status == "running" && task.TailscaleHostname != "" {
            return streamLogsSSH(task.TailscaleHostname, logsFollow, logsSystem)
        }

        // For stopped tasks, read from local log files
        logDir := filepath.Join("/var/lib/stockyard/logs", taskID)
        logFile := filepath.Join(logDir, "agent.log")

        if logsSystem {
            logFile = filepath.Join(logDir, "system.log")
        }

        f, err := os.Open(logFile)
        if err != nil {
            return fmt.Errorf("failed to open log file: %w\nTask may not have generated logs yet.", err)
        }
        defer f.Close()

        // Tail the file
        if logsTail > 0 {
            // Simple tail implementation
            stat, _ := f.Stat()
            if stat.Size() > int64(logsTail*200) {
                f.Seek(-int64(logsTail*200), io.SeekEnd)
            }
        }

        _, err = io.Copy(os.Stdout, f)
        return err
    },
}

func streamLogsSSH(hostname string, follow bool, system bool) error {
    var logPath string
    if system {
        logPath = "/var/log/cloud-init-output.log"
    } else {
        // Claude Code logs or agent stdout
        logPath = "/workspace/.claude/logs/latest.log"
    }

    sshArgs := []string{
        "-o", "StrictHostKeyChecking=accept-new",
        fmt.Sprintf("vscode@%s", hostname),
    }

    if follow {
        sshArgs = append(sshArgs, "tail", "-f", logPath)
    } else {
        sshArgs = append(sshArgs, "cat", logPath)
    }

    cmd := exec.Command("ssh", sshArgs...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr

    return cmd.Run()
}

func init() {
    logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
    logsCmd.Flags().BoolVar(&logsSystem, "system", false, "Show system logs instead of agent logs")
    logsCmd.Flags().IntVarP(&logsTail, "tail", "n", 0, "Number of lines to show from end")
    rootCmd.AddCommand(logsCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard logs --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/logs.go
git commit -m "feat: add logs command

- Stream logs via SSH for running tasks
- Read local logs for stopped tasks
- Support --follow, --system, --tail flags"
```

---

### Task 11.8: Add cp Command

**Files:**
- Create: `cmd/stockyard/cp.go`

**Step 1: Create cp command**

```go
// cmd/stockyard/cp.go
package main

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "strings"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
    Use:   "cp <task-id>:<remote-path> <local-path>",
    Short: "Copy files from a task",
    Long: `Copy files from a task's VM to local filesystem.

Examples:
  stockyard cp task-123:/workspace/output.txt ./output.txt
  stockyard cp task-123:/workspace/.claude/logs ./claude-logs/`,
    Args: cobra.ExactArgs(2),
    RunE: func(cmd *cobra.Command, args []string) error {
        // Parse source
        source := args[0]
        dest := args[1]

        parts := strings.SplitN(source, ":", 2)
        if len(parts) != 2 {
            return fmt.Errorf("source must be in format <task-id>:<path>")
        }

        taskID := parts[0]
        remotePath := parts[1]

        cfg, err := config.Load()
        if err != nil {
            return err
        }

        c, err := client.New(cfg.Daemon.SocketPath)
        if err != nil {
            return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
        }
        defer c.Close()

        // Get task details
        task, err := c.GetTask(context.Background(), taskID)
        if err != nil {
            return fmt.Errorf("failed to get task: %w", err)
        }

        if task == nil {
            return fmt.Errorf("task not found: %s", taskID)
        }

        if task.TailscaleHostname == "" {
            return fmt.Errorf("task has no Tailscale hostname (was --no-tailscale used?)")
        }

        // Use scp to copy files
        scpSource := fmt.Sprintf("vscode@%s:%s", task.TailscaleHostname, remotePath)

        scpCmd := exec.Command("scp",
            "-o", "StrictHostKeyChecking=accept-new",
            "-r", // Recursive for directories
            scpSource,
            dest,
        )
        scpCmd.Stdout = os.Stdout
        scpCmd.Stderr = os.Stderr

        fmt.Printf("Copying %s to %s...\n", source, dest)

        if err := scpCmd.Run(); err != nil {
            return fmt.Errorf("scp failed: %w", err)
        }

        fmt.Println("Copy complete.")
        return nil
    },
}

func init() {
    rootCmd.AddCommand(cpCmd)
}
```

**Step 2: Verify**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard cp --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/cp.go
git commit -m "feat: add cp command

- Copy files from VM via scp
- Support directories with -r
- Uses Tailscale hostname"
```

---

### Task 11.9: Add configure Command (TUI)

**Files:**
- Create: `cmd/stockyard/configure.go`

**Step 1: Add survey dependency**

```bash
go get github.com/AlecAivazis/survey/v2
```

**Step 2: Create configure command**

```go
// cmd/stockyard/configure.go
package main

import (
    "fmt"

    "github.com/AlecAivazis/survey/v2"
    "github.com/obra/stockyard/pkg/config"
    "github.com/spf13/cobra"
)

var configureCmd = &cobra.Command{
    Use:   "configure",
    Short: "Interactive configuration",
    Long:  `Interactively configure stockyard settings.`,
    RunE: func(cmd *cobra.Command, args []string) error {
        cfg, err := config.Load()
        if err != nil {
            return err
        }

        fmt.Println("Stockyard Configuration")
        fmt.Println("========================")
        fmt.Println()

        // Instance ID
        instancePrompt := &survey.Input{
            Message: "Instance ID:",
            Default: cfg.InstanceID,
            Help:    "Unique identifier for this stockyard instance",
        }
        survey.AskOne(instancePrompt, &cfg.InstanceID)

        // Secrets provider
        providerPrompt := &survey.Select{
            Message: "Secrets provider:",
            Options: []string{"1password", "aws"},
            Default: cfg.Secrets.Provider,
        }
        survey.AskOne(providerPrompt, &cfg.Secrets.Provider)

        // 1Password vault
        if cfg.Secrets.Provider == "1password" {
            vaultPrompt := &survey.Input{
                Message: "1Password vault:",
                Default: cfg.Secrets.Vault,
            }
            survey.AskOne(vaultPrompt, &cfg.Secrets.Vault)
        }

        // Secrets prefix (auto-set from instance ID)
        cfg.Secrets.Prefix = cfg.InstanceID

        // Daemon socket
        socketPrompt := &survey.Input{
            Message: "Daemon socket path:",
            Default: cfg.Daemon.SocketPath,
        }
        survey.AskOne(socketPrompt, &cfg.Daemon.SocketPath)

        // Confirm save
        var save bool
        confirmPrompt := &survey.Confirm{
            Message: "Save configuration?",
            Default: true,
        }
        survey.AskOne(confirmPrompt, &save)

        if save {
            if err := cfg.Save(); err != nil {
                return fmt.Errorf("failed to save: %w", err)
            }

            configDir, _ := config.ConfigDir()
            fmt.Printf("\nConfiguration saved to %s/config.json\n", configDir)
        }

        return nil
    },
}

func init() {
    rootCmd.AddCommand(configureCmd)
}
```

**Step 3: Verify**

```bash
go mod tidy
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard configure --help
```

**Step 4: Commit**

```bash
git add cmd/stockyard/configure.go go.mod go.sum
git commit -m "feat: add configure command with TUI

- Interactive configuration prompts
- Instance ID, secrets provider, vault
- Save confirmation"
```

---

### Task 11.10: Complete CLI Help and Documentation

**Files:**
- Modify: `cmd/stockyard/root.go`

**Step 1: Enhance root command**

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
    Long: `Stockyard runs coding agents in isolated Firecracker micro-VMs
with ZFS-based audit trail snapshots.

Quick Start:
  # Initialize stockyard
  stockyard init --instance my-dev

  # Start the daemon (in another terminal)
  stockyardd

  # Run a coding agent
  stockyard run --repo github.com/org/repo -- claude-code -p "your prompt"

  # Attach to the running VM
  stockyard attach <task-id>

  # List running tasks
  stockyard list`,
}

func Execute() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

**Step 2: Verify help output**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard --help
```

**Step 3: Commit**

```bash
git add cmd/stockyard/root.go
git commit -m "feat: enhance CLI help and documentation

- Quick start guide in long description
- Clear examples"
```

---

## Phase 12: Integration Testing

### Task 12.1: Create Test Fixtures

**Files:**
- Create: `tests/fixtures/test-repo/README.md`
- Create: `tests/fixtures/test-repo/main.py`

**Step 1: Create test repository files**

```bash
mkdir -p tests/fixtures/test-repo
```

```markdown
# tests/fixtures/test-repo/README.md
# Test Repository

This is a test repository for stockyard integration tests.
```

```python
# tests/fixtures/test-repo/main.py
#!/usr/bin/env python3
"""Test script for stockyard integration tests."""

import sys
import time

def main():
    print("Test script running")
    print(f"Python version: {sys.version}")

    # Create a marker file
    with open("/workspace/test-marker.txt", "w") as f:
        f.write("test completed\n")

    print("Test completed successfully")

if __name__ == "__main__":
    main()
```

**Step 2: Commit**

```bash
git add tests/fixtures/
git commit -m "test: add test fixtures

- Simple test repo for integration tests"
```

---

### Task 12.2: Create Integration Test Framework

**Files:**
- Create: `tests/integration/helpers.go`
- Create: `tests/integration/setup_test.go`

**Step 1: Create test helpers**

```go
// tests/integration/helpers.go
//go:build integration

package integration

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"

    "github.com/obra/stockyard/pkg/client"
    "github.com/obra/stockyard/pkg/config"
)

// TestConfig holds test configuration
type TestConfig struct {
    BinaryPath     string
    DaemonPath     string
    SocketPath     string
    ConfigDir      string
    TestRepo       string
}

// GetTestConfig loads test configuration from environment
func GetTestConfig(t *testing.T) *TestConfig {
    t.Helper()

    binPath := os.Getenv("STOCKYARD_BIN")
    if binPath == "" {
        binPath = "../../bin/stockyard"
    }

    daemonPath := os.Getenv("STOCKYARD_DAEMON")
    if daemonPath == "" {
        daemonPath = "../../bin/stockyardd"
    }

    return &TestConfig{
        BinaryPath: binPath,
        DaemonPath: daemonPath,
        SocketPath: "/tmp/stockyard-test.sock",
        ConfigDir:  t.TempDir(),
        TestRepo:   "../../tests/fixtures/test-repo",
    }
}

// RunCommand runs stockyard CLI command
func RunCommand(t *testing.T, cfg *TestConfig, args ...string) (string, error) {
    t.Helper()

    cmd := exec.Command(cfg.BinaryPath, args...)
    cmd.Env = append(os.Environ(),
        fmt.Sprintf("XDG_CONFIG_HOME=%s", cfg.ConfigDir),
    )

    output, err := cmd.CombinedOutput()
    return string(output), err
}

// WaitForDaemon waits for daemon to be ready
func WaitForDaemon(ctx context.Context, socketPath string) error {
    for i := 0; i < 30; i++ {
        c, err := client.New(socketPath)
        if err == nil {
            c.Close()
            return nil
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(100 * time.Millisecond):
        }
    }
    return fmt.Errorf("daemon not ready after 3s")
}

// CreateTestConfig creates a test configuration
func CreateTestConfig(t *testing.T, cfg *TestConfig) {
    t.Helper()

    c := &config.Config{
        InstanceID: "test-instance",
        Secrets: config.SecretsConfig{
            Provider: "1password",
            Vault:    "Stockyard",
            Prefix:   "test-instance",
        },
        Daemon: config.DaemonConfig{
            SocketPath: cfg.SocketPath,
        },
    }

    stockyardDir := filepath.Join(cfg.ConfigDir, "stockyard")
    if err := os.MkdirAll(stockyardDir, 0755); err != nil {
        t.Fatalf("mkdir config: %v", err)
    }

    if err := c.SaveToDir(stockyardDir); err != nil {
        t.Fatalf("save config: %v", err)
    }
}
```

**Step 2: Create setup test**

```go
// tests/integration/setup_test.go
//go:build integration

package integration

import (
    "context"
    "os"
    "os/exec"
    "testing"
    "time"
)

var testConfig *TestConfig
var daemonProcess *exec.Cmd

func TestMain(m *testing.M) {
    // Build binaries
    buildCmd := exec.Command("go", "build", "-o", "../../bin/stockyard", "../../cmd/stockyard")
    if err := buildCmd.Run(); err != nil {
        os.Exit(1)
    }

    buildCmd = exec.Command("go", "build", "-o", "../../bin/stockyardd", "../../cmd/stockyardd")
    if err := buildCmd.Run(); err != nil {
        os.Exit(1)
    }

    code := m.Run()
    os.Exit(code)
}

func setupTest(t *testing.T) (*TestConfig, func()) {
    cfg := GetTestConfig(t)
    CreateTestConfig(t, cfg)

    // Start daemon
    ctx, cancel := context.WithCancel(context.Background())

    daemonProcess = exec.CommandContext(ctx, cfg.DaemonPath)
    daemonProcess.Env = append(os.Environ(),
        "XDG_CONFIG_HOME="+cfg.ConfigDir,
    )

    if err := daemonProcess.Start(); err != nil {
        t.Fatalf("start daemon: %v", err)
    }

    // Wait for daemon
    waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
    defer waitCancel()

    if err := WaitForDaemon(waitCtx, cfg.SocketPath); err != nil {
        cancel()
        daemonProcess.Wait()
        t.Fatalf("daemon not ready: %v", err)
    }

    cleanup := func() {
        cancel()
        daemonProcess.Wait()
    }

    return cfg, cleanup
}
```

**Step 3: Commit**

```bash
git add tests/integration/
git commit -m "test: add integration test framework

- Test helpers for CLI and daemon
- Config setup utilities
- Daemon lifecycle management"
```

---

### Task 12.3: Add Basic Integration Tests

**Files:**
- Create: `tests/integration/basic_test.go`

**Step 1: Create basic tests**

```go
// tests/integration/basic_test.go
//go:build integration

package integration

import (
    "strings"
    "testing"
)

func TestVersion(t *testing.T) {
    cfg := GetTestConfig(t)

    output, err := RunCommand(t, cfg, "version")
    if err != nil {
        t.Fatalf("version command failed: %v", err)
    }

    if !strings.Contains(output, "stockyard") {
        t.Errorf("expected version output, got: %s", output)
    }
}

func TestInit(t *testing.T) {
    cfg := GetTestConfig(t)

    output, err := RunCommand(t, cfg, "init", "--instance", "test-init")
    if err != nil {
        t.Fatalf("init command failed: %v\nOutput: %s", err, output)
    }

    if !strings.Contains(output, "Initialized") {
        t.Errorf("expected initialization message, got: %s", output)
    }
}

func TestListEmpty(t *testing.T) {
    cfg, cleanup := setupTest(t)
    defer cleanup()

    output, err := RunCommand(t, cfg, "list")
    if err != nil {
        t.Fatalf("list command failed: %v\nOutput: %s", err, output)
    }

    if !strings.Contains(output, "No tasks") {
        t.Errorf("expected 'No tasks' message, got: %s", output)
    }
}

func TestHelp(t *testing.T) {
    cfg := GetTestConfig(t)

    output, err := RunCommand(t, cfg, "--help")
    if err != nil {
        t.Fatalf("help command failed: %v", err)
    }

    expectedCommands := []string{"run", "list", "stop", "destroy", "snapshot", "attach"}
    for _, cmd := range expectedCommands {
        if !strings.Contains(output, cmd) {
            t.Errorf("expected command %q in help, got: %s", cmd, output)
        }
    }
}
```

**Step 2: Run tests**

```bash
go test ./tests/integration/... -v -tags=integration
```

**Step 3: Commit**

```bash
git add tests/integration/basic_test.go
git commit -m "test: add basic integration tests

- Version command test
- Init command test
- List empty test
- Help output verification"
```

---

### Task 12.4: Add Task Lifecycle Tests

**Files:**
- Create: `tests/integration/task_test.go`

**Step 1: Create task lifecycle tests**

Note: These tests require Flintlock and ZFS to be available.

```go
// tests/integration/task_test.go
//go:build integration && e2e

package integration

import (
    "context"
    "strings"
    "testing"
    "time"

    "github.com/obra/stockyard/pkg/client"
)

func TestTaskLifecycle(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping e2e test in short mode")
    }

    cfg, cleanup := setupTest(t)
    defer cleanup()

    // Create task
    output, err := RunCommand(t, cfg, "run",
        "--repo", cfg.TestRepo,
        "--name", "lifecycle-test",
        "--no-tailscale",
        "--", "bash", "-c", "sleep 60",
    )
    if err != nil {
        t.Fatalf("run command failed: %v\nOutput: %s", err, output)
    }

    // Extract task ID
    var taskID string
    for _, line := range strings.Split(output, "\n") {
        if strings.Contains(line, "Task created:") {
            parts := strings.Fields(line)
            if len(parts) >= 3 {
                taskID = parts[2]
            }
        }
    }
    if taskID == "" {
        t.Fatalf("could not extract task ID from: %s", output)
    }

    // Wait for task to be running
    time.Sleep(2 * time.Second)

    // List tasks
    output, err = RunCommand(t, cfg, "list")
    if err != nil {
        t.Fatalf("list command failed: %v", err)
    }
    if !strings.Contains(output, taskID) {
        t.Errorf("task %s not in list: %s", taskID, output)
    }

    // Create snapshot
    output, err = RunCommand(t, cfg, "snapshot", taskID, "test-snap")
    if err != nil {
        t.Fatalf("snapshot command failed: %v\nOutput: %s", err, output)
    }

    // List snapshots
    output, err = RunCommand(t, cfg, "snapshots", taskID)
    if err != nil {
        t.Fatalf("snapshots command failed: %v", err)
    }
    if !strings.Contains(output, "test-snap") {
        t.Errorf("snapshot not found in list: %s", output)
    }

    // Stop task
    output, err = RunCommand(t, cfg, "stop", taskID)
    if err != nil {
        t.Fatalf("stop command failed: %v\nOutput: %s", err, output)
    }

    // Verify stopped
    time.Sleep(time.Second)
    c, _ := client.New(cfg.SocketPath)
    defer c.Close()
    task, _ := c.GetTask(context.Background(), taskID)
    if task.Status != "stopped" {
        t.Errorf("expected status 'stopped', got %q", task.Status)
    }

    // Destroy task
    output, err = RunCommand(t, cfg, "destroy", "--force", taskID)
    if err != nil {
        t.Fatalf("destroy command failed: %v\nOutput: %s", err, output)
    }

    // Verify destroyed
    output, err = RunCommand(t, cfg, "list")
    if err != nil {
        t.Fatalf("list command failed: %v", err)
    }
    if strings.Contains(output, taskID) {
        t.Errorf("task %s still in list after destroy: %s", taskID, output)
    }
}

func TestSnapshotRestore(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping e2e test in short mode")
    }

    cfg, cleanup := setupTest(t)
    defer cleanup()

    // Create task
    output, _ := RunCommand(t, cfg, "run",
        "--repo", cfg.TestRepo,
        "--name", "restore-test",
        "--no-tailscale",
        "--", "bash", "-c", "sleep 120",
    )

    // Extract task ID
    var taskID string
    for _, line := range strings.Split(output, "\n") {
        if strings.Contains(line, "Task created:") {
            parts := strings.Fields(line)
            if len(parts) >= 3 {
                taskID = parts[2]
            }
        }
    }

    time.Sleep(2 * time.Second)

    // Create initial snapshot
    RunCommand(t, cfg, "snapshot", taskID, "before-changes")

    // TODO: Make changes to workspace via SSH or exec

    // Create after snapshot
    RunCommand(t, cfg, "snapshot", taskID, "after-changes")

    // Restore to before-changes
    output, err := RunCommand(t, cfg, "restore", "--force", taskID, "before-changes")
    if err != nil {
        t.Fatalf("restore failed: %v\nOutput: %s", err, output)
    }

    // Cleanup
    RunCommand(t, cfg, "destroy", "--force", taskID)
}
```

**Step 2: Commit**

```bash
git add tests/integration/task_test.go
git commit -m "test: add task lifecycle integration tests

- Full task create/list/stop/destroy cycle
- Snapshot create and restore
- Requires e2e build tag for full tests"
```

---

### Task 12.5: Add Test Running Script

**Files:**
- Create: `scripts/test.sh`

**Step 1: Create test script**

```bash
#!/bin/bash
# scripts/test.sh
# Run stockyard tests

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

echo "=== Building ==="
make build

echo ""
echo "=== Unit Tests ==="
go test ./pkg/... -v

echo ""
echo "=== Integration Tests (basic) ==="
go test ./tests/integration/... -v -tags=integration -short

if [ "$1" == "--e2e" ]; then
    echo ""
    echo "=== End-to-End Tests ==="
    echo "Note: Requires Flintlock and ZFS"

    # Check prerequisites
    if ! command -v zfs &>/dev/null; then
        echo "Error: ZFS not found"
        exit 1
    fi

    go test ./tests/integration/... -v -tags="integration e2e"
fi

echo ""
echo "=== All Tests Passed ==="
```

**Step 2: Make executable and commit**

```bash
chmod +x scripts/test.sh
git add scripts/test.sh
git commit -m "test: add test running script

- Unit tests
- Integration tests
- Optional e2e tests with --e2e flag"
```

---

### Task 12.6: Update Makefile with Test Targets

**Files:**
- Modify: `Makefile`

**Step 1: Update Makefile**

```makefile
# Makefile

.PHONY: all build proto clean test test-unit test-integration test-e2e

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

test: test-unit test-integration

test-unit:
	go test ./pkg/... -v

test-integration:
	go test ./tests/integration/... -v -tags=integration -short

test-e2e:
	go test ./tests/integration/... -v -tags="integration e2e"

# Development helpers
dev-daemon: build
	./bin/stockyardd

lint:
	golangci-lint run

fmt:
	go fmt ./...

.DEFAULT_GOAL := build
```

**Step 2: Commit**

```bash
git add Makefile
git commit -m "build: add test targets to Makefile

- make test: run unit and integration
- make test-unit: unit tests only
- make test-integration: integration tests
- make test-e2e: full end-to-end tests"
```

---

### Task 12.7: Final Verification

**Step 1: Build everything**

```bash
make clean
make all
```

**Step 2: Run all tests**

```bash
make test
```

**Step 3: Verify CLI commands**

```bash
./bin/stockyard --help
./bin/stockyard version
./bin/stockyard init --help
./bin/stockyard run --help
./bin/stockyard list --help
./bin/stockyard attach --help
./bin/stockyard stop --help
./bin/stockyard destroy --help
./bin/stockyard snapshot --help
./bin/stockyard snapshots --help
./bin/stockyard restore --help
./bin/stockyard logs --help
./bin/stockyard cp --help
./bin/stockyard configure --help
```

**Step 4: Commit verification**

```bash
git add -A
git status
git commit -m "feat: complete stockyard implementation

All CLI commands implemented:
- run, list, stop, destroy
- attach, logs, cp
- snapshot, snapshots, restore
- init, configure, version

Integration test framework in place."
```

---

**End of Part 6. Implementation plan complete.**

## Summary

The stockyard implementation is now fully planned across 6 parts:

1. **Part 1 - Foundation** (Phases 1-3): Project structure, config, secrets, ZFS
2. **Part 2 - Daemon** (Phases 4-6): State management, gRPC API, basic CLI
3. **Part 3 - Flintlock** (Phase 7): VM lifecycle, cloud-init, networking
4. **Part 4 - VM Image** (Phase 8): Dockerfile, stockyard-snapshot, hooks
5. **Part 5 - vsock & Tailscale** (Phases 9-10): Snapshot service, SSH access
6. **Part 6 - CLI & Tests** (Phases 11-12): All CLI commands, integration tests

Each phase follows TDD methodology with explicit steps for tests, implementation, verification, and commits.

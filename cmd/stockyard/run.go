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
  stockyard run --repo github.com/org/repo --ref feature-auth \
    -- claude-code --dangerously-skip-permissions -p "implement OAuth"`,
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
		}

		return nil
	},
}

func init() {
	runCmd.Flags().StringVar(&runRepo, "repo", "", "Git repository URL (required)")
	runCmd.Flags().StringVar(&runRef, "ref", "main", "Git branch, tag, or commit")
	runCmd.Flags().StringVar(&runName, "name", "", "Human-readable task name")
	runCmd.Flags().Int32Var(&runCPUs, "cpus", 2, "Number of CPU cores")
	runCmd.Flags().StringVar(&runMemory, "memory", "4G", "Memory allocation")
	runCmd.Flags().BoolVar(&runNoTailscale, "no-tailscale", false, "Skip Tailscale")
	runCmd.Flags().StringArrayVar(&runEnv, "env", nil, "Environment variables (KEY=value)")
	rootCmd.AddCommand(runCmd)
}

// cmd/stockyard/cp.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
	Use:   "cp <task-id>:<remote-path> <local-path>",
	Short: "Copy files from a task",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		source := args[0]
		dest := args[1]

		parts := strings.SplitN(source, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("source must be in format <task-id>:<path>")
		}

		taskID := parts[0]
		remotePath := parts[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		task, err := c.GetTask(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}

		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		if task.TailscaleHostname == "" {
			return fmt.Errorf("task has no Tailscale hostname")
		}

		scpSource := fmt.Sprintf("%s@%s:%s", cfg.VM.User, task.TailscaleHostname, remotePath)

		scpCmd := exec.Command("scp",
			"-o", "StrictHostKeyChecking=accept-new",
			"-r",
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

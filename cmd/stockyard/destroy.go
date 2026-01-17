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
	destroyCmd.Flags().BoolVarP(&destroyForce, "force", "f", false, "Force destruction")
	rootCmd.AddCommand(destroyCmd)
}

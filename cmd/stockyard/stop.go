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
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

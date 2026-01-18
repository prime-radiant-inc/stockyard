// cmd/stockyard/restart.go
package main

import (
	"context"
	"fmt"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart <task-id>",
	Short: "Restart a stopped task",
	Long:  `Restart a stopped task by starting its VM again.`,
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

		fmt.Printf("Restarting task %s...\n", taskID)

		if err := c.RestartTask(context.Background(), taskID); err != nil {
			return fmt.Errorf("failed to restart task: %w", err)
		}

		fmt.Println("Task restarted.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restartCmd)
}

// cmd/stockyard/restore.go
package main

import (
	"context"
	"fmt"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore <task-id> <snapshot-name>",
	Short: "Restore a task's workspace to a snapshot",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		snapName := args[1]

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		c, err := client.New(cfg.Daemon.SocketPath)
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}
		defer c.Close()

		if err := c.RestoreSnapshot(context.Background(), taskID, snapName); err != nil {
			return fmt.Errorf("failed to restore snapshot: %w", err)
		}

		fmt.Printf("Restored to snapshot: %s\n", snapName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restoreCmd)
}

// cmd/stockyard/snapshot.go
package main

import (
	"context"
	"fmt"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <task-id> [label]",
	Short: "Create a snapshot of a task's workspace",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		label := ""
		if len(args) > 1 {
			label = args[1]
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		c, err := client.New(cfg.Daemon.SocketPath)
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
		}
		defer c.Close()

		snapName, err := c.CreateSnapshot(context.Background(), taskID, label)
		if err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}

		fmt.Printf("Created snapshot: %s\n", snapName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
}

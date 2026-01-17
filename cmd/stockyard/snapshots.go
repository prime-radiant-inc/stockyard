// cmd/stockyard/snapshots.go
package main

import (
	"context"
	"fmt"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var snapshotsCmd = &cobra.Command{
	Use:   "snapshots <task-id>",
	Short: "List snapshots for a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		c, err := client.New(cfg.Daemon.SocketPath)
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w", err)
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

		for _, s := range snapshots {
			fmt.Println(s.Name)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(snapshotsCmd)
}

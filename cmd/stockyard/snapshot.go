// cmd/stockyard/snapshot.go
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <task-id> [label]",
	Short: "Create a manual snapshot",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		label := "manual"
		if len(args) > 1 {
			label = args[1]
		}

		c, err := getClient()
		if err != nil {
			return err
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

// cmd/stockyard/restore.go
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var restoreForce bool

var restoreCmd = &cobra.Command{
	Use:   "restore <task-id> <snapshot-name>",
	Short: "Restore a task to a snapshot",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		snapshotName := args[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		task, err := c.GetTask(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}

		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		if !restoreForce {
			fmt.Printf("About to restore task %s to snapshot %s\n", taskID, snapshotName)
			if task.Status == "running" {
				fmt.Printf("Warning: Task is running. Restore will stop the VM.\n")
			}
			fmt.Printf("This will roll back all changes since the snapshot.\n")
			fmt.Printf("Run with --force to confirm.\n")
			return nil
		}

		fmt.Printf("Restoring task %s to %s...\n", taskID, snapshotName)

		if err := c.RestoreSnapshot(context.Background(), taskID, snapshotName); err != nil {
			return fmt.Errorf("failed to restore: %w", err)
		}

		fmt.Println("Restored successfully.")
		return nil
	},
}

func init() {
	restoreCmd.Flags().BoolVarP(&restoreForce, "force", "f", false, "Force restore")
	rootCmd.AddCommand(restoreCmd)
}

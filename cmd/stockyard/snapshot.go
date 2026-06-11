// cmd/stockyard/snapshot.go
package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage task snapshots",
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create <task-id> [label]",
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

var snapshotLsCmd = &cobra.Command{
	Use:     "ls <task-id>",
	Aliases: []string{"list"},
	Short:   "List snapshots for a task",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := getClient()
		if err != nil {
			return err
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

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCREATED")
		for _, s := range snapshots {
			fmt.Fprintf(w, "%s\t%s\n", s.Name, s.CreatedAt)
		}
		w.Flush()

		return nil
	},
}

var snapshotRestoreForce bool

var snapshotRestoreCmd = &cobra.Command{
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

		if !snapshotRestoreForce {
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
	snapshotRestoreCmd.Flags().BoolVarP(&snapshotRestoreForce, "force", "f", false, "Force restore")
	snapshotCmd.AddCommand(snapshotCreateCmd, snapshotLsCmd, snapshotRestoreCmd)
	rootCmd.AddCommand(snapshotCmd)
}

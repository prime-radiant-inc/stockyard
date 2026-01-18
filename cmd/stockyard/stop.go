// cmd/stockyard/stop.go
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <task-id>",
	Short: "Stop a running task (workspace persists)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := getClient()
		if err != nil {
			return err
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

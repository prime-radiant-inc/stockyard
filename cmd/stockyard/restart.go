// cmd/stockyard/restart.go
package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart <task-id>",
	Short: "Restart a stopped task",
	Long:  `Restart a stopped task by starting its VM again.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := getClient()
		if err != nil {
			return err
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

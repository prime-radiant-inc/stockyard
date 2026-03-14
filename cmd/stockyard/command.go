// cmd/stockyard/command.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var commandLogsFollow bool

var commandCmd = &cobra.Command{
	Use:   "command",
	Short: "Inspect and view output of individual commands",
}

var commandStatusCmd = &cobra.Command{
	Use:   "status <task-id> <command-id>",
	Short: "Show status and details of a command",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// task-id is accepted for API symmetry but command-id is globally unique
		commandID := args[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		info, err := c.GetCommandStatus(context.Background(), commandID)
		if err != nil {
			return fmt.Errorf("failed to get command status: %w", err)
		}

		if info == nil {
			return fmt.Errorf("command not found: %s", commandID)
		}

		fmt.Printf("ID:             %s\n", info.Id)
		fmt.Printf("Queue:          %s\n", info.QueueName)
		fmt.Printf("Status:         %s\n", info.Status)
		fmt.Printf("Command:        %s\n", strings.Join(info.Command, " "))
		fmt.Printf("Stop on fail:   %v\n", info.StopOnFailure)
		if info.Status == "completed" || info.Status == "failed" {
			fmt.Printf("Exit code:      %d\n", info.ExitCode)
		}
		if info.CreatedAt != "" {
			fmt.Printf("Created at:     %s\n", info.CreatedAt)
		}
		if info.StartedAt != "" {
			fmt.Printf("Started at:     %s\n", info.StartedAt)
		}
		if info.FinishedAt != "" {
			fmt.Printf("Finished at:    %s\n", info.FinishedAt)
		}
		return nil
	},
}

var commandLogsCmd = &cobra.Command{
	Use:   "logs <task-id> <command-id>",
	Short: "Stream output from a command",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// task-id is accepted for API symmetry but command-id is globally unique
		commandID := args[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.StreamCommandOutput(context.Background(), commandID, commandLogsFollow, os.Stdout); err != nil {
			return fmt.Errorf("failed to stream command output: %w", err)
		}
		return nil
	},
}

func init() {
	commandLogsCmd.Flags().BoolVarP(&commandLogsFollow, "follow", "f", false, "Follow output (stream until command completes)")
	commandCmd.AddCommand(commandStatusCmd)
	commandCmd.AddCommand(commandLogsCmd)
	rootCmd.AddCommand(commandCmd)
}

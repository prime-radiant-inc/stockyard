// cmd/stockyard/queue.go
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var queueCreateMode string

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Manage command queues for a task",
}

var queueCreateCmd = &cobra.Command{
	Use:   "create <task-id> <name>",
	Short: "Create a new command queue",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID, queueName := args[0], args[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.CreateQueue(context.Background(), taskID, queueName, queueCreateMode); err != nil {
			return fmt.Errorf("failed to create queue: %w", err)
		}

		fmt.Printf("Queue created: %s (mode: %s)\n", queueName, queueCreateMode)
		return nil
	},
}

var queueListCmd = &cobra.Command{
	Use:   "list <task-id>",
	Short: "List queues for a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		queues, err := c.ListQueues(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to list queues: %w", err)
		}

		if len(queues) == 0 {
			fmt.Println("No queues found.")
			return nil
		}

		fmt.Printf("%-20s  %-8s  %-9s  %s\n", "NAME", "MODE", "PROTECTED", "STATUS")
		fmt.Println(strings.Repeat("-", 55))
		for _, q := range queues {
			protected := "no"
			if q.Protected {
				protected = "yes"
			}
			fmt.Printf("%-20s  %-8s  %-9s  %s\n", q.Name, q.Mode, protected, q.Status)
		}
		return nil
	},
}

var queueStatusCmd = &cobra.Command{
	Use:   "status <task-id> [queue-name]",
	Short: "Show queue status and its commands",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		queueName := "default"
		if len(args) > 1 {
			queueName = args[1]
		}

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		queue, commands, err := c.GetQueueStatus(context.Background(), taskID, queueName)
		if err != nil {
			return fmt.Errorf("failed to get queue status: %w", err)
		}

		if queue == nil {
			return fmt.Errorf("queue not found: %s", queueName)
		}

		protected := "no"
		if queue.Protected {
			protected = "yes"
		}
		fmt.Printf("Queue:     %s\n", queue.Name)
		fmt.Printf("Mode:      %s\n", queue.Mode)
		fmt.Printf("Status:    %s\n", queue.Status)
		fmt.Printf("Protected: %s\n", protected)

		if len(commands) == 0 {
			fmt.Println("\nNo commands in queue.")
			return nil
		}

		fmt.Printf("\n%-36s  %-10s  %-5s  %s\n", "COMMAND ID", "STATUS", "EXIT", "COMMAND")
		fmt.Println(strings.Repeat("-", 80))
		for _, cmd := range commands {
			cmdStr := strings.Join(cmd.Command, " ")
			if len(cmdStr) > 30 {
				cmdStr = cmdStr[:27] + "..."
			}
			exitCode := "-"
			if cmd.Status == "completed" || cmd.Status == "failed" {
				exitCode = fmt.Sprintf("%d", cmd.ExitCode)
			}
			fmt.Printf("%-36s  %-10s  %-5s  %s\n", cmd.Id, cmd.Status, exitCode, cmdStr)
		}
		return nil
	},
}

var queueFlushCmd = &cobra.Command{
	Use:   "flush <task-id> <queue-name>",
	Short: "Remove all pending commands from a queue",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID, queueName := args[0], args[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.FlushQueue(context.Background(), taskID, queueName); err != nil {
			return fmt.Errorf("failed to flush queue: %w", err)
		}

		fmt.Printf("Queue flushed: %s\n", queueName)
		return nil
	},
}

var queueDestroyCmd = &cobra.Command{
	Use:   "destroy <task-id> <queue-name>",
	Short: "Destroy a queue and all its commands",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID, queueName := args[0], args[1]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		if err := c.DestroyQueue(context.Background(), taskID, queueName); err != nil {
			return fmt.Errorf("failed to destroy queue: %w", err)
		}

		fmt.Printf("Queue destroyed: %s\n", queueName)
		return nil
	},
}

func init() {
	queueCreateCmd.Flags().StringVar(&queueCreateMode, "mode", "serial", "Queue execution mode (serial)")
	queueCmd.AddCommand(queueCreateCmd)
	queueCmd.AddCommand(queueListCmd)
	queueCmd.AddCommand(queueStatusCmd)
	queueCmd.AddCommand(queueFlushCmd)
	queueCmd.AddCommand(queueDestroyCmd)
	rootCmd.AddCommand(queueCmd)
}

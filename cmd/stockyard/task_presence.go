package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/obra/stockyard/pkg/client"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type taskPresence string

const (
	taskPresencePresent        taskPresence = "present"
	taskPresenceCleanupPending taskPresence = "cleanup_pending"
	taskPresenceAbsent         taskPresence = "absent"
)

type taskPresenceResult struct {
	TaskID       string       `json:"task_id"`
	TaskPresence taskPresence `json:"task_presence"`
}

func newTaskPresenceCommand(newClient func() (*client.Client, error)) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "task-presence <task-id>",
		Short: "Report exact task-row presence",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !jsonOutput {
				return fmt.Errorf("task-presence requires --json")
			}
			c, err := newClient()
			if err != nil {
				return err
			}
			defer c.Close()
			return writeTaskPresence(cmd.Context(), c, args[0], cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit one JSON result")
	return cmd
}

func writeTaskPresence(ctx context.Context, c *client.Client, taskID string, output io.Writer) error {
	task, err := c.GetTask(ctx, taskID)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return json.NewEncoder(output).Encode(taskPresenceResult{
				TaskID:       taskID,
				TaskPresence: taskPresenceAbsent,
			})
		}
		return fmt.Errorf("get task %q: %w", taskID, err)
	}
	if task == nil {
		return fmt.Errorf("get task %q returned an empty task", taskID)
	}
	if task.GetId() != taskID {
		return fmt.Errorf("get task %q returned task %q", taskID, task.GetId())
	}

	presence := taskPresencePresent
	if task.GetStatus() == string(taskPresenceCleanupPending) {
		presence = taskPresenceCleanupPending
	}
	return json.NewEncoder(output).Encode(taskPresenceResult{TaskID: taskID, TaskPresence: presence})
}

func init() {
	rootCmd.AddCommand(newTaskPresenceCommand(getClient))
}

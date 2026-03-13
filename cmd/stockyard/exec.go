// cmd/stockyard/exec.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	execQueue         string
	execNoStopOnFail  bool
	execEnv           []string
)

var execCmd = &cobra.Command{
	Use:   "exec <task-id> [--queue=default] [--no-stop-on-failure] [--env KEY=val] -- <command...>",
	Short: "Queue a command for execution in a task VM",
	Long: `Queue a command for execution inside a running task VM.

The command is appended to the specified queue (default: "default") and
executed in order. By default, the queue stops on the first failure.

Examples:
  stockyard exec <task-id> -- ls -la
  stockyard exec <task-id> --queue=build -- make test
  stockyard exec <task-id> --no-stop-on-failure --env FOO=bar -- ./run.sh`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		// Find command after --
		var command []string
		for i, arg := range os.Args {
			if arg == "--" && i+1 < len(os.Args) {
				command = os.Args[i+1:]
				break
			}
		}

		if len(command) == 0 {
			return fmt.Errorf("no command specified; use -- <command...>")
		}

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		// Parse environment variables
		env := make(map[string]string)
		for _, e := range execEnv {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}

		stopOnFailure := !execNoStopOnFail

		commandID, err := c.QueueCommand(context.Background(), taskID, execQueue, command, env, stopOnFailure)
		if err != nil {
			return fmt.Errorf("failed to queue command: %w", err)
		}

		fmt.Printf("Command queued: %s\n", commandID)
		return nil
	},
}

func init() {
	execCmd.Flags().StringVar(&execQueue, "queue", "default", "Queue name to submit command to")
	execCmd.Flags().BoolVar(&execNoStopOnFail, "no-stop-on-failure", false, "Continue queue execution even if this command fails")
	execCmd.Flags().StringArrayVar(&execEnv, "env", nil, "Environment variables (KEY=value)")
	rootCmd.AddCommand(execCmd)
}

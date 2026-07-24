// cmd/stockyard/destroy.go
package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/obra/stockyard/pkg/client"
	"github.com/spf13/cobra"
)

func quoteTaskNameForDisplay(name string) string {
	return strconv.QuoteToASCII(name)
}

func quotePOSIXShellArgument(value string) (string, bool) {
	if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", false
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'", true
}

func newDestroyCommand(newClient func() (*client.Client, error)) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "destroy <task-id>",
		Short: "Destroy a task and its workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]

			c, err := newClient()
			if err != nil {
				return err
			}
			defer c.Close()

			task, err := c.GetTask(cmd.Context(), taskID)
			if err != nil {
				return fmt.Errorf("failed to get task: %w", err)
			}
			if task == nil {
				return fmt.Errorf("get task %q returned an empty task", taskID)
			}
			if task.GetId() != taskID {
				return fmt.Errorf("get task %q returned task %q", taskID, task.GetId())
			}

			output := cmd.OutOrStdout()
			if !force {
				fmt.Fprintf(output, "About to destroy task %s:\n", taskID)
				fmt.Fprintln(output, "\nThis will delete the VM and all workspace data.")
				fmt.Fprintln(output, "Run with --force to confirm.")
				return nil
			}

			fmt.Fprintf(output, "Destroying task %s...\n", taskID)
			if err := c.DestroyTask(cmd.Context(), taskID); err != nil {
				return fmt.Errorf("failed to destroy task: %w", err)
			}
			fmt.Fprintln(output, "Task destroyed.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force destruction")
	return cmd
}

var destroyCmd = newDestroyCommand(getClient)

func init() {
	rootCmd.AddCommand(destroyCmd)
}

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
	var confirmName string
	cmd := &cobra.Command{
		Use:   "destroy <task-id>",
		Short: "Destroy a task and its workspace",
		Long: `Destroy a task and its workspace.

Without --force, the command only previews the destruction. Unnamed tasks
require --force. Named tasks require both --force and --confirm-name, and the
confirmation must match the stored task name byte-for-byte.`,
		Args: cobra.ExactArgs(1),
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

			taskName := task.GetName()
			displayName := quoteTaskNameForDisplay(taskName)
			output := cmd.OutOrStdout()
			if !force {
				fmt.Fprintf(output, "About to destroy task %s", taskID)
				if taskName != "" {
					fmt.Fprintf(output, " (name %s)", displayName)
				}
				fmt.Fprintln(output, ":")
				fmt.Fprintln(output, "\nThis will delete the VM and all workspace data.")
				if taskName == "" {
					fmt.Fprintln(output, "Run with --force to confirm.")
				} else if quotedName, ok := quotePOSIXShellArgument(taskName); ok {
					fmt.Fprintf(output, "Add --force --confirm-name=%s to the same command.\n", quotedName)
				} else {
					fmt.Fprintf(
						output,
						"Task name: %s. Add --force and --confirm-name with that exact value; no pasteable suffix is shown because the name contains non-printing characters.\n",
						displayName,
					)
				}
				return nil
			}

			if cmd.Flags().Changed("confirm-name") && taskName == "" {
				return fmt.Errorf(
					"refusing to destroy unnamed task %q: --confirm-name was provided but the task has no name",
					taskID,
				)
			}
			if taskName != "" && confirmName != taskName {
				return fmt.Errorf(
					"refusing to destroy named task %q (name %s): --confirm-name must match exactly",
					taskID,
					displayName,
				)
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
	cmd.Flags().StringVar(&confirmName, "confirm-name", "", "Confirm a named task by its exact task name")
	return cmd
}

var destroyCmd = newDestroyCommand(getClient)

func init() {
	rootCmd.AddCommand(destroyCmd)
}

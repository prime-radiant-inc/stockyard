// cmd/stockyard/destroy.go
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

var destroyForce bool

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

var destroyCmd = &cobra.Command{
	Use:   "destroy <task-id>",
	Short: "Destroy a task and its workspace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

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

		if !destroyForce {
			fmt.Printf("About to destroy task %s:\n", taskID)
			fmt.Printf("\nThis will delete the VM and all workspace data.\n")
			fmt.Printf("Run with --force to confirm.\n")
			return nil
		}

		fmt.Printf("Destroying task %s...\n", taskID)

		if err := c.DestroyTask(context.Background(), taskID); err != nil {
			return fmt.Errorf("failed to destroy task: %w", err)
		}

		fmt.Println("Task destroyed.")
		return nil
	},
}

func init() {
	destroyCmd.Flags().BoolVarP(&destroyForce, "force", "f", false, "Force destruction")
	rootCmd.AddCommand(destroyCmd)
}

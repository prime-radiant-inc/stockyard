// cmd/stockyard/list.go
package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var listStatus string

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "List tasks",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		tasks, err := c.ListTasks(context.Background(), listStatus)
		if err != nil {
			return err
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tSTATUS\tCREATED")
		for _, t := range tasks {
			name := t.Name
			if name == "" {
				name = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				t.Id, name, t.Status, t.CreatedAt)
		}
		w.Flush()

		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (running, stopped, failed)")
	rootCmd.AddCommand(listCmd)
}

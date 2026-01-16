// cmd/stockyard/version.go
package main

import (
	"fmt"

	"github.com/obra/stockyard/pkg/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("stockyard %s\n", version.Version)
		fmt.Printf("  commit: %s\n", version.GitCommit)
		fmt.Printf("  built:  %s\n", version.BuildDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

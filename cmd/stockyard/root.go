// cmd/stockyard/root.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "stockyard",
	Short: "Coding agent VM orchestrator",
	Long:  `Stockyard runs coding agents in isolated Firecracker micro-VMs with ZFS-based audit trail snapshots.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

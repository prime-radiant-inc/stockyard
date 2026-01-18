// cmd/stockyard/root.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var urlFlag string

var rootCmd = &cobra.Command{
	Use:   "stockyard",
	Short: "Coding agent VM orchestrator",
	Long: `Stockyard runs coding agents in isolated Firecracker micro-VMs
with ZFS-based audit trail snapshots.

Quick Start:
  # Initialize stockyard
  stockyard init --instance my-dev

  # Start the daemon (in another terminal)
  stockyardd

  # Run a coding agent
  stockyard run --repo github.com/org/repo -- claude-code -p "your prompt"

  # Attach to the running VM
  stockyard attach <task-id>

  # List running tasks
  stockyard list`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&urlFlag, "url", "", "Daemon URL (env: STOCKYARD_URL)\n  Formats: unix:///path, grpc://host:port, grpcs://host:port")
}

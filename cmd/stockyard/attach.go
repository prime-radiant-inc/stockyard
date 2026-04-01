// cmd/stockyard/attach.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var attachCmd = &cobra.Command{
	Use:   "attach <task-id>",
	Short: "Attach to a running task via SSH",
	Long:  `Attach to a running task's VM via SSH through Tailscale.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Get task details
		task, err := c.GetTask(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}

		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		if task.Status != "running" {
			return fmt.Errorf("task is not running (status: %s)", task.Status)
		}

		// Determine SSH target — prefer Tailscale hostname, fall back to direct IP
		sshHost := task.TailscaleHostname
		if sshHost == "" {
			sshHost = task.Ip
		}
		if sshHost == "" {
			return fmt.Errorf("task has no reachable address (no Tailscale hostname or IP)")
		}
		sshUser := cfg.VM.User

		fmt.Printf("Connecting to %s@%s...\n", sshUser, sshHost)

		// Exec SSH (replaces current process)
		sshPath, err := exec.LookPath("ssh")
		if err != nil {
			return fmt.Errorf("ssh not found: %w", err)
		}

		sshArgs := []string{"ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "LogLevel=ERROR",
			fmt.Sprintf("%s@%s", sshUser, sshHost),
		}
		// Append any extra args after "--"
		if cmd.ArgsLenAtDash() >= 0 {
			sshArgs = append(sshArgs, "--")
			sshArgs = append(sshArgs, args[1:]...)
		}

		return syscall.Exec(sshPath, sshArgs, os.Environ())
	},
}

func init() {
	rootCmd.AddCommand(attachCmd)
}

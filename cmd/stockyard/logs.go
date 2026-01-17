// cmd/stockyard/logs.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsSystem bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <task-id>",
	Short: "Get logs from a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		c, err := client.New(cfg.Daemon.SocketPath)
		if err != nil {
			return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
		}
		defer c.Close()

		task, err := c.GetTask(context.Background(), taskID)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}

		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		if task.Status != "running" || task.TailscaleHostname == "" {
			return fmt.Errorf("task is not running or has no Tailscale access")
		}

		return streamLogsSSH(task.TailscaleHostname, logsFollow, logsSystem)
	},
}

func streamLogsSSH(hostname string, follow bool, system bool) error {
	var logPath string
	if system {
		logPath = "/var/log/cloud-init-output.log"
	} else {
		logPath = "/workspace/.claude/logs/latest.log"
	}

	sshArgs := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		fmt.Sprintf("vscode@%s", hostname),
	}

	if follow {
		sshArgs = append(sshArgs, "tail", "-f", logPath)
	} else {
		sshArgs = append(sshArgs, "cat", logPath)
	}

	sshCmd := exec.Command("ssh", sshArgs...)
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr

	return sshCmd.Run()
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	logsCmd.Flags().BoolVar(&logsSystem, "system", false, "Show system logs")
	rootCmd.AddCommand(logsCmd)
}

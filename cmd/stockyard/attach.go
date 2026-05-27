// cmd/stockyard/attach.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

// buildAttachCommand decides the program and argv to exec for `stockyard attach`,
// dispatching on the task's backend. extraArgs are appended after a "--" separator
// for SSH or directly as the remote command for `container exec`.
//
// For apple-container: `container exec -t -i -u <user> stockyard-<vmID> <shell-or-extraArgs>`.
// For Firecracker (and any other backend): SSH via Tailscale hostname (or IP fallback).
func buildAttachCommand(task *pb.Task, sshUser, containerBin string, extraArgs []string) (string, []string, error) {
	if task.GetBackend() == "apple-container" {
		vmID := task.GetVmId()
		if vmID == "" {
			return "", nil, fmt.Errorf("apple-container task has no vm_id")
		}
		argv := []string{containerBin, "exec", "-t", "-i", "-u", sshUser, "stockyard-" + vmID}
		if len(extraArgs) > 0 {
			argv = append(argv, extraArgs...)
		} else {
			argv = append(argv, "/bin/bash")
		}
		return containerBin, argv, nil
	}

	// Default: SSH via Tailscale hostname, falling back to direct IP.
	sshHost := task.GetTailscaleHostname()
	if sshHost == "" {
		sshHost = task.GetIp()
	}
	if sshHost == "" {
		return "", nil, fmt.Errorf("task has no reachable address (no Tailscale hostname or IP)")
	}
	argv := []string{"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		fmt.Sprintf("%s@%s", sshUser, sshHost),
	}
	if len(extraArgs) > 0 {
		argv = append(argv, "--")
		argv = append(argv, extraArgs...)
	}
	return "ssh", argv, nil
}

var attachCmd = &cobra.Command{
	Use:   "attach <task-id>",
	Short: "Attach to a running task",
	Long:  `Attach to a running task — via container exec (apple-container) or SSH (Firecracker, the fallback).`,
	Args:  cobra.MinimumNArgs(1),
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

		var extraArgs []string
		if cmd.ArgsLenAtDash() >= 0 {
			extraArgs = args[1:]
		}

		containerBin := cfg.AppleContainer.ContainerBin
		if containerBin == "" {
			containerBin = "container"
		}

		program, argv, err := buildAttachCommand(task, cfg.VM.User, containerBin, extraArgs)
		if err != nil {
			return err
		}

		progPath, err := exec.LookPath(program)
		if err != nil {
			return fmt.Errorf("%s not found: %w", program, err)
		}

		fmt.Printf("Attaching to %s...\n", taskID)
		return syscall.Exec(progPath, argv, os.Environ())
	},
}

func init() {
	rootCmd.AddCommand(attachCmd)
}

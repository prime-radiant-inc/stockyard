// cmd/stockyard/run.go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/spf13/cobra"
)

var (
	runRepo             string
	runRef              string
	runName             string
	runCPUs             int32
	runMemory           string
	runNoTailscale      bool
	runTailscaleAuthKey string
	runEnv              []string
)

var runCmd = &cobra.Command{
	Use:   "run [flags] -- <command> [args...]",
	Short: "Run a coding agent in a new VM",
	Long: `Run a coding agent in a new Firecracker micro-VM.

Examples:
  stockyard run --repo github.com/org/repo --ref feature-auth \
    -- claude-code --dangerously-skip-permissions -p "implement OAuth"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runRepo == "" {
			return fmt.Errorf("--repo is required")
		}

		// Find command after --
		var command []string
		for i, arg := range os.Args {
			if arg == "--" && i+1 < len(os.Args) {
				command = os.Args[i+1:]
				break
			}
		}

		if len(command) == 0 {
			return fmt.Errorf("command is required after --")
		}

		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		// Parse environment variables
		env := make(map[string]string)
		for _, e := range runEnv {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}

		ref := runRef
		if ref == "" {
			ref = "main"
		}

		// Read SSH public keys from ~/.ssh/
		sshKeys := readSSHPublicKeys()

		req := &pb.CreateTaskRequest{
			Repo:              runRepo,
			Ref:               ref,
			Name:              runName,
			Command:           command,
			Env:               env,
			Cpus:              runCPUs,
			Memory:            runMemory,
			NoTailscale:       runNoTailscale,
			TailscaleAuthKey:  runTailscaleAuthKey,
			SshAuthorizedKeys: sshKeys,
		}

		fmt.Printf("Creating task for %s@%s...\n", runRepo, ref)

		resp, err := c.CreateTask(context.Background(), req)
		if err != nil {
			return fmt.Errorf("failed to create task: %w", err)
		}

		fmt.Printf("Task created: %s\n", resp.TaskId)
		if resp.TailscaleHostname != "" {
			fmt.Printf("Tailscale hostname: %s\n", resp.TailscaleHostname)
			fmt.Printf("\nTo attach: stockyard attach %s\n", resp.TaskId)
		}

		return nil
	},
}

// readSSHPublicKeys reads SSH public keys from ~/.ssh/*.pub
func readSSHPublicKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	sshDir := filepath.Join(home, ".ssh")
	patterns := []string{"id_*.pub", "*.pub"}

	var keys []string
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(sshDir, pattern))
		if err != nil {
			continue
		}
		for _, path := range matches {
			if seen[path] {
				continue
			}
			seen[path] = true

			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			key := strings.TrimSpace(string(data))
			if key != "" && strings.HasPrefix(key, "ssh-") {
				keys = append(keys, key)
			}
		}
	}

	return keys
}

func init() {
	runCmd.Flags().StringVar(&runRepo, "repo", "", "Git repository URL (required)")
	runCmd.Flags().StringVar(&runRef, "ref", "main", "Git branch, tag, or commit")
	runCmd.Flags().StringVar(&runName, "name", "", "Human-readable task name")
	runCmd.Flags().Int32Var(&runCPUs, "cpus", 2, "Number of CPU cores")
	runCmd.Flags().StringVar(&runMemory, "memory", "4G", "Memory allocation")
	runCmd.Flags().BoolVar(&runNoTailscale, "no-tailscale", false, "Skip Tailscale")
	runCmd.Flags().StringVar(&runTailscaleAuthKey, "tailscale-auth-key", "", "Tailscale auth key (overrides 1Password)")
	runCmd.Flags().StringArrayVar(&runEnv, "env", nil, "Environment variables (KEY=value)")
	rootCmd.AddCommand(runCmd)
}

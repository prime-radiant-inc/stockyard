// cmd/stockyard/init.go
package main

import (
	"fmt"

	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var initInstanceName string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize stockyard configuration",
	Long:  `Initialize stockyard configuration with an instance name.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if cfg.InstanceID != "" {
			fmt.Printf("Warning: overwriting existing instance ID %q\n", cfg.InstanceID)
		}

		cfg.InstanceID = initInstanceName
		cfg.Secrets.Prefix = initInstanceName

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		configDir, err := config.ConfigDir()
		if err != nil {
			configDir = "~/.config/stockyard"
		}
		fmt.Printf("Initialized stockyard instance %q\n", initInstanceName)
		fmt.Printf("Config saved to %s/config.json\n", configDir)
		fmt.Printf("\nNext steps:\n")
		fmt.Printf("  1. Create 1Password vault 'Stockyard' (if not exists)\n")
		fmt.Printf("  2. Add secrets under op://Stockyard/%s/\n", initInstanceName)
		fmt.Printf("     - anthropic-api-key\n")
		fmt.Printf("     - github-token\n")
		fmt.Printf("     - tailscale-auth-key\n")

		return nil
	},
}

func init() {
	initCmd.Flags().StringVar(&initInstanceName, "instance", "", "Instance name (required)")
	initCmd.MarkFlagRequired("instance")
	rootCmd.AddCommand(initCmd)
}

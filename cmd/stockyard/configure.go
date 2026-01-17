// cmd/stockyard/configure.go
package main

import (
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Interactive configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			// Use defaults if no config exists
			cfg = config.DefaultConfig()
		}

		fmt.Println("Stockyard Configuration")
		fmt.Println("========================")
		fmt.Println()

		// Instance ID
		instancePrompt := &survey.Input{
			Message: "Instance ID:",
			Default: cfg.InstanceID,
			Help:    "Unique identifier for this stockyard instance",
		}
		survey.AskOne(instancePrompt, &cfg.InstanceID)

		// Secrets provider
		providerPrompt := &survey.Select{
			Message: "Secrets provider:",
			Options: []string{"1password", "aws"},
			Default: cfg.Secrets.Provider,
		}
		survey.AskOne(providerPrompt, &cfg.Secrets.Provider)

		// 1Password vault
		if cfg.Secrets.Provider == "1password" {
			vaultPrompt := &survey.Input{
				Message: "1Password vault:",
				Default: cfg.Secrets.Vault,
			}
			survey.AskOne(vaultPrompt, &cfg.Secrets.Vault)
		}

		// Secrets prefix (auto-set from instance ID)
		cfg.Secrets.Prefix = cfg.InstanceID

		// Daemon socket
		socketPrompt := &survey.Input{
			Message: "Daemon socket path:",
			Default: cfg.Daemon.SocketPath,
		}
		survey.AskOne(socketPrompt, &cfg.Daemon.SocketPath)

		// Confirm save
		var save bool
		confirmPrompt := &survey.Confirm{
			Message: "Save configuration?",
			Default: true,
		}
		survey.AskOne(confirmPrompt, &save)

		if save {
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save: %w", err)
			}

			configDir, _ := config.ConfigDir()
			fmt.Printf("\nConfiguration saved to %s/config.json\n", configDir)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(configureCmd)
}

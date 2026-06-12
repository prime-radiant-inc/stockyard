// cmd/stockyard/init.go
package main

import (
	"fmt"
	"io"
	"runtime"

	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var (
	initInstanceName string
	initBackend      string
	initImage        string
)

// defaultBackend returns the platform-default VM backend for a GOOS value:
// apple-container on darwin, firecracker everywhere else. Extracted (rather
// than branching on runtime.GOOS inline) so both arms are testable on any
// development machine.
func defaultBackend(goos string) string {
	if goos == "darwin" {
		return "apple-container"
	}
	return "firecracker"
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize stockyard configuration",
	Long: `Initialize stockyard configuration: instance name, VM backend, and
(for apple-container) the default task image. Everything else — socket path,
DHCP ranges, secrets directory — is edited directly in config.json.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		backend := initBackend
		if backend == "" {
			backend = defaultBackend(runtime.GOOS)
		}
		if backend != "firecracker" && backend != "apple-container" {
			return fmt.Errorf("invalid --backend %q (valid: firecracker, apple-container)", backend)
		}
		if initImage != "" && backend == "firecracker" {
			return fmt.Errorf("--image is only valid with --backend apple-container; " +
				"the firecracker default image comes from rootfs_path via " +
				"'stockyard image import' (see 'make -C vm-image deploy')")
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		out := cmd.OutOrStdout()
		if cfg.InstanceID != "" {
			fmt.Fprintf(out, "Warning: overwriting existing instance ID %q\n", cfg.InstanceID)
		}

		cfg.InstanceID = initInstanceName
		cfg.Secrets.Prefix = initInstanceName
		// Always write the backend explicitly so the file says what the
		// daemon will do, even when it matches the platform default.
		cfg.Backend = backend
		if initImage != "" {
			cfg.AppleContainer.Image = initImage
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		configDir, err := config.ConfigDir()
		if err != nil {
			configDir = "~/.config/stockyard"
		}
		fmt.Fprintf(out, "Initialized stockyard instance %q (backend: %s)\n", initInstanceName, backend)
		fmt.Fprintf(out, "Config saved to %s/config.json\n", configDir)
		printNextSteps(out, cfg)

		return nil
	},
}

// printNextSteps prints truthful per-backend setup guidance. Secrets are
// optional on both backends: the daemon resolves each secret through a
// FallbackProvider — 1Password first, then files in secrets.dir
// (cmd/stockyardd/main.go) — and tasks run without them.
func printNextSteps(out io.Writer, cfg *config.Config) {
	fmt.Fprintf(out, "\nNext steps:\n")
	switch cfg.Backend {
	case "apple-container":
		fmt.Fprintf(out, "  1. Make sure a task image exists in the container CLI's local store:\n")
		fmt.Fprintf(out, "       container image ls\n")
		fmt.Fprintf(out, "     Build the reference image with: make -C vm-image container-image\n")
		fmt.Fprintf(out, "     (tagged stockyard.local/stockyard-vm:container)\n")
		if cfg.AppleContainer.Image == "" {
			fmt.Fprintf(out, "  2. Set the default task image: re-run init with --image <ref>,\n")
			fmt.Fprintf(out, "     or edit apple_container.image in config.json\n")
		} else {
			fmt.Fprintf(out, "  2. Default task image: %s\n", cfg.AppleContainer.Image)
			fmt.Fprintf(out, "     (override per task with 'stockyard run --image <ref>')\n")
		}
		fmt.Fprintf(out, "  3. Start the container service, then the daemon:\n")
		fmt.Fprintf(out, "       container system start && stockyardd\n")
	default: // firecracker
		fmt.Fprintf(out, "  1. Build and register the default VM image:\n")
		fmt.Fprintf(out, "       make -C vm-image deploy\n")
		fmt.Fprintf(out, "     (builds the rootfs and registers it via 'stockyard image import default';\n")
		fmt.Fprintf(out, "      named variants: make -C vm-image deploy-image REGISTRY_IMAGE=<name>)\n")
		fmt.Fprintf(out, "  2. Start the daemon: stockyardd\n")
		fmt.Fprintf(out, "     (Linux hosts: install scripts/stockyardd.service and run 'systemctl start stockyardd')\n")
	}

	secretsDir := cfg.Secrets.Dir
	if secretsDir == "" {
		secretsDir = "/etc/stockyard/secrets" // mirrors cmd/stockyardd/main.go
	}
	vault := cfg.Secrets.Vault
	if vault == "" {
		vault = "Stockyard"
	}
	fmt.Fprintf(out, "\nSecrets (optional — tasks run without them):\n")
	fmt.Fprintf(out, "  For each secret the daemon tries 1Password (op://%s/%s/<name>)\n", vault, cfg.InstanceID)
	fmt.Fprintf(out, "  and falls back to files in %s:\n", secretsDir)
	fmt.Fprintf(out, "    - anthropic-api-key\n")
	fmt.Fprintf(out, "    - github-token\n")
	fmt.Fprintf(out, "    - tailscale-auth-key\n")
}

func init() {
	initCmd.Flags().StringVar(&initInstanceName, "instance", "", "Instance name (required)")
	initCmd.Flags().StringVar(&initBackend, "backend", "",
		"VM backend: firecracker or apple-container (default: apple-container on macOS, firecracker elsewhere)")
	initCmd.Flags().StringVar(&initImage, "image", "",
		"Default task image for the apple-container backend (seeds apple_container.image)")
	initCmd.MarkFlagRequired("instance")
	rootCmd.AddCommand(initCmd)
}

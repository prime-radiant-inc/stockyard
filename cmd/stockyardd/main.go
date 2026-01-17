// cmd/stockyardd/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/daemon"
	"github.com/obra/stockyard/pkg/secrets"
	"github.com/obra/stockyard/pkg/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("stockyardd %s\n", version.Version)
		os.Exit(0)
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if cfg.InstanceID == "" {
		return fmt.Errorf("stockyard not initialized. Run: stockyard init --instance <name>")
	}

	secretsProvider := secrets.NewOnePasswordProvider(cfg.Secrets.Vault, cfg.Secrets.Prefix)

	d, err := daemon.New(cfg, secretsProvider)
	if err != nil {
		return fmt.Errorf("failed to create daemon: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	fmt.Printf("Starting stockyardd for instance %q\n", cfg.InstanceID)
	return d.Start(ctx)
}

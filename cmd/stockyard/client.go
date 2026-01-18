// cmd/stockyard/client.go
package main

import (
	"fmt"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
)

// getClient returns a client connected to the daemon.
// It resolves the URL from: --url flag -> STOCKYARD_URL env -> config -> default
func getClient() (*client.Client, error) {
	// Load config to get socket path (may not exist for remote-only usage)
	var configSocketPath string
	if cfg, err := config.Load(); err == nil {
		configSocketPath = cfg.Daemon.SocketPath
	}

	url := client.ResolveURL(urlFlag, configSocketPath)

	c, err := client.NewFromURL(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon at %s: %w\nIs stockyardd running?", url, err)
	}

	return c, nil
}

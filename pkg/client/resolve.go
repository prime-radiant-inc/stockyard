// pkg/client/resolve.go
package client

import "os"

const DefaultSocketPath = "/var/run/stockyard/stockyard.sock"

// ResolveURL determines the daemon URL using the following precedence:
// 1. flagURL (from --url flag)
// 2. STOCKYARD_URL environment variable
// 3. configSocketPath (from system config)
// 4. Default socket path
func ResolveURL(flagURL, configSocketPath string) string {
	// 1. Flag takes highest precedence
	if flagURL != "" {
		return flagURL
	}

	// 2. Environment variable
	if envURL := os.Getenv("STOCKYARD_URL"); envURL != "" {
		return envURL
	}

	// 3. Config socket path (convert to unix:// URL)
	if configSocketPath != "" {
		return "unix://" + configSocketPath
	}

	// 4. Default
	return "unix://" + DefaultSocketPath
}

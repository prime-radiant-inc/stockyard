// pkg/client/url.go
package client

import (
	"fmt"
	"strings"
)

// ParseURL parses a stockyard URL and returns the address and whether TLS is required.
// Supported schemes:
//   - unix:///path/to/socket - Unix socket
//   - grpc://host:port - TCP without TLS
//   - grpcs://host:port - TCP with TLS
//   - host:port - defaults to grpc://
func ParseURL(rawURL string) (addr string, tls bool, err error) {
	if rawURL == "" {
		return "", false, fmt.Errorf("empty URL")
	}

	// Check for scheme
	if strings.HasPrefix(rawURL, "unix://") {
		return rawURL, false, nil
	}

	if strings.HasPrefix(rawURL, "grpcs://") {
		return strings.TrimPrefix(rawURL, "grpcs://"), true, nil
	}

	if strings.HasPrefix(rawURL, "grpc://") {
		return strings.TrimPrefix(rawURL, "grpc://"), false, nil
	}

	// Check for invalid schemes
	if strings.Contains(rawURL, "://") {
		return "", false, fmt.Errorf("unsupported URL scheme: %s (use unix://, grpc://, or grpcs://)", rawURL)
	}

	// Bare host:port - default to grpc://
	if strings.Contains(rawURL, ":") {
		return rawURL, false, nil
	}

	return "", false, fmt.Errorf("invalid URL format: %s", rawURL)
}

// pkg/client/resolve_test.go
package client

import (
	"os"
	"testing"
)

func TestResolveURL(t *testing.T) {
	// Clean env before each test
	originalEnv := os.Getenv("STOCKYARD_URL")
	defer os.Setenv("STOCKYARD_URL", originalEnv)

	tests := []struct {
		name       string
		flagURL    string
		envURL     string
		configPath string
		want       string
	}{
		{
			name:    "flag takes precedence",
			flagURL: "grpc://flag-host:1234",
			envURL:  "grpc://env-host:5678",
			want:    "grpc://flag-host:1234",
		},
		{
			name:    "env used when no flag",
			flagURL: "",
			envURL:  "grpc://env-host:5678",
			want:    "grpc://env-host:5678",
		},
		{
			name:       "config socket used when no flag or env",
			flagURL:    "",
			envURL:     "",
			configPath: "/custom/socket.sock",
			want:       "unix:///custom/socket.sock",
		},
		{
			name:       "default when nothing set",
			flagURL:    "",
			envURL:     "",
			configPath: "",
			want:       "unix:///var/run/stockyard/stockyard.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("STOCKYARD_URL", tt.envURL)
			got := ResolveURL(tt.flagURL, tt.configPath)
			if got != tt.want {
				t.Errorf("ResolveURL(%q, %q) = %q, want %q", tt.flagURL, tt.configPath, got, tt.want)
			}
		})
	}
}

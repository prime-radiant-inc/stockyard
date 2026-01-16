package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	InstanceID string        `json:"instance_id"`
	Secrets    SecretsConfig `json:"secrets"`
	Daemon     DaemonConfig  `json:"daemon"`
	ZFS        ZFSConfig     `json:"zfs"`
}

type SecretsConfig struct {
	Provider string `json:"provider"`
	Vault    string `json:"vault"`
	Prefix   string `json:"prefix"`
}

type DaemonConfig struct {
	SocketPath string `json:"socket_path"`
}

type ZFSConfig struct {
	Pool     string `json:"pool"`
	BasePath string `json:"base_path"`
}

func DefaultConfig() *Config {
	return &Config{
		Secrets: SecretsConfig{
			Provider: "1password",
			Vault:    "Stockyard",
		},
		Daemon: DaemonConfig{
			SocketPath: "/var/run/stockyard/stockyard.sock",
		},
		ZFS: ZFSConfig{
			Pool:     "tank",
			BasePath: "stockyard/workspaces",
		},
	}
}

func LoadFromDir(dir string) (*Config, error) {
	configPath := filepath.Join(dir, "config.json")

	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) SaveToDir(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	configPath := filepath.Join(dir, "config.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0644)
}

func Load() (*Config, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	return LoadFromDir(configDir)
}

func (c *Config) Save() error {
	configDir, err := ConfigDir()
	if err != nil {
		return err
	}
	return c.SaveToDir(configDir)
}

func ConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "stockyard"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".config", "stockyard"), nil
}

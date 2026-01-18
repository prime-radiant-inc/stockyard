package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	InstanceID  string            `json:"instance_id"`
	Secrets     SecretsConfig     `json:"secrets"`
	Daemon      DaemonConfig      `json:"daemon"`
	ZFS         ZFSConfig         `json:"zfs"`
	Firecracker FirecrackerConfig `json:"firecracker"`
	VM          VMConfig          `json:"vm"`
	HTTP        HTTPConfig        `json:"http"`
}

type VMConfig struct {
	User string `json:"user"`
}

type SecretsConfig struct {
	Provider string `json:"provider"`
	Vault    string `json:"vault"`  // For 1password provider
	Prefix   string `json:"prefix"` // For 1password provider
	Dir      string `json:"dir"`    // For file provider
}

type DaemonConfig struct {
	SocketPath string `json:"socket_path"`
	DataDir    string `json:"data_dir"`
}

type ZFSConfig struct {
	Pool       string `json:"pool"`
	BasePath   string `json:"base_path"`
	ImagesPath string `json:"images_path"`
	VMsPath    string `json:"vms_path"`
}

type FirecrackerConfig struct {
	KernelPath     string `json:"kernel_path"`
	RootfsPath     string `json:"rootfs_path"`
	BridgeName     string `json:"bridge_name"`
	VMSubnet       string `json:"vm_subnet"`
	VMGateway      string `json:"vm_gateway"`
	DHCPRangeStart string `json:"dhcp_range_start"`
	DHCPRangeEnd   string `json:"dhcp_range_end"`
	DHCPLeaseTime  string `json:"dhcp_lease_time"`
}

// HTTPConfig configures the web dashboard HTTP server.
type HTTPConfig struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr"`
}

func DefaultConfig() *Config {
	return &Config{
		Secrets: SecretsConfig{
			Provider: "1password",
			Vault:    "Stockyard",
		},
		Daemon: DaemonConfig{
			SocketPath: "/var/run/stockyard/stockyard.sock",
			DataDir:    "/var/lib/stockyard",
		},
		ZFS: ZFSConfig{
			Pool:       "tank",
			BasePath:   "stockyard/workspaces",
			ImagesPath: "stockyard/images",
			VMsPath:    "stockyard/vms",
		},
		Firecracker: FirecrackerConfig{
			KernelPath:     "/tmp/vmlinux.bin",
			RootfsPath:     "/var/lib/stockyard/rootfs.ext4",
			BridgeName:     "flbr0",
			VMSubnet:       "192.168.64.0/18",
			VMGateway:      "192.168.64.1",
			DHCPRangeStart: "192.168.64.2",
			DHCPRangeEnd:   "192.168.127.254",
			DHCPLeaseTime:  "12h",
		},
		VM: VMConfig{
			User: "mooby",
		},
		HTTP: HTTPConfig{
			Enabled: false,
			Addr:    ":8080",
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
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

func (c *Config) SaveToDir(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(dir, "config.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

func Load() (*Config, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}
	return LoadFromDir(configDir)
}

func (c *Config) Save() error {
	configDir, err := ConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}
	return c.SaveToDir(configDir)
}

func ConfigDir() (string, error) {
	// Check for explicit config directory
	if dir := os.Getenv("STOCKYARD_CONFIG_DIR"); dir != "" {
		return dir, nil
	}

	// Check system-wide config first (for daemon/root usage)
	systemDir := "/etc/stockyard"
	if _, err := os.Stat(filepath.Join(systemDir, "config.json")); err == nil {
		return systemDir, nil
	}

	// Fall back to user config directories
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "stockyard"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		// If no home dir and no system config, use /etc/stockyard as default
		return systemDir, nil
	}

	return filepath.Join(home, ".config", "stockyard"), nil
}

// VMDir returns the path to VM state directories.
func (c *Config) VMDir() string {
	return filepath.Join(c.Daemon.DataDir, "vms", "stockyard")
}

// DHCPLeaseFile returns the path to the DHCP lease file.
func (c *Config) DHCPLeaseFile() string {
	return filepath.Join(c.Daemon.DataDir, "dnsmasq.leases")
}

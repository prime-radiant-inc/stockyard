// Package firecracker provides direct Firecracker microVM management.
package firecracker

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CloudInitConfig specifies settings for cloud-init user-data generation.
type CloudInitConfig struct {
	Hostname          string
	Environment       map[string]string
	SSHAuthorizedKeys []string
	WorkspacePath     string
	PostCreateScript  string
}

// cloudInitData represents the cloud-init YAML structure.
type cloudInitData struct {
	Hostname          string          `yaml:"hostname,omitempty"`
	ManageEtcHosts    bool            `yaml:"manage_etc_hosts,omitempty"`
	SSHAuthorizedKeys []string        `yaml:"ssh_authorized_keys,omitempty"`
	WriteFiles        []cloudInitFile `yaml:"write_files,omitempty"`
	RunCmd            []string        `yaml:"runcmd,omitempty"`
}

// cloudInitFile represents a file to write via cloud-init.
type cloudInitFile struct {
	Path        string `yaml:"path"`
	Permissions string `yaml:"permissions,omitempty"`
	Content     string `yaml:"content"`
}

// Generate creates base64-encoded cloud-init user-data from the configuration.
func (c *CloudInitConfig) Generate() (string, error) {
	data := &cloudInitData{
		Hostname:       c.Hostname,
		ManageEtcHosts: true,
	}

	if len(c.SSHAuthorizedKeys) > 0 {
		data.SSHAuthorizedKeys = c.SSHAuthorizedKeys
	}

	// Build write_files section
	var files []cloudInitFile

	// Write environment variables to /etc/stockyard/env
	if len(c.Environment) > 0 {
		envContent := c.buildEnvFileContent()
		files = append(files, cloudInitFile{
			Path:        "/etc/stockyard/env",
			Permissions: "0600",
			Content:     envContent,
		})

		// Write shell profile script for environment variables
		profileContent := c.buildProfileScript()
		files = append(files, cloudInitFile{
			Path:        "/etc/profile.d/stockyard.sh",
			Permissions: "0644",
			Content:     profileContent,
		})
	}

	// Write workspace path config if specified
	if c.WorkspacePath != "" {
		files = append(files, cloudInitFile{
			Path:        "/etc/stockyard/workspace",
			Permissions: "0644",
			Content:     c.WorkspacePath,
		})
	}

	// Write post-create script if specified
	if c.PostCreateScript != "" {
		files = append(files, cloudInitFile{
			Path:        "/etc/stockyard/post-create.sh",
			Permissions: "0755",
			Content:     c.PostCreateScript,
		})
	}

	if len(files) > 0 {
		data.WriteFiles = files
	}

	// Build runcmd section
	var cmds []string

	// Create stockyard directory
	cmds = append(cmds, "mkdir -p /etc/stockyard")

	// Run post-create script if provided
	if c.PostCreateScript != "" {
		cmds = append(cmds, "/etc/stockyard/post-create.sh")
	}

	if len(cmds) > 0 {
		data.RunCmd = cmds
	}

	// Generate YAML
	yamlBytes, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal cloud-init data: %w", err)
	}

	// Prepend cloud-config header
	cloudConfig := "#cloud-config\n" + string(yamlBytes)

	// Encode to base64
	encoded := base64.StdEncoding.EncodeToString([]byte(cloudConfig))

	return encoded, nil
}

// buildEnvFileContent creates the content for /etc/stockyard/env.
func (c *CloudInitConfig) buildEnvFileContent() string {
	var lines []string

	// Sort keys for deterministic output
	keys := make([]string, 0, len(c.Environment))
	for k := range c.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := c.Environment[k]
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}

	return strings.Join(lines, "\n") + "\n"
}

// buildProfileScript creates the content for /etc/profile.d/stockyard.sh.
func (c *CloudInitConfig) buildProfileScript() string {
	var lines []string
	lines = append(lines, "#!/bin/bash")
	lines = append(lines, "# Stockyard environment variables")

	// Sort keys for deterministic output
	keys := make([]string, 0, len(c.Environment))
	for k := range c.Environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := c.Environment[k]
		lines = append(lines, fmt.Sprintf("export %s=%q", k, v))
	}

	return strings.Join(lines, "\n") + "\n"
}

// MMDSNetworkConfig holds static IP configuration for MMDS.
type MMDSNetworkConfig struct {
	IP      string `json:"ip"`
	Netmask string `json:"netmask"`
	Gateway string `json:"gateway"`
	DNS     string `json:"dns"`
}

// MMDSMetadata holds metadata fields for MMDS.
type MMDSMetadata struct {
	InstanceID        string
	Hostname          string
	TailscaleAuthKey  string
	SSHAuthorizedKeys []string
	UserData          string
	NetworkConfig     *MMDSNetworkConfig // Static IP configuration (optional)
	TailscaleState    []byte             // Pre-registered Tailscale state (optional)
}

// BuildMMDSData constructs the MMDS data structure for cloud-init.
func BuildMMDSData(meta MMDSMetadata) map[string]interface{} {
	metaData := map[string]interface{}{
		"instance-id":    meta.InstanceID,
		"local-hostname": meta.Hostname,
	}
	if meta.TailscaleAuthKey != "" {
		metaData["tailscale-auth-key"] = meta.TailscaleAuthKey
	}
	if len(meta.SSHAuthorizedKeys) > 0 {
		metaData["ssh-authorized-keys"] = strings.Join(meta.SSHAuthorizedKeys, "\n")
	}
	if meta.NetworkConfig != nil {
		metaData["network-config"] = map[string]interface{}{
			"ip":      meta.NetworkConfig.IP,
			"netmask": meta.NetworkConfig.Netmask,
			"gateway": meta.NetworkConfig.Gateway,
			"dns":     meta.NetworkConfig.DNS,
		}
	}
	if len(meta.TailscaleState) > 0 {
		// Base64 encode for safe JSON transport
		metaData["tailscale-state"] = base64.StdEncoding.EncodeToString(meta.TailscaleState)
	}
	return map[string]interface{}{
		"latest": map[string]interface{}{
			"meta-data": metaData,
			"user-data": meta.UserData,
		},
	}
}

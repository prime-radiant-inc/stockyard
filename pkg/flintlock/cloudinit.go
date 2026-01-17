package flintlock

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
	TailscaleAuthKey  string
	TailscaleHostname string
	WorkspacePath     string
	PostCreateScript  string
}

// cloudInitData represents the cloud-init YAML structure.
type cloudInitData struct {
	Hostname        string           `yaml:"hostname,omitempty"`
	ManageEtcHosts  bool             `yaml:"manage_etc_hosts,omitempty"`
	SSHAuthorizedKeys []string       `yaml:"ssh_authorized_keys,omitempty"`
	WriteFiles      []cloudInitFile  `yaml:"write_files,omitempty"`
	RunCmd          []string         `yaml:"runcmd,omitempty"`
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

	// Set up Tailscale if auth key provided
	if c.TailscaleAuthKey != "" {
		tsHostname := c.TailscaleHostname
		if tsHostname == "" {
			tsHostname = c.Hostname
		}
		cmds = append(cmds, fmt.Sprintf("tailscale up --authkey=%s --hostname=%s --accept-routes --ssh",
			c.TailscaleAuthKey, tsHostname))
	}

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

package flintlock

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCloudInitConfig_Generate(t *testing.T) {
	cfg := &CloudInitConfig{
		Hostname: "stockyard-task-123",
		Environment: map[string]string{
			"ANTHROPIC_API_KEY": "sk-test-123",
			"GITHUB_TOKEN":      "ghp_test456",
		},
		SSHAuthorizedKeys: []string{
			"ssh-ed25519 AAAA... user@host",
		},
		TailscaleAuthKey: "tskey-auth-xxx",
		WorkspacePath:    "/workspace",
	}

	userData, err := cfg.Generate()
	if err != nil {
		t.Fatalf("failed to generate cloud-init: %v", err)
	}

	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(userData)
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}

	content := string(decoded)

	// Should start with cloud-init header
	if !strings.HasPrefix(content, "#cloud-config\n") {
		t.Error("should start with #cloud-config")
	}

	// Should contain hostname
	if !strings.Contains(content, "stockyard-task-123") {
		t.Error("should contain hostname")
	}

	// Should contain tailscale setup
	if !strings.Contains(content, "tailscale") {
		t.Error("should contain tailscale setup")
	}

	// Should contain SSH key
	if !strings.Contains(content, "ssh-ed25519") {
		t.Error("should contain SSH authorized key")
	}

	// Should contain environment variables
	if !strings.Contains(content, "ANTHROPIC_API_KEY") {
		t.Error("should contain ANTHROPIC_API_KEY environment variable")
	}
	if !strings.Contains(content, "GITHUB_TOKEN") {
		t.Error("should contain GITHUB_TOKEN environment variable")
	}

	// Should contain workspace path
	if !strings.Contains(content, "/workspace") {
		t.Error("should contain workspace path")
	}
}

func TestCloudInitConfig_Generate_MinimalConfig(t *testing.T) {
	cfg := &CloudInitConfig{
		Hostname: "minimal-vm",
	}

	userData, err := cfg.Generate()
	if err != nil {
		t.Fatalf("failed to generate cloud-init: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(userData)
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}

	content := string(decoded)

	if !strings.HasPrefix(content, "#cloud-config\n") {
		t.Error("should start with #cloud-config")
	}

	if !strings.Contains(content, "minimal-vm") {
		t.Error("should contain hostname")
	}
}

func TestCloudInitConfig_Generate_WithPostCreateScript(t *testing.T) {
	cfg := &CloudInitConfig{
		Hostname:         "script-vm",
		PostCreateScript: "#!/bin/bash\necho 'Hello from post-create script'",
	}

	userData, err := cfg.Generate()
	if err != nil {
		t.Fatalf("failed to generate cloud-init: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(userData)
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}

	content := string(decoded)

	if !strings.Contains(content, "post-create script") {
		t.Error("should contain post-create script content")
	}
}

func TestCloudInitConfig_Generate_NoTailscaleWithoutKey(t *testing.T) {
	cfg := &CloudInitConfig{
		Hostname:         "no-tailscale-vm",
		TailscaleAuthKey: "",
	}

	userData, err := cfg.Generate()
	if err != nil {
		t.Fatalf("failed to generate cloud-init: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(userData)
	if err != nil {
		t.Fatalf("failed to decode base64: %v", err)
	}

	content := string(decoded)

	// Should not contain tailscale up command with auth key when no key provided
	if strings.Contains(content, "tailscale up --authkey") {
		t.Error("should not contain tailscale auth command when no key provided")
	}
}

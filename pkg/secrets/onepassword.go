package secrets

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Compile-time interface compliance check
var _ Provider = (*OnePasswordProvider)(nil)

// OnePasswordProvider retrieves secrets from 1Password using the op CLI.
type OnePasswordProvider struct {
	Vault  string
	Prefix string
}

// NewOnePasswordProvider creates a new 1Password provider.
func NewOnePasswordProvider(vault, prefix string) *OnePasswordProvider {
	return &OnePasswordProvider{
		Vault:  vault,
		Prefix: prefix,
	}
}

func (p *OnePasswordProvider) buildPath(name string) string {
	return fmt.Sprintf("op://%s/%s/%s", p.Vault, p.Prefix, name)
}

// GetSecret retrieves a secret from 1Password using the op CLI.
func (p *OnePasswordProvider) GetSecret(ctx context.Context, name string) (string, error) {
	path := p.buildPath(name)

	cmd := exec.CommandContext(ctx, "op", "read", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("op read failed: %w: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Name returns the provider name.
func (p *OnePasswordProvider) Name() string {
	return "1password"
}

package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Compile-time interface compliance check
var _ Provider = (*FileProvider)(nil)

// FileProvider retrieves secrets from files in a directory.
type FileProvider struct {
	Dir string
}

// NewFileProvider creates a new file-based secrets provider.
func NewFileProvider(dir string) *FileProvider {
	return &FileProvider{Dir: dir}
}

// GetSecret retrieves a secret from a file.
func (p *FileProvider) GetSecret(ctx context.Context, name string) (string, error) {
	path := filepath.Join(p.Dir, name)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("secret %q not found in %s", name, p.Dir)
		}
		return "", fmt.Errorf("failed to read secret %q: %w", name, err)
	}

	return strings.TrimSpace(string(data)), nil
}

// Name returns the provider name.
func (p *FileProvider) Name() string {
	return "file"
}

package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileProvider_GetSecret(t *testing.T) {
	// Create temp directory with test secrets
	dir := t.TempDir()

	// Write test secret
	secretPath := filepath.Join(dir, "test-secret")
	if err := os.WriteFile(secretPath, []byte("secret-value\n"), 0600); err != nil {
		t.Fatalf("failed to write test secret: %v", err)
	}

	provider := NewFileProvider(dir)
	ctx := context.Background()

	t.Run("existing secret", func(t *testing.T) {
		val, err := provider.GetSecret(ctx, "test-secret")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "secret-value" {
			t.Errorf("got %q, want %q", val, "secret-value")
		}
	})

	t.Run("missing secret", func(t *testing.T) {
		_, err := provider.GetSecret(ctx, "nonexistent")
		if err == nil {
			t.Error("expected error for missing secret")
		}
	})

	t.Run("name", func(t *testing.T) {
		if provider.Name() != "file" {
			t.Errorf("got %q, want %q", provider.Name(), "file")
		}
	})
}

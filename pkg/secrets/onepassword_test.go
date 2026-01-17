package secrets

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOnePasswordProvider_BuildPath(t *testing.T) {
	p := &OnePasswordProvider{
		Vault:  "Stockyard",
		Prefix: "flower-garden",
	}

	path := p.buildPath("anthropic-api-key")
	expected := "op://Stockyard/flower-garden/anthropic-api-key"

	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}

func TestOnePasswordProvider_GetSecret_Error(t *testing.T) {
	p := &OnePasswordProvider{
		Vault:  "NonExistent",
		Prefix: "test",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// This will fail because the vault/item doesn't exist (or op isn't installed)
	_, err := p.GetSecret(ctx, "fake-secret")
	if err == nil {
		t.Skip("op CLI succeeded unexpectedly (may have matching secret)")
	}

	// Verify error contains context
	if !strings.Contains(err.Error(), "op read failed") {
		t.Errorf("error should contain 'op read failed', got: %v", err)
	}
}

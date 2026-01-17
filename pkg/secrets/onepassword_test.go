package secrets

import (
	"testing"
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

package secrets

import (
	"context"
	"fmt"
	"strings"
)

// Compile-time interface compliance check
var _ Provider = (*FallbackProvider)(nil)

// FallbackProvider tries multiple providers in order until one succeeds.
type FallbackProvider struct {
	Providers []Provider
}

// NewFallbackProvider creates a provider that tries each provider in order.
func NewFallbackProvider(providers ...Provider) *FallbackProvider {
	return &FallbackProvider{Providers: providers}
}

// GetSecret tries each provider in order until one succeeds.
func (p *FallbackProvider) GetSecret(ctx context.Context, name string) (string, error) {
	var errors []string

	for _, provider := range p.Providers {
		secret, err := provider.GetSecret(ctx, name)
		if err == nil {
			return secret, nil
		}
		errors = append(errors, fmt.Sprintf("%s: %v", provider.Name(), err))
	}

	return "", fmt.Errorf("all providers failed for secret %q: %s", name, strings.Join(errors, "; "))
}

// Name returns the provider name.
func (p *FallbackProvider) Name() string {
	names := make([]string, len(p.Providers))
	for i, provider := range p.Providers {
		names[i] = provider.Name()
	}
	return "fallback(" + strings.Join(names, ",") + ")"
}

package secrets

import (
	"context"
	"fmt"
)

// Provider defines the interface for retrieving secrets from various backends.
type Provider interface {
	GetSecret(ctx context.Context, name string) (string, error)
	Name() string
}

// MockProvider is a test implementation of Provider that stores secrets in memory.
type MockProvider struct {
	Secrets map[string]string
}

func (m *MockProvider) GetSecret(ctx context.Context, name string) (string, error) {
	if val, ok := m.Secrets[name]; ok {
		return val, nil
	}
	return "", fmt.Errorf("secret %q not found", name)
}

func (m *MockProvider) Name() string {
	return "mock"
}

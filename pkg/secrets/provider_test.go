package secrets

import (
	"context"
	"testing"
)

func TestMockProvider(t *testing.T) {
	mock := &MockProvider{
		Secrets: map[string]string{
			"api-key": "test-secret-value",
		},
	}

	val, err := mock.GetSecret(context.Background(), "api-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val != "test-secret-value" {
		t.Errorf("got %q, want %q", val, "test-secret-value")
	}

	_, err = mock.GetSecret(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent secret")
	}
}

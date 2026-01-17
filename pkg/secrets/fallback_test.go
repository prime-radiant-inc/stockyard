package secrets

import (
	"context"
	"testing"
)

func TestFallbackProvider_GetSecret(t *testing.T) {
	ctx := context.Background()

	t.Run("first provider succeeds", func(t *testing.T) {
		p1 := &MockProvider{Secrets: map[string]string{"key": "value1"}}
		p2 := &MockProvider{Secrets: map[string]string{"key": "value2"}}
		fallback := NewFallbackProvider(p1, p2)

		val, err := fallback.GetSecret(ctx, "key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "value1" {
			t.Errorf("got %q, want %q (should use first provider)", val, "value1")
		}
	})

	t.Run("falls back to second provider", func(t *testing.T) {
		p1 := &MockProvider{Secrets: map[string]string{}}
		p2 := &MockProvider{Secrets: map[string]string{"key": "value2"}}
		fallback := NewFallbackProvider(p1, p2)

		val, err := fallback.GetSecret(ctx, "key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if val != "value2" {
			t.Errorf("got %q, want %q (should fall back)", val, "value2")
		}
	})

	t.Run("all providers fail", func(t *testing.T) {
		p1 := &MockProvider{Secrets: map[string]string{}}
		p2 := &MockProvider{Secrets: map[string]string{}}
		fallback := NewFallbackProvider(p1, p2)

		_, err := fallback.GetSecret(ctx, "key")
		if err == nil {
			t.Error("expected error when all providers fail")
		}
	})

	t.Run("name", func(t *testing.T) {
		p1 := &MockProvider{}
		p2 := &MockProvider{}
		fallback := NewFallbackProvider(p1, p2)

		expected := "fallback(mock,mock)"
		if fallback.Name() != expected {
			t.Errorf("got %q, want %q", fallback.Name(), expected)
		}
	})
}

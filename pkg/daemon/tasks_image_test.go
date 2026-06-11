package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type fakeImageValidator struct {
	err     error
	lastRef string
}

func (f *fakeImageValidator) ValidateImage(ctx context.Context, ref string) error {
	f.lastRef = ref
	return f.err
}

func TestResolveTaskImage_EmptyResolvesToDefault(t *testing.T) {
	got, err := resolveTaskImage(context.Background(), "", "apple-container", "stockyard-vm:latest", &fakeImageValidator{})
	if err != nil {
		t.Fatalf("resolveTaskImage: %v", err)
	}
	if got != "stockyard-vm:latest" {
		t.Errorf("resolved = %q, want default", got)
	}
}

func TestResolveTaskImage_UnsupportedBackendRejects(t *testing.T) {
	_, err := resolveTaskImage(context.Background(), "prudence-vm:1.2", "firecracker", "default", nil)
	if err == nil {
		t.Fatal("expected rejection when backend lacks ImageValidator")
	}
	want := "firecracker backend does not support per-task images yet (PRI-2150 phase 2)"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want substring %q", err, want)
	}
}

func TestResolveTaskImage_ValidatorMissPropagates(t *testing.T) {
	v := &fakeImageValidator{err: fmt.Errorf(`image "nope" not found`)}
	_, err := resolveTaskImage(context.Background(), "nope", "apple-container", "stockyard-vm:latest", v)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected validator error to propagate, got %v", err)
	}
	if v.lastRef != "nope" {
		t.Errorf("validator called with %q, want \"nope\"", v.lastRef)
	}
}

func TestResolveTaskImage_ValidRequestResolves(t *testing.T) {
	got, err := resolveTaskImage(context.Background(), "prudence-vm:1.2", "apple-container", "stockyard-vm:latest", &fakeImageValidator{})
	if err != nil {
		t.Fatalf("resolveTaskImage: %v", err)
	}
	if got != "prudence-vm:1.2" {
		t.Errorf("resolved = %q, want requested ref", got)
	}
}

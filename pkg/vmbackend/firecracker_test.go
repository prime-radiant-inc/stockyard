package vmbackend

import (
	"testing"
)

func TestFirecrackerBackend_ImplementsInterface(t *testing.T) {
	var _ Backend = (*FirecrackerBackend)(nil)
}

func TestFirecrackerBackend_NilClient(t *testing.T) {
	b := NewFirecrackerBackend(nil)
	if err := b.Close(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

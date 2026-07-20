package vmbackend

import (
	"context"
	"errors"
	"testing"

	"github.com/obra/stockyard/pkg/firecracker"
)

type firecrackerDeleteTestClient struct {
	namespace string
	id        string
	err       error
}

func (c *firecrackerDeleteTestClient) CreateVM(context.Context, *firecracker.VMConfig) (*firecracker.VMInfo, error) {
	return nil, nil
}

func (c *firecrackerDeleteTestClient) StartVM(context.Context, *firecracker.VMConfig) (*firecracker.VMInfo, error) {
	return nil, nil
}

func (c *firecrackerDeleteTestClient) StopVM(context.Context, string, string) error { return nil }

func (c *firecrackerDeleteTestClient) DeleteVM(_ context.Context, namespace, id string) error {
	c.namespace = namespace
	c.id = id
	return c.err
}

func (c *firecrackerDeleteTestClient) GetVM(context.Context, string, string) (*firecracker.VM, error) {
	return nil, nil
}

func (c *firecrackerDeleteTestClient) ListVMs(context.Context, string) ([]*firecracker.VM, error) {
	return nil, nil
}

func (c *firecrackerDeleteTestClient) Close() error { return nil }

func TestFirecrackerBackend_ImplementsInterface(t *testing.T) {
	var _ Backend = (*FirecrackerBackend)(nil)
}

func TestFirecrackerBackend_NilClient(t *testing.T) {
	b := NewFirecrackerBackend(nil)
	if err := b.Close(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFirecrackerBackend_DeletePreservesVerifiedClientPostcondition(t *testing.T) {
	postconditionErr := errors.New("exact deletion postcondition unverified")
	client := &firecrackerDeleteTestClient{err: postconditionErr}
	backend := &FirecrackerBackend{client: client}

	if err := backend.DeleteVM(context.Background(), "task-one"); !errors.Is(err, postconditionErr) {
		t.Fatalf("DeleteVM error = %v, want postcondition failure", err)
	}
	if client.namespace != "stockyard" || client.id != "task-one" {
		t.Fatalf("client DeleteVM target = %s/%s", client.namespace, client.id)
	}
}

func TestFirecrackerBackend_DeleteReturnsNilOnlyAfterClientVerification(t *testing.T) {
	client := &firecrackerDeleteTestClient{}
	backend := &FirecrackerBackend{client: client}

	if err := backend.DeleteVM(context.Background(), "task-one"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
}

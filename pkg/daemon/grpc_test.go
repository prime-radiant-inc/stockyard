// pkg/daemon/grpc_test.go
package daemon

import (
	"context"
	"strings"
	"testing"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/secrets"
	"github.com/obra/stockyard/pkg/vmbackend"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestGRPCServer(t *testing.T, withTaskManager bool) *grpcServer {
	t.Helper()

	cfg := &config.Config{
		ZFS: config.ZFSConfig{
			Pool:     "tank",
			BasePath: "stockyard/workspaces",
		},
	}

	state, err := NewStateInMemory()
	if err != nil {
		t.Fatalf("failed to create state: %v", err)
	}
	t.Cleanup(func() { state.Close() })

	secretsProvider := &secrets.MockProvider{
		Secrets: map[string]string{},
	}

	d := &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		state:   state,
	}

	if withTaskManager {
		d.tasks = NewTaskManager(d, nil) // nil backend = no VM backend
	}

	return newGRPCServer(d)
}

func TestGRPCServer_StopTask_NoTaskManager(t *testing.T) {
	s := newTestGRPCServer(t, false)

	_, err := s.StopTask(context.Background(), &pb.StopTaskRequest{
		TaskId: "test-task",
	})

	if err == nil {
		t.Fatal("expected error when task manager not initialized")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.Unavailable {
		t.Errorf("expected Unavailable code, got %v", st.Code())
	}

	if st.Message() != "task manager not initialized" {
		t.Errorf("unexpected message: %s", st.Message())
	}
}

func TestGRPCServer_DestroyTask_NoTaskManager(t *testing.T) {
	s := newTestGRPCServer(t, false)

	_, err := s.DestroyTask(context.Background(), &pb.DestroyTaskRequest{
		TaskId: "test-task",
	})

	if err == nil {
		t.Fatal("expected error when task manager not initialized")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.Unavailable {
		t.Errorf("expected Unavailable code, got %v", st.Code())
	}

	if st.Message() != "task manager not initialized" {
		t.Errorf("unexpected message: %s", st.Message())
	}
}

func TestGRPCServer_StopTask_TaskNotFound(t *testing.T) {
	s := newTestGRPCServer(t, true)

	_, err := s.StopTask(context.Background(), &pb.StopTaskRequest{
		TaskId: "nonexistent-task",
	})

	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("expected NotFound code, got %v", st.Code())
	}
}

func TestGRPCServer_DestroyTask_TaskNotFound(t *testing.T) {
	s := newTestGRPCServer(t, true)

	_, err := s.DestroyTask(context.Background(), &pb.DestroyTaskRequest{
		TaskId: "nonexistent-task",
	})
	if err != nil {
		t.Fatalf("DestroyTask should treat exact row absence as completed cleanup: %v", err)
	}
}

func TestGRPCServer_RestartTask_NoTaskManager(t *testing.T) {
	s := newTestGRPCServer(t, false)

	_, err := s.RestartTask(context.Background(), &pb.RestartTaskRequest{
		TaskId: "test-task",
	})

	if err == nil {
		t.Fatal("expected error when task manager not initialized")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.Unavailable {
		t.Errorf("expected Unavailable code, got %v", st.Code())
	}

	if st.Message() != "task manager not initialized" {
		t.Errorf("unexpected message: %s", st.Message())
	}
}

func TestGRPCServer_RestartTask_TaskNotFound(t *testing.T) {
	s := newTestGRPCServer(t, true)

	_, err := s.RestartTask(context.Background(), &pb.RestartTaskRequest{
		TaskId: "nonexistent-task",
	})

	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("expected NotFound code, got %v", st.Code())
	}
}

func TestGRPCServer_CreateTask_NoTaskManager(t *testing.T) {
	s := newTestGRPCServer(t, false)

	_, err := s.CreateTask(context.Background(), &pb.CreateTaskRequest{
		Name: "test-task",
	})

	if err == nil {
		t.Fatal("expected error when task manager not initialized")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.Unavailable {
		t.Errorf("expected Unavailable code, got %v", st.Code())
	}
}

func TestGRPCServer_GetTask_NotFound(t *testing.T) {
	s := newTestGRPCServer(t, true)

	_, err := s.GetTask(context.Background(), &pb.GetTaskRequest{
		TaskId: "nonexistent",
	})

	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("expected NotFound code, got %v", st.Code())
	}
}

func TestGRPCServer_ListTasks_Empty(t *testing.T) {
	s := newTestGRPCServer(t, true)

	resp, err := s.ListTasks(context.Background(), &pb.ListTasksRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(resp.Tasks))
	}
}

func TestGRPCServer_GetLogs_Unimplemented(t *testing.T) {
	s := newTestGRPCServer(t, true)

	err := s.GetLogs(&pb.GetLogsRequest{TaskId: "test"}, nil)
	if err == nil {
		t.Fatal("expected error for unimplemented method")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}

	if st.Code() != codes.Unimplemented {
		t.Errorf("expected Unimplemented code, got %v", st.Code())
	}
}

// fakeListerBackend implements vmbackend.Backend trivially plus ImageLister.
type fakeListerBackend struct {
	vmbackend.Backend // nil embed: only ListImages is called in these tests
	images            []vmbackend.ImageInfo
}

func (f *fakeListerBackend) ListImages(ctx context.Context) ([]vmbackend.ImageInfo, error) {
	return f.images, nil
}

func TestGRPCServer_ListImages_ListerBackend(t *testing.T) {
	s := newTestGRPCServer(t, true)
	s.daemon.cfg.Backend = "apple-container"
	s.daemon.tasks = NewTaskManager(s.daemon, &fakeListerBackend{
		images: []vmbackend.ImageInfo{{Reference: "stockyard-vm:latest", Digest: "sha256:abc", Size: "5.6 GB"}},
	})

	resp, err := s.ListImages(context.Background(), &pb.ListImagesRequest{})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(resp.Images) != 1 || resp.Images[0].Reference != "stockyard-vm:latest" {
		t.Errorf("unexpected images: %+v", resp.Images)
	}
}

func TestGRPCServer_ListImages_UnsupportedBackend(t *testing.T) {
	s := newTestGRPCServer(t, true) // TaskManager with nil backend
	s.daemon.cfg.Backend = "firecracker"

	_, err := s.ListImages(context.Background(), &pb.ListImagesRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented, got %v", err)
	}
	if !strings.Contains(err.Error(), "firecracker") {
		t.Errorf("error should name the backend: %v", err)
	}
}

func TestGRPCServer_ImportImage_AppleContainerRedirects(t *testing.T) {
	s := newTestGRPCServer(t, true)
	s.daemon.cfg.Backend = "apple-container"

	_, err := s.ImportImage(context.Background(), &pb.ImportImageRequest{Name: "x", RootfsPath: "/tmp/x"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented, got %v", err)
	}
	if !strings.Contains(err.Error(), "container image") {
		t.Errorf("apple-container should redirect to the container CLI: %v", err)
	}
}

// newTestGRPCServerWithRegistry builds a grpcServer that has an imageRegistry
// backed by in-memory state and fakeRegistryZFS/fakeDestroyer.
func newTestGRPCServerWithRegistry(t *testing.T) (*grpcServer, *fakeRegistryZFS, *fakeDestroyer) {
	t.Helper()
	s := newTestGRPCServer(t, true)
	s.daemon.cfg.Backend = "firecracker"
	fz := &fakeRegistryZFS{snapshots: map[string]bool{}}
	fd := &fakeDestroyer{}
	s.daemon.images = &imageRegistry{
		state:      s.daemon.state,
		zfs:        fz,
		destroyer:  fd,
		pool:       "tank",
		imagesPath: "stockyard/images",
	}
	return s, fz, fd
}

func TestGRPCServer_RegistryImportListRemove(t *testing.T) {
	s, _, _ := newTestGRPCServerWithRegistry(t)
	rf := tempRootfs(t)

	// Import
	_, err := s.ImportImage(context.Background(), &pb.ImportImageRequest{Name: "prudence:1", RootfsPath: rf})
	if err != nil {
		t.Fatalf("ImportImage: %v", err)
	}

	// List — should show the imported image
	resp, err := s.ListImages(context.Background(), &pb.ListImagesRequest{})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(resp.Images) != 1 || resp.Images[0].Reference != "prudence:1" {
		t.Errorf("unexpected list: %+v", resp.Images)
	}

	// Remove
	_, err = s.RemoveImage(context.Background(), &pb.RemoveImageRequest{Name: "prudence:1"})
	if err != nil {
		t.Fatalf("RemoveImage: %v", err)
	}

	// List again — empty
	resp2, err := s.ListImages(context.Background(), &pb.ListImagesRequest{})
	if err != nil {
		t.Fatalf("ListImages after remove: %v", err)
	}
	if len(resp2.Images) != 0 {
		t.Errorf("expected 0 images after remove, got %d", len(resp2.Images))
	}
}

func TestGRPCServer_RegistryImportMissingRootfs(t *testing.T) {
	s, _, _ := newTestGRPCServerWithRegistry(t)
	_, err := s.ImportImage(context.Background(), &pb.ImportImageRequest{Name: "x", RootfsPath: "/nonexistent/rootfs.ext4"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestGRPCServer_RegistryRemoveDefault(t *testing.T) {
	s, _, _ := newTestGRPCServerWithRegistry(t)
	_, err := s.RemoveImage(context.Background(), &pb.RemoveImageRequest{Name: "default"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for default removal, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot be removed") {
		t.Errorf("expected refusal message: %v", err)
	}
}

func TestGRPCServer_ListImages_NoRegistryNoLister(t *testing.T) {
	s := newTestGRPCServer(t, true)
	s.daemon.cfg.Backend = "firecracker"
	// images is nil, backend is nil — no lister at all.

	_, err := s.ListImages(context.Background(), &pb.ListImagesRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("expected Unimplemented, got %v", err)
	}
	if !strings.Contains(err.Error(), "firecracker") {
		t.Errorf("error should name the backend: %v", err)
	}
}

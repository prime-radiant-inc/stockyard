// pkg/daemon/grpc_test.go
package daemon

import (
	"context"
	"testing"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/secrets"
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
		d.tasks = NewTaskManager(d, nil) // nil config = no firecracker client
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
		Repo: "github.com/test/repo",
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

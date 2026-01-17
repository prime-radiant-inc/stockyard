package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/obra/stockyard/pkg/api/v1"
)

type grpcServer struct {
	pb.UnimplementedStockyardServer
	daemon *Daemon
}

func newGRPCServer(d *Daemon) *grpcServer {
	return &grpcServer{daemon: d}
}

func (s *grpcServer) Register(srv *grpc.Server) {
	pb.RegisterStockyardServer(srv, s)
}

func (s *grpcServer) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.CreateTaskResponse, error) {
	if s.daemon.tasks == nil {
		return nil, status.Error(codes.Unavailable, "task manager not initialized")
	}

	task, err := s.daemon.tasks.CreateTask(ctx, &CreateTaskRequest{
		Repo:              req.Repo,
		Ref:               req.Ref,
		Name:              req.Name,
		Command:           req.Command,
		Env:               req.Env,
		CPUs:              req.Cpus,
		MemoryMB:          parseMemory(req.Memory),
		NoTailscale:       req.NoTailscale,
		TailscaleAuthKey:  req.TailscaleAuthKey,
		SSHAuthorizedKeys: req.SshAuthorizedKeys,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create task: %v", err)
	}

	hostname := ""
	if !req.NoTailscale {
		hostname = fmt.Sprintf("stockyard-%s", task.ID)
	}

	return &pb.CreateTaskResponse{
		TaskId:            task.ID,
		TailscaleHostname: hostname,
	}, nil
}

func (s *grpcServer) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.GetTaskResponse, error) {
	task, err := s.daemon.state.GetTask(req.TaskId)
	if err != nil {
		if strings.Contains(err.Error(), "task not found") {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get task: %v", err)
	}

	return &pb.GetTaskResponse{
		Task: taskToProto(task),
	}, nil
}

func (s *grpcServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	tasks, err := s.daemon.state.ListTasks(req.Status)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list tasks: %v", err)
	}

	pbTasks := make([]*pb.Task, len(tasks))
	for i, t := range tasks {
		pbTasks[i] = taskToProto(t)
	}

	return &pb.ListTasksResponse{Tasks: pbTasks}, nil
}

func (s *grpcServer) StopTask(ctx context.Context, req *pb.StopTaskRequest) (*pb.StopTaskResponse, error) {
	// TODO: Stop VM via Flintlock
	err := s.daemon.state.UpdateTaskStatus(req.TaskId, "stopped")
	if err != nil {
		if strings.Contains(err.Error(), "task not found") {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to stop task: %v", err)
	}
	return &pb.StopTaskResponse{}, nil
}

func (s *grpcServer) DestroyTask(ctx context.Context, req *pb.DestroyTaskRequest) (*pb.DestroyTaskResponse, error) {
	// TODO: Destroy VM via Flintlock
	// Destroy ZFS dataset
	if err := s.daemon.zfs.DestroyDataset(ctx, req.TaskId); err != nil {
		// Log but don't fail - dataset may not exist
		fmt.Printf("Warning: failed to destroy ZFS dataset: %v\n", err)
	}

	if err := s.daemon.state.DeleteTask(req.TaskId); err != nil {
		if strings.Contains(err.Error(), "task not found") {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to delete task: %v", err)
	}
	return &pb.DestroyTaskResponse{}, nil
}

func (s *grpcServer) CreateSnapshot(ctx context.Context, req *pb.CreateSnapshotRequest) (*pb.CreateSnapshotResponse, error) {
	// Sync filesystem first
	if err := s.daemon.zfs.Sync(ctx, req.TaskId); err != nil {
		fmt.Printf("Warning: sync failed: %v\n", err)
	}

	snapName, err := s.daemon.zfs.CreateSnapshot(ctx, req.TaskId, req.Label)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create snapshot: %v", err)
	}

	// Record in database
	if err := s.daemon.state.RecordSnapshot(req.TaskId, snapName); err != nil {
		fmt.Printf("Warning: failed to record snapshot in database: %v\n", err)
	}

	return &pb.CreateSnapshotResponse{SnapshotName: snapName}, nil
}

func (s *grpcServer) ListSnapshots(ctx context.Context, req *pb.ListSnapshotsRequest) (*pb.ListSnapshotsResponse, error) {
	snapshots, err := s.daemon.zfs.ListSnapshots(ctx, req.TaskId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list snapshots: %v", err)
	}

	pbSnaps := make([]*pb.Snapshot, len(snapshots))
	for i, name := range snapshots {
		pbSnaps[i] = &pb.Snapshot{Name: name}
	}

	return &pb.ListSnapshotsResponse{Snapshots: pbSnaps}, nil
}

func (s *grpcServer) RestoreSnapshot(ctx context.Context, req *pb.RestoreSnapshotRequest) (*pb.RestoreSnapshotResponse, error) {
	if err := s.daemon.zfs.RollbackSnapshot(ctx, req.TaskId, req.SnapshotName); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to restore snapshot: %v", err)
	}
	return &pb.RestoreSnapshotResponse{}, nil
}

func (s *grpcServer) GetLogs(req *pb.GetLogsRequest, stream grpc.ServerStreamingServer[pb.LogEntry]) error {
	// TODO: Implement log streaming
	return status.Error(codes.Unimplemented, "not implemented")
}

func taskToProto(t *Task) *pb.Task {
	pt := &pb.Task{
		Id:                t.ID,
		Name:              t.Name,
		Repo:              t.Repo,
		Ref:               t.Ref,
		Status:            t.Status,
		TailscaleHostname: t.TailscaleHostname,
		CreatedAt:         t.CreatedAt.Format(time.RFC3339),
	}
	if t.StoppedAt != nil {
		pt.StoppedAt = t.StoppedAt.Format(time.RFC3339)
	}
	return pt
}

package daemon

import (
	"context"
	"errors"
	"fmt"
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
		Name:              req.Name,
		Env:               req.VmEnv,
		CPUs:              req.Cpus,
		MemoryMB:          parseMemory(req.Memory),
		NoTailscale:       req.NoTailscale,
		TailscaleAuthKey:  req.TailscaleAuthKey,
		SSHAuthorizedKeys: req.SshAuthorizedKeys,
		DotEnv:            req.Dotenv,
		Image:             req.Image,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create task: %v", err)
	}

	// Report the hostname the task manager actually assigned — non-empty only
	// when Tailscale was genuinely set up (valid auth key). Do not synthesize
	// one here; it stays empty when Tailscale wasn't configured for this task.
	return &pb.CreateTaskResponse{
		TaskId:            task.ID,
		TailscaleHostname: task.TailscaleHostname,
	}, nil
}

func (s *grpcServer) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.GetTaskResponse, error) {
	task, err := s.daemon.state.GetTask(req.TaskId)
	if err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to get task: %v", err)
	}

	return &pb.GetTaskResponse{
		Task: taskToProto(task, s.daemon.cfg.Backend),
	}, nil
}

func (s *grpcServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	tasks, err := s.daemon.state.ListTasks(req.Status)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list tasks: %v", err)
	}

	pbTasks := make([]*pb.Task, len(tasks))
	for i, t := range tasks {
		pbTasks[i] = taskToProto(t, s.daemon.cfg.Backend)
	}

	return &pb.ListTasksResponse{Tasks: pbTasks}, nil
}

func (s *grpcServer) StopTask(ctx context.Context, req *pb.StopTaskRequest) (*pb.StopTaskResponse, error) {
	if s.daemon.tasks == nil {
		return nil, status.Error(codes.Unavailable, "task manager not initialized")
	}

	if err := s.daemon.tasks.StopTask(ctx, req.TaskId); err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to stop task: %v", err)
	}
	return &pb.StopTaskResponse{}, nil
}

func (s *grpcServer) RestartTask(ctx context.Context, req *pb.RestartTaskRequest) (*pb.RestartTaskResponse, error) {
	if s.daemon.tasks == nil {
		return nil, status.Error(codes.Unavailable, "task manager not initialized")
	}

	if err := s.daemon.tasks.RestartTask(ctx, req.TaskId); err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if errors.Is(err, ErrTaskNotStopped) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to restart task: %v", err)
	}
	return &pb.RestartTaskResponse{}, nil
}

func (s *grpcServer) DestroyTask(ctx context.Context, req *pb.DestroyTaskRequest) (*pb.DestroyTaskResponse, error) {
	if s.daemon.tasks == nil {
		return nil, status.Error(codes.Unavailable, "task manager not initialized")
	}

	if err := s.daemon.tasks.DestroyTask(ctx, req.TaskId); err != nil {
		if errors.Is(err, ErrTaskNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to destroy task: %v", err)
	}
	return &pb.DestroyTaskResponse{}, nil
}

func (s *grpcServer) CreateSnapshot(ctx context.Context, req *pb.CreateSnapshotRequest) (*pb.CreateSnapshotResponse, error) {
	if s.daemon.zfs == nil {
		return nil, status.Error(codes.Unavailable, "snapshots require ZFS (not available on this backend)")
	}

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
	if s.daemon.zfs == nil {
		return nil, status.Error(codes.Unavailable, "snapshots require ZFS (not available on this backend)")
	}

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
	if s.daemon.zfs == nil {
		return nil, status.Error(codes.Unavailable, "snapshots require ZFS (not available on this backend)")
	}

	if err := s.daemon.zfs.RollbackSnapshot(ctx, req.TaskId, req.SnapshotName); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to restore snapshot: %v", err)
	}
	return &pb.RestoreSnapshotResponse{}, nil
}

func (s *grpcServer) GetLogs(req *pb.GetLogsRequest, stream grpc.ServerStreamingServer[pb.LogEntry]) error {
	// Note: Log streaming is handled via SSH through Tailscale in the CLI.
	// This gRPC endpoint is not used by the stockyard CLI.
	// It could be implemented for programmatic access if needed.
	return status.Error(codes.Unimplemented, "use SSH via Tailscale for log access")
}

func taskToProto(t *Task, backend string) *pb.Task {
	pt := &pb.Task{
		Id:                t.ID,
		Name:              t.Name,
		Status:            t.Status,
		TailscaleHostname: t.TailscaleHostname,
		Ip:                t.IP,
		Backend:           backend,
		VmId:              t.VMID,
		Image:             t.Image,
		CreatedAt:         t.CreatedAt.Format(time.RFC3339),
	}
	if t.StoppedAt != nil {
		pt.StoppedAt = t.StoppedAt.Format(time.RFC3339)
	}
	return pt
}

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
	"github.com/obra/stockyard/pkg/vmbackend"
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

// ListImages enumerates the backend's local image store.
func (s *grpcServer) ListImages(ctx context.Context, req *pb.ListImagesRequest) (*pb.ListImagesResponse, error) {
	if s.daemon.tasks == nil {
		return nil, status.Error(codes.Unavailable, "task manager not initialized")
	}
	var lister vmbackend.ImageLister
	if s.daemon.images != nil {
		lister = s.daemon.images
	} else {
		lister, _ = s.daemon.tasks.backend.(vmbackend.ImageLister)
	}
	if lister == nil {
		return nil, status.Errorf(codes.Unimplemented,
			"image listing is not supported by the %s backend (PRI-2150 phase 2)", s.backendName())
	}
	images, err := lister.ListImages(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list images: %v", err)
	}
	pbImages := make([]*pb.ImageInfo, len(images))
	for i, img := range images {
		pbImages[i] = &pb.ImageInfo{
			Reference: img.Reference,
			Digest:    img.Digest,
			Size:      img.Size,
			CreatedAt: img.CreatedAt,
		}
	}
	return &pb.ListImagesResponse{Images: pbImages}, nil
}

// ImportImage registers a named image into the Firecracker registry, or
// redirects to the container CLI for the apple-container backend.
func (s *grpcServer) ImportImage(ctx context.Context, req *pb.ImportImageRequest) (*pb.ImportImageResponse, error) {
	if s.daemon.images != nil {
		if err := s.daemon.images.Import(ctx, req.Name, req.RootfsPath, req.KernelPath); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		return &pb.ImportImageResponse{}, nil
	}
	return nil, s.imageMutationUnsupported("import", "container image load` or `container image pull")
}

// RemoveImage unregisters a named image from the Firecracker registry, or
// redirects to the container CLI for the apple-container backend.
func (s *grpcServer) RemoveImage(ctx context.Context, req *pb.RemoveImageRequest) (*pb.RemoveImageResponse, error) {
	if s.daemon.images != nil {
		if err := s.daemon.images.Remove(ctx, req.Name); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		return &pb.RemoveImageResponse{}, nil
	}
	return nil, s.imageMutationUnsupported("remove", "container image rm")
}

func (s *grpcServer) imageMutationUnsupported(verb, containerCmd string) error {
	if s.backendName() == "apple-container" {
		return status.Errorf(codes.Unimplemented,
			"the apple-container image store is managed by the container CLI; use `%s` on the daemon host", containerCmd)
	}
	return status.Errorf(codes.Unimplemented,
		"image %s requires the Firecracker image registry (PRI-2150 phase 2)", verb)
}

// backendName returns the configured backend, naming the default explicitly.
func (s *grpcServer) backendName() string {
	if s.daemon.cfg.Backend == "" {
		return "firecracker"
	}
	return s.daemon.cfg.Backend
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

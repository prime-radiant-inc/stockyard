package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
		Name:              req.Name,
		Env:               req.VmEnv,
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
		if errors.Is(err, ErrTaskNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
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
	// Note: Log streaming is handled via SSH through Tailscale in the CLI.
	// This gRPC endpoint is not used by the stockyard CLI.
	// It could be implemented for programmatic access if needed.
	return status.Error(codes.Unimplemented, "use SSH via Tailscale for log access")
}

func (s *grpcServer) CreateQueue(ctx context.Context, req *pb.CreateQueueRequest) (*pb.CreateQueueResponse, error) {
	if s.daemon.queueManager == nil {
		return nil, status.Error(codes.Unavailable, "queue manager not initialized")
	}
	mode := req.Mode
	if mode == "" {
		mode = "serial"
	}
	if err := s.daemon.queueManager.CreateQueue(req.TaskId, req.QueueName, mode); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create queue: %v", err)
	}
	return &pb.CreateQueueResponse{}, nil
}

func (s *grpcServer) ListQueues(ctx context.Context, req *pb.ListQueuesRequest) (*pb.ListQueuesResponse, error) {
	queues, err := s.daemon.queueManager.ListQueues(req.TaskId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list queues: %v", err)
	}
	pbQueues := make([]*pb.QueueInfo, len(queues))
	for i, q := range queues {
		pbQueues[i] = &pb.QueueInfo{
			Name:      q.Name,
			Mode:      q.Mode,
			Protected: q.Protected,
			Status:    q.Status,
		}
	}
	return &pb.ListQueuesResponse{Queues: pbQueues}, nil
}

func (s *grpcServer) GetQueueStatus(ctx context.Context, req *pb.GetQueueStatusRequest) (*pb.GetQueueStatusResponse, error) {
	queue, commands, err := s.daemon.queueManager.GetQueueStatus(req.TaskId, req.QueueName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get queue status: %v", err)
	}
	pbCommands := make([]*pb.CommandInfo, len(commands))
	for i, c := range commands {
		pbCommands[i] = commandToProto(c)
	}
	return &pb.GetQueueStatusResponse{
		Queue: &pb.QueueInfo{
			Name:      queue.Name,
			Mode:      queue.Mode,
			Protected: queue.Protected,
			Status:    queue.Status,
		},
		Commands: pbCommands,
	}, nil
}

func (s *grpcServer) FlushQueue(ctx context.Context, req *pb.FlushQueueRequest) (*pb.FlushQueueResponse, error) {
	if err := s.daemon.queueManager.FlushQueue(req.TaskId, req.QueueName); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to flush queue: %v", err)
	}
	return &pb.FlushQueueResponse{}, nil
}

func (s *grpcServer) ResumeQueue(ctx context.Context, req *pb.ResumeQueueRequest) (*pb.ResumeQueueResponse, error) {
	if s.daemon.queueManager == nil {
		return nil, status.Error(codes.Unavailable, "queue manager not initialized")
	}
	if err := s.daemon.queueManager.ResumeQueue(req.TaskId, req.QueueName); err != nil {
		if errors.Is(err, ErrQueueNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Errorf(codes.FailedPrecondition, "failed to resume queue: %v", err)
	}
	return &pb.ResumeQueueResponse{}, nil
}

func (s *grpcServer) DestroyQueue(ctx context.Context, req *pb.DestroyQueueRequest) (*pb.DestroyQueueResponse, error) {
	if err := s.daemon.queueManager.DestroyQueue(req.TaskId, req.QueueName); err != nil {
		if errors.Is(err, ErrQueueProtected) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to destroy queue: %v", err)
	}
	return &pb.DestroyQueueResponse{}, nil
}

func (s *grpcServer) QueueCommand(ctx context.Context, req *pb.QueueCommandRequest) (*pb.QueueCommandResponse, error) {
	if s.daemon.queueManager == nil {
		return nil, status.Error(codes.Unavailable, "queue manager not initialized")
	}
	if len(req.Command) == 0 || req.Command[0] == "" {
		return nil, status.Error(codes.InvalidArgument, "command is required and must not be empty")
	}
	queueName := req.QueueName
	if queueName == "" {
		queueName = "default"
	}
	commandID, err := s.daemon.queueManager.QueueCommand(req.TaskId, queueName, req.Command, req.Env, req.StopOnFailure)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to queue command: %v", err)
	}
	return &pb.QueueCommandResponse{CommandId: commandID}, nil
}

func (s *grpcServer) GetCommandStatus(ctx context.Context, req *pb.GetCommandStatusRequest) (*pb.GetCommandStatusResponse, error) {
	cmd, err := s.daemon.queueManager.GetCommandStatus(req.CommandId)
	if err != nil {
		if errors.Is(err, ErrCommandNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to get command status: %v", err)
	}
	return &pb.GetCommandStatusResponse{
		Command: commandToProto(cmd),
	}, nil
}

func (s *grpcServer) StreamCommandOutput(req *pb.StreamCommandOutputRequest, stream grpc.ServerStreamingServer[pb.CommandOutputChunk]) error {
	cmd, err := s.daemon.queueManager.GetCommandStatus(req.CommandId)
	if err != nil {
		return status.Errorf(codes.NotFound, "command not found: %v", err)
	}

	if cmd.OutputPath == "" {
		return status.Error(codes.NotFound, "no output available")
	}

	// Open output file
	f, err := os.Open(cmd.OutputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status.Error(codes.NotFound, "output file not found")
		}
		return status.Errorf(codes.Internal, "failed to open output: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.CommandOutputChunk{Data: buf[:n]}); sendErr != nil {
				return sendErr
			}
		}
		if err == io.EOF {
			if !req.Follow {
				return nil
			}
			// Check if command is still running
			cmd, err = s.daemon.queueManager.GetCommandStatus(req.CommandId)
			if err != nil || cmd.Status == "completed" || cmd.Status == "failed" {
				return nil
			}
			select {
			case <-stream.Context().Done():
				return stream.Context().Err()
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		if err != nil {
			return status.Errorf(codes.Internal, "read error: %v", err)
		}
	}
}

func (s *grpcServer) StreamQueueOutput(req *pb.StreamQueueOutputRequest, stream grpc.ServerStreamingServer[pb.QueueOutputChunk]) error {
	queueName := req.QueueName
	if queueName == "" {
		queueName = "default"
	}

	// Track which commands we've already fully streamed.
	streamed := make(map[string]bool)

	for {
		queue, commands, err := s.daemon.queueManager.GetQueueStatus(req.TaskId, queueName)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to get queue status: %v", err)
		}

		for _, cmd := range commands {
			if streamed[cmd.ID] {
				continue
			}
			if cmd.Status == "pending" {
				break // serial queue: nothing after this has started
			}

			isRunning := cmd.Status == "running"
			if err := s.streamCommandForQueue(cmd, req.Follow && isRunning, stream); err != nil {
				return err
			}
			if !isRunning {
				streamed[cmd.ID] = true
			}
		}

		if !req.Follow {
			return nil
		}

		// Check termination: queue stopped, or no pending/running commands left
		allDone := true
		for _, c := range commands {
			if c.Status == "pending" || c.Status == "running" {
				allDone = false
				break
			}
		}
		if allDone || queue.Status == "stopped" {
			return nil
		}

		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// streamCommandForQueue streams a single command's output file for the queue tail.
func (s *grpcServer) streamCommandForQueue(cmd *Command, follow bool, stream grpc.ServerStreamingServer[pb.QueueOutputChunk]) error {
	if cmd.OutputPath == "" {
		return nil
	}

	// Wait for the output file to appear. There's a race between the command
	// status being set to "running" and the goroutine creating the file.
	var f *os.File
	for {
		var err error
		f, err = os.Open(cmd.OutputPath)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return status.Errorf(codes.Internal, "failed to open output: %v", err)
		}
		if !follow {
			return nil // no file and not following — nothing to show
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer f.Close()

	// Send header chunk identifying the command
	cmdStr := strings.Join(cmd.Command, " ")
	header := fmt.Sprintf("\n=== %s: %s ===\n", cmd.ID, cmdStr)
	if err := stream.Send(&pb.QueueOutputChunk{Data: []byte(header), CommandId: cmd.ID}); err != nil {
		return err
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.QueueOutputChunk{Data: buf[:n]}); sendErr != nil {
				return sendErr
			}
		}
		if readErr == io.EOF {
			if !follow {
				return nil
			}
			// Check if command is still running
			updated, err := s.daemon.queueManager.GetCommandStatus(cmd.ID)
			if err != nil || updated.Status == "completed" || updated.Status == "failed" {
				return nil
			}
			select {
			case <-stream.Context().Done():
				return stream.Context().Err()
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		if readErr != nil {
			return status.Errorf(codes.Internal, "read error: %v", readErr)
		}
	}
}

func commandToProto(c *Command) *pb.CommandInfo {
	ci := &pb.CommandInfo{
		Id:            c.ID,
		QueueName:     c.QueueName,
		Command:       c.Command,
		Status:        c.Status,
		StopOnFailure: c.StopOnFailure,
		CreatedAt:     c.CreatedAt.Format(time.RFC3339),
	}
	if c.ExitCode != nil {
		ci.ExitCode = int32(*c.ExitCode)
	}
	if c.StartedAt != nil {
		ci.StartedAt = c.StartedAt.Format(time.RFC3339)
	}
	if c.FinishedAt != nil {
		ci.FinishedAt = c.FinishedAt.Format(time.RFC3339)
	}
	return ci
}

func taskToProto(t *Task) *pb.Task {
	pt := &pb.Task{
		Id:                t.ID,
		Name:              t.Name,
		Status:            t.Status,
		TailscaleHostname: t.TailscaleHostname,
		CreatedAt:         t.CreatedAt.Format(time.RFC3339),
	}
	if t.StoppedAt != nil {
		pt.StoppedAt = t.StoppedAt.Format(time.RFC3339)
	}
	return pt
}

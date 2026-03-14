// Package client provides a gRPC client for the stockyard daemon.
package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/obra/stockyard/pkg/api/v1"
)

// Client wraps the gRPC connection to the stockyard daemon.
type Client struct {
	conn   *grpc.ClientConn
	client pb.StockyardClient
}

// NewFromURL creates a new client connected to the daemon at the given URL.
// Supported URL formats:
//   - unix:///path/to/socket - Unix socket (local)
//   - grpc://host:port - TCP without TLS (remote)
//   - grpcs://host:port - TCP with TLS (remote)
//   - host:port - defaults to grpc://
func NewFromURL(url string) (*Client, error) {
	addr, useTLS, err := ParseURL(url)
	if err != nil {
		return nil, err
	}

	var opts []grpc.DialOption

	if useTLS {
		// TLS connection
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	} else {
		// No TLS
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewStockyardClient(conn),
	}, nil
}

// New creates a new client connected to the daemon at the given Unix socket path.
func New(socketPath string) (*Client, error) {
	return NewFromURL("unix://" + socketPath)
}

// NewWithDialer creates a new client with a custom dialer (useful for testing).
func NewWithDialer(target string, dialer func(ctx context.Context, addr string) (net.Conn, error)) (*Client, error) {
	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewStockyardClient(conn),
	}, nil
}

// Close closes the connection to the daemon.
func (c *Client) Close() error {
	return c.conn.Close()
}

// CreateTask creates a new task with the given request parameters.
func (c *Client) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.CreateTaskResponse, error) {
	return c.client.CreateTask(ctx, req)
}

// GetTask retrieves a task by its ID.
func (c *Client) GetTask(ctx context.Context, taskID string) (*pb.Task, error) {
	resp, err := c.client.GetTask(ctx, &pb.GetTaskRequest{TaskId: taskID})
	if err != nil {
		return nil, err
	}
	return resp.Task, nil
}

// ListTasks returns all tasks, optionally filtered by status.
func (c *Client) ListTasks(ctx context.Context, status string) ([]*pb.Task, error) {
	resp, err := c.client.ListTasks(ctx, &pb.ListTasksRequest{Status: status})
	if err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// StopTask stops a running task.
func (c *Client) StopTask(ctx context.Context, taskID string) error {
	_, err := c.client.StopTask(ctx, &pb.StopTaskRequest{TaskId: taskID})
	return err
}

// RestartTask restarts a stopped task.
func (c *Client) RestartTask(ctx context.Context, taskID string) error {
	_, err := c.client.RestartTask(ctx, &pb.RestartTaskRequest{TaskId: taskID})
	return err
}

// DestroyTask destroys a task and its associated resources.
func (c *Client) DestroyTask(ctx context.Context, taskID string) error {
	_, err := c.client.DestroyTask(ctx, &pb.DestroyTaskRequest{TaskId: taskID})
	return err
}

// CreateSnapshot creates a snapshot of a task's state.
func (c *Client) CreateSnapshot(ctx context.Context, taskID, label string) (string, error) {
	resp, err := c.client.CreateSnapshot(ctx, &pb.CreateSnapshotRequest{
		TaskId: taskID,
		Label:  label,
	})
	if err != nil {
		return "", err
	}
	return resp.SnapshotName, nil
}

// ListSnapshots returns all snapshots for a task.
func (c *Client) ListSnapshots(ctx context.Context, taskID string) ([]*pb.Snapshot, error) {
	resp, err := c.client.ListSnapshots(ctx, &pb.ListSnapshotsRequest{TaskId: taskID})
	if err != nil {
		return nil, err
	}
	return resp.Snapshots, nil
}

// RestoreSnapshot restores a task to a previous snapshot.
func (c *Client) RestoreSnapshot(ctx context.Context, taskID, snapshotName string) error {
	_, err := c.client.RestoreSnapshot(ctx, &pb.RestoreSnapshotRequest{
		TaskId:       taskID,
		SnapshotName: snapshotName,
	})
	return err
}

// GetLogs returns a stream of log entries for a task.
func (c *Client) GetLogs(ctx context.Context, taskID string, follow bool, tail int32) (pb.Stockyard_GetLogsClient, error) {
	return c.client.GetLogs(ctx, &pb.GetLogsRequest{
		TaskId: taskID,
		Follow: follow,
		Tail:   tail,
	})
}

// StreamLogs streams log entries to the given writer until the stream ends or context is cancelled.
func (c *Client) StreamLogs(ctx context.Context, taskID string, follow bool, tail int32, out io.Writer) error {
	stream, err := c.GetLogs(ctx, taskID, follow, tail)
	if err != nil {
		return err
	}

	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s %s\n", entry.Timestamp, entry.Line)
	}
}

// CreateQueue creates a new command queue for a task.
func (c *Client) CreateQueue(ctx context.Context, taskID, queueName, mode string) error {
	_, err := c.client.CreateQueue(ctx, &pb.CreateQueueRequest{
		TaskId:    taskID,
		QueueName: queueName,
		Mode:      mode,
	})
	return err
}

// ListQueues returns all queues for a task.
func (c *Client) ListQueues(ctx context.Context, taskID string) ([]*pb.QueueInfo, error) {
	resp, err := c.client.ListQueues(ctx, &pb.ListQueuesRequest{TaskId: taskID})
	if err != nil {
		return nil, err
	}
	return resp.Queues, nil
}

// GetQueueStatus returns info about a queue and its commands.
func (c *Client) GetQueueStatus(ctx context.Context, taskID, queueName string) (*pb.QueueInfo, []*pb.CommandInfo, error) {
	resp, err := c.client.GetQueueStatus(ctx, &pb.GetQueueStatusRequest{
		TaskId:    taskID,
		QueueName: queueName,
	})
	if err != nil {
		return nil, nil, err
	}
	return resp.Queue, resp.Commands, nil
}

// FlushQueue removes all pending commands from a queue.
func (c *Client) FlushQueue(ctx context.Context, taskID, queueName string) error {
	_, err := c.client.FlushQueue(ctx, &pb.FlushQueueRequest{
		TaskId:    taskID,
		QueueName: queueName,
	})
	return err
}

// ResumeQueue resumes a stopped serial queue.
func (c *Client) ResumeQueue(ctx context.Context, taskID, queueName string) error {
	_, err := c.client.ResumeQueue(ctx, &pb.ResumeQueueRequest{
		TaskId:    taskID,
		QueueName: queueName,
	})
	return err
}

// DestroyQueue destroys a queue and all its commands.
func (c *Client) DestroyQueue(ctx context.Context, taskID, queueName string) error {
	_, err := c.client.DestroyQueue(ctx, &pb.DestroyQueueRequest{
		TaskId:    taskID,
		QueueName: queueName,
	})
	return err
}

// QueueCommand enqueues a command for execution in a task's queue.
func (c *Client) QueueCommand(ctx context.Context, taskID, queueName string, command []string, env map[string]string, stopOnFailure bool) (string, error) {
	resp, err := c.client.QueueCommand(ctx, &pb.QueueCommandRequest{
		TaskId:        taskID,
		QueueName:     queueName,
		Command:       command,
		Env:           env,
		StopOnFailure: stopOnFailure,
	})
	if err != nil {
		return "", err
	}
	return resp.CommandId, nil
}

// GetCommandStatus returns the status of a specific command.
func (c *Client) GetCommandStatus(ctx context.Context, commandID string) (*pb.CommandInfo, error) {
	resp, err := c.client.GetCommandStatus(ctx, &pb.GetCommandStatusRequest{CommandId: commandID})
	if err != nil {
		return nil, err
	}
	return resp.Command, nil
}

// StreamQueueOutput streams output from all commands in a queue to the given writer.
func (c *Client) StreamQueueOutput(ctx context.Context, taskID, queueName string, follow bool, out io.Writer) error {
	stream, err := c.client.StreamQueueOutput(ctx, &pb.StreamQueueOutputRequest{
		TaskId:    taskID,
		QueueName: queueName,
		Follow:    follow,
	})
	if err != nil {
		return err
	}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		out.Write(chunk.Data)
	}
}

// StreamCommandOutput streams output from a command to the given writer.
func (c *Client) StreamCommandOutput(ctx context.Context, commandID string, follow bool, out io.Writer) error {
	stream, err := c.client.StreamCommandOutput(ctx, &pb.StreamCommandOutputRequest{
		CommandId: commandID,
		Follow:    follow,
	})
	if err != nil {
		return err
	}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		out.Write(chunk.Data)
	}
}

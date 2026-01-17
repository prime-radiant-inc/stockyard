// Package client provides a gRPC client for the stockyard daemon.
package client

import (
	"context"
	"fmt"
	"io"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/obra/stockyard/pkg/api/v1"
)

// Client wraps the gRPC connection to the stockyard daemon.
type Client struct {
	conn   *grpc.ClientConn
	client pb.StockyardClient
}

// New creates a new client connected to the daemon at the given Unix socket path.
func New(socketPath string) (*Client, error) {
	conn, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewStockyardClient(conn),
	}, nil
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

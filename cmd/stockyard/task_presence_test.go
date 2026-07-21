package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const taskPresenceBufSize = 1024 * 1024

type taskPresenceServer struct {
	pb.UnimplementedStockyardServer
	response *pb.GetTaskResponse
	err      error
}

func (s taskPresenceServer) GetTask(context.Context, *pb.GetTaskRequest) (*pb.GetTaskResponse, error) {
	return s.response, s.err
}

func newTaskPresenceClient(t *testing.T, server pb.StockyardServer) *client.Client {
	t.Helper()
	listener := bufconn.Listen(taskPresenceBufSize)
	grpcServer := grpc.NewServer()
	pb.RegisterStockyardServer(grpcServer, server)
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			t.Errorf("serve bufconn gRPC: %v", err)
		}
	}()
	t.Cleanup(grpcServer.Stop)

	c, err := client.NewWithDialer("passthrough:///bufnet", func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	})
	if err != nil {
		t.Fatalf("new bufconn client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func decodeSingleJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return err
	}
	return nil
}

func TestTaskPresenceMapsExactGetTaskOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		server  taskPresenceServer
		want    taskPresenceResult
		wantErr bool
	}{
		{
			name:   "ordinary row is present",
			server: taskPresenceServer{response: &pb.GetTaskResponse{Task: &pb.Task{Id: "t-123", Status: "running"}}},
			want:   taskPresenceResult{TaskID: "t-123", TaskPresence: taskPresencePresent},
		},
		{
			name:   "cleanup pending row is distinct",
			server: taskPresenceServer{response: &pb.GetTaskResponse{Task: &pb.Task{Id: "t-123", Status: "cleanup_pending"}}},
			want:   taskPresenceResult{TaskID: "t-123", TaskPresence: taskPresenceCleanupPending},
		},
		{
			name:   "not found is absent",
			server: taskPresenceServer{err: status.Error(codes.NotFound, "missing")},
			want:   taskPresenceResult{TaskID: "t-123", TaskPresence: taskPresenceAbsent},
		},
		{
			name:    "internal status is not absence",
			server:  taskPresenceServer{err: status.Error(codes.Internal, "broken")},
			wantErr: true,
		},
		{
			name:    "nil task response is not success",
			server:  taskPresenceServer{response: &pb.GetTaskResponse{}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			err := writeTaskPresence(context.Background(), newTaskPresenceClient(t, tt.server), "t-123", &output)
			if tt.wantErr {
				if err == nil {
					t.Fatal("writeTaskPresence succeeded")
				}
				if output.Len() != 0 {
					t.Fatalf("success JSON on error: %s", output.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("writeTaskPresence: %v", err)
			}
			var got taskPresenceResult
			if err := decodeSingleJSON(output.Bytes(), &got); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if got != tt.want {
				t.Fatalf("result = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestTaskPresenceCommandRequiresOneIDAndJSON(t *testing.T) {
	cmd := newTaskPresenceCommand(func() (*client.Client, error) {
		t.Fatal("client should not be created for invalid arguments")
		return nil, nil
	})
	cmd.SetArgs([]string{"t-123"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("task-presence accepted missing --json")
	}

	cmd = newTaskPresenceCommand(func() (*client.Client, error) {
		t.Fatal("client should not be created for invalid arguments")
		return nil, nil
	})
	cmd.SetArgs([]string{"t-123", "extra", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("task-presence accepted multiple task IDs")
	}
}

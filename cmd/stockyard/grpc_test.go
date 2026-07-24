package main

import (
	"context"
	"net"
	"testing"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const stockyardTestBufSize = 1024 * 1024

func newStockyardTestClient(t *testing.T, server pb.StockyardServer) *client.Client {
	t.Helper()
	listener := bufconn.Listen(stockyardTestBufSize)
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

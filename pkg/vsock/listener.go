// pkg/vsock/listener.go
package vsock

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/mdlayher/vsock"
)

const (
	// DefaultPort is the vsock port for snapshot service
	DefaultPort = 52000
)

// SnapshotHandler is called when a snapshot is requested
// vmID identifies which VM made the request
// label is the snapshot label from the VM
type SnapshotHandler func(vmID, label string) error

// SnapshotServer listens for snapshot requests from VMs
type SnapshotServer struct {
	handler        SnapshotHandler
	port           uint32
	UnixSocketPath string // Fallback for testing

	mu        sync.Mutex
	listeners []io.Closer
}

// NewSnapshotServer creates a new snapshot server
func NewSnapshotServer(handler SnapshotHandler) *SnapshotServer {
	return &SnapshotServer{
		handler: handler,
		port:    DefaultPort,
	}
}

// ListenVsock starts listening on vsock
func (s *SnapshotServer) ListenVsock(ctx context.Context) error {
	l, err := vsock.Listen(s.port, nil)
	if err != nil {
		return fmt.Errorf("vsock listen: %w", err)
	}

	s.mu.Lock()
	s.listeners = append(s.listeners, l)
	s.mu.Unlock()

	log.Printf("Snapshot server listening on vsock port %d", s.port)

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("vsock accept error: %v", err)
			continue
		}

		// Get VM ID from vsock connection
		vsockConn, ok := conn.(*vsock.Conn)
		vmID := "unknown"
		if ok {
			vmID = fmt.Sprintf("cid-%d", vsockConn.RemoteAddr().(*vsock.Addr).ContextID)
		}

		go s.handleConnection(conn, vmID)
	}
}

// ListenUnix starts listening on Unix socket (for testing)
func (s *SnapshotServer) ListenUnix(ctx context.Context) error {
	if s.UnixSocketPath == "" {
		s.UnixSocketPath = "/run/stockyard/snapshot.sock"
	}

	// Ensure directory exists
	if err := os.MkdirAll("/run/stockyard", 0755); err != nil {
		// Ignore error, might be testing
	}

	// Remove stale socket
	os.Remove(s.UnixSocketPath)

	l, err := net.Listen("unix", s.UnixSocketPath)
	if err != nil {
		return fmt.Errorf("unix listen: %w", err)
	}

	s.mu.Lock()
	s.listeners = append(s.listeners, l)
	s.mu.Unlock()

	log.Printf("Snapshot server listening on %s", s.UnixSocketPath)

	go func() {
		<-ctx.Done()
		l.Close()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("unix accept error: %v", err)
			continue
		}

		go s.handleConnection(conn, "unix-client")
	}
}

// Listen starts both vsock and unix listeners
func (s *SnapshotServer) Listen(ctx context.Context) error {
	errCh := make(chan error, 2)

	// Try vsock
	go func() {
		if err := s.ListenVsock(ctx); err != nil {
			log.Printf("vsock listener failed: %v (falling back to unix)", err)
		}
		errCh <- nil
	}()

	// Always start Unix as fallback
	go func() {
		errCh <- s.ListenUnix(ctx)
	}()

	// Wait for context or error
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *SnapshotServer) handleConnection(conn net.Conn, vmID string) {
	defer conn.Close()

	// Read request
	label, err := DecodeSnapshotRequest(conn)
	if err != nil {
		log.Printf("failed to decode request from %s: %v", vmID, err)
		EncodeSnapshotResponse(conn, false, err.Error())
		return
	}

	log.Printf("Snapshot request from %s: %q", vmID, label)

	// Call handler
	if err := s.handler(vmID, label); err != nil {
		log.Printf("snapshot handler error for %s: %v", vmID, err)
		EncodeSnapshotResponse(conn, false, err.Error())
		return
	}

	// Success
	EncodeSnapshotResponse(conn, true, "")
}

// Close closes all listeners
func (s *SnapshotServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, l := range s.listeners {
		l.Close()
	}
	s.listeners = nil
	return nil
}

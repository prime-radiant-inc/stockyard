// pkg/vsock/listener.go
package vsock

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
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
	if handler == nil {
		panic("vsock: handler cannot be nil")
	}
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
	dir := filepath.Dir(s.UnixSocketPath)
	if err := os.MkdirAll(dir, 0755); err != nil && !os.IsExist(err) {
		log.Printf("warning: could not create directory %s: %v", dir, err)
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // This will stop both listeners when we exit

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	// Try vsock
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.ListenVsock(ctx); err != nil {
			log.Printf("vsock listener failed: %v (falling back to unix)", err)
		}
	}()

	// Always start Unix as fallback
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.ListenUnix(ctx); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	// Wait for context cancellation or Unix listener error
	select {
	case <-ctx.Done():
		cancel()
		wg.Wait()
		return nil
	case err := <-errCh:
		cancel()
		wg.Wait()
		return err
	}
}

func (s *SnapshotServer) handleConnection(conn net.Conn, vmID string) {
	defer conn.Close()

	// Read request
	label, err := DecodeSnapshotRequest(conn)
	if err != nil {
		log.Printf("failed to decode request from %s: %v", vmID, err)
		if encErr := EncodeSnapshotResponse(conn, false, err.Error()); encErr != nil {
			log.Printf("failed to send error response to %s: %v", vmID, encErr)
		}
		return
	}

	log.Printf("Snapshot request from %s: %q", vmID, label)

	// Call handler
	if err := s.handler(vmID, label); err != nil {
		log.Printf("snapshot handler error for %s: %v", vmID, err)
		if encErr := EncodeSnapshotResponse(conn, false, err.Error()); encErr != nil {
			log.Printf("failed to send error response to %s: %v", vmID, encErr)
		}
		return
	}

	// Success
	if err := EncodeSnapshotResponse(conn, true, ""); err != nil {
		log.Printf("failed to send success response to %s: %v", vmID, err)
	}
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

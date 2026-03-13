// pkg/dashboard/vsock_session.go
package dashboard

import (
	"fmt"
	"net"
	"sync"

	"github.com/obra/stockyard/pkg/shell"
)

// VsockSession represents a terminal session over vsock.
type VsockSession struct {
	ID     string
	TaskID string
	CID    uint32
	User   string

	conn net.Conn

	mu     sync.Mutex
	closed bool
}

// SendOpen sends the Open message to start a shell session.
func (s *VsockSession) SendOpen(term string, cols, rows int, command []string, env map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("session closed")
	}

	msg := shell.OpenMessage{
		User:    s.User,
		Term:    term,
		Cols:    cols,
		Rows:    rows,
		Command: command,
		Env:     env,
	}
	payload, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("marshal open: %w", err)
	}
	return shell.WriteMessage(s.conn, shell.MsgOpen, payload)
}

// SendData sends terminal input to the VM.
func (s *VsockSession) SendData(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("session closed")
	}

	return shell.WriteMessage(s.conn, shell.MsgData, data)
}

// SendResize sends a terminal resize message.
func (s *VsockSession) SendResize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("session closed")
	}

	msg := shell.ResizeMessage{
		Cols: cols,
		Rows: rows,
	}
	payload, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("marshal resize: %w", err)
	}
	return shell.WriteMessage(s.conn, shell.MsgResize, payload)
}

// ReadMessage reads the next message from the VM.
// Returns message type and payload.
func (s *VsockSession) ReadMessage() (uint8, []byte, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, nil, fmt.Errorf("session closed")
	}
	// Get conn reference while holding lock, then release before blocking read
	conn := s.conn
	s.mu.Unlock()

	return shell.ReadMessage(conn)
}

// Close closes the vsock connection.
func (s *VsockSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// Conn returns the underlying connection (for deadline setting).
func (s *VsockSession) Conn() net.Conn {
	return s.conn
}

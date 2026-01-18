package dashboard

import (
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// TerminalSession represents an active terminal connection to a VM.
type TerminalSession struct {
	ID       string
	TaskID   string
	Hostname string
	User     string

	agentConn net.Conn
	client    *ssh.Client
	session   *ssh.Session
	stdin     io.WriteCloser
	stdout    io.Reader
	stderr    io.Reader

	mu     sync.Mutex
	closed bool
}

// TerminalInputMessage is sent from browser to daemon with user input.
type TerminalInputMessage struct {
	Type string `json:"type"` // "terminal_input"
	Data string `json:"data"` // Raw terminal input
}

// TerminalOutputMessage is sent from daemon to browser with terminal output.
type TerminalOutputMessage struct {
	Type string `json:"type"` // "terminal_output"
	Data string `json:"data"` // Raw terminal output
}

// TerminalResizeMessage is sent from browser to daemon when terminal resizes.
type TerminalResizeMessage struct {
	Type string `json:"type"` // "terminal_resize"
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// TerminalManager manages active terminal sessions.
type TerminalManager struct {
	sessions map[string]*TerminalSession
	mu       sync.RWMutex
}

// NewTerminalManager creates a new terminal session manager.
func NewTerminalManager() *TerminalManager {
	return &TerminalManager{
		sessions: make(map[string]*TerminalSession),
	}
}

// AddSession adds a terminal session.
func (tm *TerminalManager) AddSession(session *TerminalSession) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.sessions[session.ID] = session
}

// GetSession returns a session by ID.
func (tm *TerminalManager) GetSession(id string) *TerminalSession {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.sessions[id]
}

// RemoveSession removes a session by ID.
func (tm *TerminalManager) RemoveSession(id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.sessions, id)
}

// GetSessionsByTask returns all sessions for a task.
func (tm *TerminalManager) GetSessionsByTask(taskID string) []*TerminalSession {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var result []*TerminalSession
	for _, s := range tm.sessions {
		if s.TaskID == taskID {
			result = append(result, s)
		}
	}
	return result
}

// Resize changes the terminal size.
func (ts *TerminalSession) Resize(cols, rows int) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.session == nil {
		return fmt.Errorf("session not connected")
	}

	return ts.session.WindowChange(rows, cols)
}

// Close closes the terminal session.
func (ts *TerminalSession) Close() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.closed {
		return nil
	}
	ts.closed = true

	if ts.session != nil {
		ts.session.Close()
	}
	if ts.client != nil {
		ts.client.Close()
	}
	if ts.agentConn != nil {
		ts.agentConn.Close()
	}
	return nil
}

// CloseAllForTask closes all terminal sessions for a task.
func (tm *TerminalManager) CloseAllForTask(taskID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for id, s := range tm.sessions {
		if s.TaskID == taskID {
			s.Close()
			delete(tm.sessions, id)
		}
	}
}

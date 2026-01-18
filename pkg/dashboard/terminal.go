package dashboard

import (
	"sync"
)

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
	sessions map[string]*VsockSession
	mu       sync.RWMutex
}

// NewTerminalManager creates a new terminal session manager.
func NewTerminalManager() *TerminalManager {
	return &TerminalManager{
		sessions: make(map[string]*VsockSession),
	}
}

// AddSession adds a terminal session.
func (tm *TerminalManager) AddSession(session *VsockSession) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.sessions[session.ID] = session
}

// GetSession returns a session by ID.
func (tm *TerminalManager) GetSession(id string) *VsockSession {
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
func (tm *TerminalManager) GetSessionsByTask(taskID string) []*VsockSession {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var result []*VsockSession
	for _, s := range tm.sessions {
		if s.TaskID == taskID {
			result = append(result, s)
		}
	}
	return result
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

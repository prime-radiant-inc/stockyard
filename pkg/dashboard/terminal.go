package dashboard

import (
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// TerminalSession represents an active terminal connection to a VM.
type TerminalSession struct {
	ID       string
	TaskID   string
	Hostname string
	User     string

	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	stderr  io.Reader

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

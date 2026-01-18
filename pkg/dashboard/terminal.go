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

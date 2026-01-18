package shell

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"sync"

	"github.com/creack/pty"
)

// ValidateUser checks if a user exists on the system
func ValidateUser(username string) error {
	_, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user %q not found: %w", username, err)
	}
	return nil
}

// Session represents an active shell session
type Session struct {
	user    string
	cmd     *exec.Cmd
	ptyFile *os.File
	mu      sync.Mutex
	closed  bool
}

// NewSession creates a new shell session for the given user.
// Requires root privileges to use login -f.
// Sets TERM environment variable and initial window size.
func NewSession(username, term string, cols, rows int) (*Session, error) {
	if err := ValidateUser(username); err != nil {
		return nil, err
	}

	// Use login -f for a proper login shell with full environment setup
	// This handles PAM, sets up environment variables, etc.
	cmd := exec.Command("login", "-f", username)

	// Set TERM environment variable
	cmd.Env = append(os.Environ(), "TERM="+term)

	// Start with PTY
	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	return &Session{
		user:    username,
		cmd:     cmd,
		ptyFile: ptyFile,
	}, nil
}

// PTY returns the PTY file for I/O.
// The returned file becomes invalid after Close() is called.
// Callers doing I/O will receive EOF or errors after Close().
func (s *Session) PTY() *os.File {
	return s.ptyFile
}

// Resize changes the PTY window size
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("session closed")
	}

	return pty.Setsize(s.ptyFile, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

// Wait waits for the shell to exit and returns the exit code
func (s *Session) Wait() (int, error) {
	err := s.cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// Close terminates the session
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.ptyFile != nil {
		s.ptyFile.Close()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}

	return nil
}

//go:build darwin

package dashboard

import (
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// ContainerExecSession is a terminal session backed by `container exec` running
// under a host PTY. It bridges that PTY to the dashboard websocket.
type ContainerExecSession struct {
	ID     string
	TaskID string
	User   string

	cmd *exec.Cmd
	pty *os.File

	mu     sync.Mutex
	closed bool
}

// containerExecArgs builds the argv for `container exec` against a VM ID,
// running the shell as the given user.
func containerExecArgs(vmID, user, shell string) []string {
	return []string{"exec", "-t", "-i", "-u", user, "stockyard-" + vmID, shell}
}

// newContainerExecSession starts `container exec` for the given VM under a PTY,
// running the shell as the given user.
func newContainerExecSession(containerBin, vmID, user, shell string, cols, rows int) (*ContainerExecSession, error) {
	if shell == "" {
		shell = "/bin/bash"
	}
	return newContainerExecSessionWithCommand(containerBin, containerExecArgs(vmID, user, shell), cols, rows)
}

// newContainerExecSessionWithCommand starts an arbitrary command under a PTY.
// Exposed for tests (which substitute `cat` for `container`).
func newContainerExecSessionWithCommand(name string, args []string, cols, rows int) (*ContainerExecSession, error) {
	cmd := exec.Command(name, args...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}
	return &ContainerExecSession{cmd: cmd, pty: ptmx}, nil
}

// Read reads terminal output from the PTY.
func (s *ContainerExecSession) Read(p []byte) (int, error) {
	return s.pty.Read(p)
}

// Write writes terminal input to the PTY.
func (s *ContainerExecSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, fmt.Errorf("session closed")
	}
	return s.pty.Write(p)
}

// Resize sets the PTY window size.
func (s *ContainerExecSession) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session closed")
	}
	return pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close terminates the exec process and releases the PTY.
func (s *ContainerExecSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.pty.Close()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	go s.cmd.Wait() // reap without blocking
	return nil
}

package shell

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// Session represents an active shell session
type Session struct {
	user    string
	cmd     *exec.Cmd
	ptyFile *os.File
	mu      sync.Mutex
	closed  bool
}

// NewSession creates a new session that executes the given command.
// command is required; returns an error if nil or empty.
// env variables are merged on top of the system environment.
// When running as root and username is non-empty, drops privileges
// to that user via SysProcAttr.Credential.
func NewSession(username, term string, cols, rows int, command []string, env map[string]string) (*Session, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("command is required")
	}

	cmd := exec.Command(command[0], command[1:]...)

	// Drop privileges if running as root and username is specified
	if username != "" && os.Getuid() == 0 {
		u, err := user.Lookup(username)
		if err != nil {
			return nil, fmt.Errorf("lookup user %q: %w", username, err)
		}
		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse uid: %w", err)
		}
		gid, err := strconv.ParseUint(u.Gid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse gid: %w", err)
		}

		// Resolve supplementary groups so the child doesn't inherit root's groups
		gids, err := u.GroupIds()
		if err != nil {
			return nil, fmt.Errorf("lookup groups for %q: %w", username, err)
		}
		var groups []uint32
		for _, gidStr := range gids {
			g, err := strconv.ParseUint(gidStr, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("parse supplementary gid: %w", err)
			}
			groups = append(groups, uint32(g))
		}

		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid:    uint32(uid),
				Gid:    uint32(gid),
				Groups: groups,
			},
		}
		cmd.Dir = u.HomeDir

		// Build a clean environment instead of inheriting root's env.
		// This prevents LD_PRELOAD and other dangerous variables from leaking.
		// Caller-provided env is applied first so that mandatory values
		// (PATH, HOME, etc.) cannot be overridden.
		var cmdEnv []string
		for k, v := range env {
			cmdEnv = append(cmdEnv, k+"="+v)
		}
		cmdEnv = append(cmdEnv,
			"PATH=/usr/local/bin:/usr/bin:/bin",
			"TERM="+term,
			"HOME="+u.HomeDir,
			"USER="+username,
			"LOGNAME="+username,
		)
		cmd.Env = cmdEnv
	} else {
		// Not dropping privileges — use system env with overlay
		cmdEnv := os.Environ()
		cmdEnv = append(cmdEnv, "TERM="+term)
		for k, v := range env {
			cmdEnv = append(cmdEnv, k+"="+v)
		}
		cmd.Env = cmdEnv
	}

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

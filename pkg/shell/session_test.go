package shell

import (
	"os"
	"os/user"
	"strings"
	"testing"
)

func TestNewSessionRequiresCommand(t *testing.T) {
	_, err := NewSession("", "xterm", 80, 24, nil, nil)
	if err == nil {
		t.Error("expected error for nil command")
	}
	_, err = NewSession("", "xterm", 80, 24, []string{}, nil)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestNewSessionWithCommand(t *testing.T) {
	cmd := []string{"echo", "hello from stockyard"}
	session, err := NewSession("", "xterm", 80, 24, cmd, nil)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	buf := make([]byte, 4096)
	n, err := session.PTY().Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "hello from stockyard") {
		t.Errorf("output = %q, want to contain %q", output, "hello from stockyard")
	}

	exitCode, err := session.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
}

func TestNewSessionWithEnv(t *testing.T) {
	env := map[string]string{"STOCKYARD_TEST_VAR": "test_value_123"}
	cmd := []string{"sh", "-c", "echo $STOCKYARD_TEST_VAR"}
	session, err := NewSession("", "xterm", 80, 24, cmd, env)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer session.Close()

	buf := make([]byte, 4096)
	n, err := session.PTY().Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	output := string(buf[:n])
	if !strings.Contains(output, "test_value_123") {
		t.Errorf("output = %q, want to contain %q", output, "test_value_123")
	}
}

func TestNewSession_RequiresRoot(t *testing.T) {
	// Privilege dropping requires root
	if os.Getuid() != 0 {
		t.Skip("skipping: privilege dropping requires root")
	}

	u, err := user.Current()
	if err != nil {
		t.Fatalf("cannot get current user: %v", err)
	}

	session, err := NewSession(u.Username, "xterm", 80, 24, []string{"login", "-f", u.Username}, nil)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	if session.PTY() == nil {
		t.Error("session PTY is nil")
	}
}

func TestNewSession_InvalidUser(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping: privilege dropping requires root")
	}

	_, err := NewSession("nonexistent_user_xyz_12345", "xterm", 80, 24, []string{"echo", "test"}, nil)
	if err == nil {
		t.Error("NewSession should fail for nonexistent user")
	}
}

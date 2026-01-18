package shell

import (
	"os"
	"os/user"
	"testing"
)

func TestValidateUser_Root(t *testing.T) {
	err := ValidateUser("root")
	if err != nil {
		t.Errorf("ValidateUser(root) failed: %v", err)
	}
}

func TestValidateUser_InvalidUser(t *testing.T) {
	err := ValidateUser("nonexistent_user_xyz_12345")
	if err == nil {
		t.Error("ValidateUser should fail for nonexistent user")
	}
}

func TestNewSession_RequiresRoot(t *testing.T) {
	// login -f requires root privileges
	if os.Getuid() != 0 {
		t.Skip("skipping: login -f requires root")
	}

	u, err := user.Current()
	if err != nil {
		t.Fatalf("cannot get current user: %v", err)
	}

	session, err := NewSession(u.Username, "xterm", 80, 24)
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
		t.Skip("skipping: login -f requires root")
	}

	_, err := NewSession("nonexistent_user_xyz_12345", "xterm", 80, 24)
	if err == nil {
		t.Error("NewSession should fail for nonexistent user")
	}
}

//go:build darwin

package dashboard

import (
	"io"
	"strings"
	"testing"
)

func TestNewContainerExecSession_RunsCommand(t *testing.T) {
	// Use `cat` as a stand-in for `container exec`: it echoes stdin to stdout
	// under a PTY, which is enough to exercise Read/Write/Close plumbing.
	sess, err := newContainerExecSessionWithCommand("cat", nil, 80, 24)
	if err != nil {
		t.Fatalf("newContainerExecSessionWithCommand: %v", err)
	}
	defer sess.Close()

	if _, err := sess.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 64)
	n, err := sess.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "hello") {
		t.Errorf("expected echoed 'hello', got %q", string(buf[:n]))
	}
}

func TestContainerExecSession_BuildArgs(t *testing.T) {
	argv := containerExecArgs("abc12345", "/bin/bash")
	joined := strings.Join(argv, " ")
	for _, want := range []string{"exec", "-t", "-i", "stockyard-abc12345", "/bin/bash"} {
		if !strings.Contains(joined, want) {
			t.Errorf("containerExecArgs missing %q; got %v", want, argv)
		}
	}
}

func TestContainerExecSession_Resize(t *testing.T) {
	sess, err := newContainerExecSessionWithCommand("cat", nil, 80, 24)
	if err != nil {
		t.Fatalf("newContainerExecSessionWithCommand: %v", err)
	}
	defer sess.Close()
	if err := sess.Resize(120, 40); err != nil {
		t.Errorf("Resize: %v", err)
	}
}

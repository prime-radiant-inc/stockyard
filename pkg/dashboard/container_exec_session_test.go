//go:build darwin

package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
	argv := containerExecArgs("abc12345", "mooby", "/bin/bash")
	joined := strings.Join(argv, " ")
	for _, want := range []string{"exec", "-t", "-i", "-u", "mooby", "stockyard-abc12345", "/bin/bash"} {
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

// TestBridgeContainerSession_ChildExitsFirst verifies that bridgeContainerSession
// returns promptly when the child process exits before the websocket client
// disconnects. This exercises the I1/I2 fixes:
//
//   - I1: the reader goroutine calls conn.Close() on PTY EOF, unblocking
//     the foreground ReadMessage so the handler doesn't hang forever.
//   - I2: bridgeContainerSession calls session.Close() before <-done, so a
//     wedged reader goroutine is always interrupted.
func TestBridgeContainerSession_ChildExitsFirst(t *testing.T) {
	handler := &TerminalHandler{}

	// "true" exits immediately with no output — simulates the child dying first.
	sess, err := newContainerExecSessionWithCommand("true", nil, 80, 24)
	if err != nil {
		t.Fatalf("newContainerExecSessionWithCommand: %v", err)
	}

	bridgeDone := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		// bridgeContainerSession owns conn lifetime; do not defer conn.Close() here.
		handler.bridgeContainerSession(conn, sess)
		close(bridgeDone)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Keep the client connected so we know it's the child that exits first, not
	// the websocket going away. The bridge itself must close the connection.
	defer wsConn.Close()

	// The handler must return well within 2 s; 5 s is a generous safety margin.
	select {
	case <-bridgeDone:
		// pass
	case <-time.After(5 * time.Second):
		t.Fatal("bridgeContainerSession did not return after child process exited (goroutine leak)")
	}
}

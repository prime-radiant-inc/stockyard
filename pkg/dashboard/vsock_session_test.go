// pkg/dashboard/vsock_session_test.go
package dashboard

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/shell"
)

func TestVsockSession_SendOpen(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	session := &VsockSession{
		ID:     "test-session",
		TaskID: "task-123",
		CID:    42,
		User:   "mooby",
		conn:   client,
	}

	// Server reads open message
	done := make(chan error, 1)
	go func() {
		msgType, payload, err := shell.ReadMessage(server)
		if err != nil {
			done <- err
			return
		}
		if msgType != shell.MsgOpen {
			done <- fmt.Errorf("expected MsgOpen, got %d", msgType)
			return
		}
		var open shell.OpenMessage
		if err := open.Unmarshal(payload); err != nil {
			done <- err
			return
		}
		if open.User != "mooby" {
			done <- fmt.Errorf("got user %q, want mooby", open.User)
			return
		}
		if open.Term != "xterm-256color" {
			done <- fmt.Errorf("got term %q, want xterm-256color", open.Term)
			return
		}
		if open.Cols != 80 || open.Rows != 24 {
			done <- fmt.Errorf("got %dx%d, want 80x24", open.Cols, open.Rows)
			return
		}
		done <- nil
	}()

	// Send open message
	if err := session.SendOpen("xterm-256color", 80, 24, []string{"login", "-f", "mooby"}, nil); err != nil {
		t.Fatalf("SendOpen failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for open message")
	}
}

func TestVsockSession_SendData(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: client,
	}

	// Server reads data
	done := make(chan error, 1)
	go func() {
		msgType, payload, err := shell.ReadMessage(server)
		if err != nil {
			done <- err
			return
		}
		if msgType != shell.MsgData {
			done <- fmt.Errorf("expected MsgData, got %d", msgType)
			return
		}
		if string(payload) != "hello" {
			done <- fmt.Errorf("got %q, want hello", payload)
			return
		}
		done <- nil
	}()

	if err := session.SendData([]byte("hello")); err != nil {
		t.Fatalf("SendData failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestVsockSession_SendResize(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: client,
	}

	done := make(chan error, 1)
	go func() {
		msgType, payload, err := shell.ReadMessage(server)
		if err != nil {
			done <- err
			return
		}
		if msgType != shell.MsgResize {
			done <- fmt.Errorf("expected MsgResize, got %d", msgType)
			return
		}
		var resize shell.ResizeMessage
		if err := resize.Unmarshal(payload); err != nil {
			done <- err
			return
		}
		if resize.Cols != 120 || resize.Rows != 40 {
			done <- fmt.Errorf("got %dx%d, want 120x40", resize.Cols, resize.Rows)
			return
		}
		done <- nil
	}()

	if err := session.SendResize(120, 40); err != nil {
		t.Fatalf("SendResize failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestVsockSession_ReadMessage(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: client,
	}

	// Server sends data message
	go func() {
		shell.WriteMessage(server, shell.MsgData, []byte("output from vm"))
	}()

	msgType, payload, err := session.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msgType != shell.MsgData {
		t.Fatalf("expected MsgData, got %d", msgType)
	}
	if string(payload) != "output from vm" {
		t.Fatalf("got %q, want 'output from vm'", payload)
	}
}

func TestVsockSession_Close(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: client,
	}

	// Close should succeed
	if err := session.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Double close should be safe
	if err := session.Close(); err != nil {
		t.Fatalf("Double close failed: %v", err)
	}

	// Operations after close should fail
	if err := session.SendData([]byte("test")); err == nil {
		t.Fatal("expected error sending data after close")
	}
}

func TestVsockSession_Conn(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	session := &VsockSession{
		ID:   "test-session",
		conn: client,
	}

	if session.Conn() != client {
		t.Fatal("Conn() should return the underlying connection")
	}
}

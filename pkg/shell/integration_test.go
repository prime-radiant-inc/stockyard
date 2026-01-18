package shell

import (
	"net"
	"testing"
	"time"
)

func TestProtocol_OpenDataExit(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	serverDone := make(chan struct{})

	// Server goroutine (simulates stockyard-shell)
	go func() {
		defer close(serverDone)

		// Read open message
		msgType, payload, err := ReadMessage(server)
		if err != nil {
			t.Errorf("server read open: %v", err)
			return
		}
		if msgType != MsgOpen {
			t.Errorf("expected MsgOpen, got %d", msgType)
			return
		}

		var open OpenMessage
		if err := open.Unmarshal(payload); err != nil {
			t.Errorf("unmarshal open: %v", err)
			return
		}

		if open.User != "testuser" {
			t.Errorf("got user %q, want testuser", open.User)
		}
		if open.Term != "xterm-256color" {
			t.Errorf("got term %q, want xterm-256color", open.Term)
		}
		if open.Cols != 80 || open.Rows != 24 {
			t.Errorf("got size %dx%d, want 80x24", open.Cols, open.Rows)
		}

		// Send some data back
		if err := WriteMessage(server, MsgData, []byte("Welcome!")); err != nil {
			t.Errorf("write data: %v", err)
			return
		}

		// Read data from client
		msgType, payload, err = ReadMessage(server)
		if err != nil {
			t.Errorf("server read data: %v", err)
			return
		}
		if msgType != MsgData || string(payload) != "ls\n" {
			t.Errorf("expected Data 'ls\\n', got type=%d payload=%q", msgType, payload)
		}

		// Read resize
		msgType, payload, err = ReadMessage(server)
		if err != nil {
			t.Errorf("server read resize: %v", err)
			return
		}
		if msgType != MsgResize {
			t.Errorf("expected MsgResize, got %d", msgType)
		}
		var resize ResizeMessage
		resize.Unmarshal(payload)
		if resize.Cols != 120 || resize.Rows != 40 {
			t.Errorf("got resize %dx%d, want 120x40", resize.Cols, resize.Rows)
		}

		// Send exit
		exit := ExitMessage{Code: 0}
		payload, _ = exit.Marshal()
		WriteMessage(server, MsgExit, payload)
	}()

	// Client side (simulates host)
	open := OpenMessage{User: "testuser", Term: "xterm-256color", Cols: 80, Rows: 24}
	payload, _ := open.Marshal()
	if err := WriteMessage(client, MsgOpen, payload); err != nil {
		t.Fatalf("client write open: %v", err)
	}

	// Read welcome message
	client.SetReadDeadline(time.Now().Add(time.Second))
	msgType, payload, err := ReadMessage(client)
	if err != nil {
		t.Fatalf("client read data: %v", err)
	}
	if msgType != MsgData || string(payload) != "Welcome!" {
		t.Errorf("expected Data 'Welcome!', got type=%d payload=%q", msgType, payload)
	}

	// Send input
	if err := WriteMessage(client, MsgData, []byte("ls\n")); err != nil {
		t.Fatalf("client write data: %v", err)
	}

	// Send resize
	resize := ResizeMessage{Cols: 120, Rows: 40}
	payload, _ = resize.Marshal()
	if err := WriteMessage(client, MsgResize, payload); err != nil {
		t.Fatalf("client write resize: %v", err)
	}

	// Read exit
	msgType, payload, err = ReadMessage(client)
	if err != nil {
		t.Fatalf("client read exit: %v", err)
	}
	if msgType != MsgExit {
		t.Errorf("expected MsgExit, got %d", msgType)
	}
	var exit ExitMessage
	exit.Unmarshal(payload)
	if exit.Code != 0 {
		t.Errorf("got exit code %d, want 0", exit.Code)
	}

	<-serverDone
}

func TestProtocol_Error(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Server sends error
	go func() {
		errMsg := ErrorMessage{Error: "user not found"}
		payload, _ := errMsg.Marshal()
		WriteMessage(server, MsgError, payload)
	}()

	// Client reads error
	client.SetReadDeadline(time.Now().Add(time.Second))
	msgType, payload, err := ReadMessage(client)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	if msgType != MsgError {
		t.Errorf("expected MsgError, got %d", msgType)
	}

	var errMsg ErrorMessage
	errMsg.Unmarshal(payload)
	if errMsg.Error != "user not found" {
		t.Errorf("got error %q, want 'user not found'", errMsg.Error)
	}
}

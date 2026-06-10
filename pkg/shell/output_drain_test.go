package shell

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// eioReader simulates a PTY master that returns data alongside EIO on the
// final read — the exact behavior that causes output loss for fast commands.
type eioReader struct {
	data    []byte
	offset  int
	errSent bool
}

func (r *eioReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	// If we've consumed all data, return it WITH an error (like PTY EIO).
	if r.offset >= len(r.data) && !r.errSent {
		r.errSent = true
		return n, errors.New("input/output error") // simulates syscall.EIO
	}
	return n, nil
}

// TestDataNotLostOnSimultaneousReadError verifies that when a reader returns
// n > 0 alongside a non-nil error (as Linux PTY masters do with EIO), the
// data is still forwarded as a MsgData message.
//
// This is a regression test for: fast commands (e.g. "echo hello") losing
// output because the PTY→vsock goroutine checked err before processing n.
func TestDataNotLostOnSimultaneousReadError(t *testing.T) {
	want := "hello world\r\n"
	reader := &eioReader{data: []byte(want)}
	var conn bytes.Buffer

	// Simulate the PTY→vsock read loop (mirrors cmd/stockyard-shell/main.go).
	// This is the FIXED version: process n > 0 before checking err.
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if werr := WriteMessage(&conn, MsgData, buf[:n]); werr != nil {
				t.Fatalf("WriteMessage: %v", werr)
			}
		}
		if err != nil {
			break
		}
	}

	// Read back the message and verify the data survived.
	msgType, payload, err := ReadMessage(&conn)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgData {
		t.Fatalf("message type: got %d, want %d (MsgData)", msgType, MsgData)
	}
	if string(payload) != want {
		t.Errorf("payload: got %q, want %q", payload, want)
	}
}

// TestDataLostWithOldPattern demonstrates that the old code pattern (check
// err before n) drops data when the reader returns both.
func TestDataLostWithOldPattern(t *testing.T) {
	want := "hello world\r\n"
	reader := &eioReader{data: []byte(want)}
	var conn bytes.Buffer

	// OLD (broken) pattern: checks err before n.
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if err != nil {
			// BUG: drops buf[:n] here
			break
		}
		if n > 0 {
			if werr := WriteMessage(&conn, MsgData, buf[:n]); werr != nil {
				t.Fatalf("WriteMessage: %v", werr)
			}
		}
	}

	// With the old pattern, the buffer should be empty — data was dropped.
	if conn.Len() != 0 {
		t.Errorf("old pattern should have dropped data, but got %d bytes", conn.Len())
	}
}

package dashboard

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const containerBridgeWriteTimeout = 10 * time.Second

// containerExecSessionIface is the method set that bridgeContainerSession
// depends on. Both the darwin implementation and the non-darwin stub must
// satisfy this interface; the compile-time assertion below enforces it.
type containerExecSessionIface interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Close() error
}

// Compile-time assertion: *ContainerExecSession must implement the interface.
var _ containerExecSessionIface = (*ContainerExecSession)(nil)

// bridgeContainerSession pumps bytes between the websocket and a
// ContainerExecSession until either side closes.
//
// Lifecycle contract
//   - When the PTY exits first: session.Read returns EOF → the reader goroutine
//     returns → conn.Close() unblocks the foreground ReadMessage → the loop
//     exits → session.Close() is called (idempotent) → <-done completes.
//   - When the websocket disconnects first: ReadMessage returns an error → the
//     loop breaks → session.Close() closes the PTY fd → the reader goroutine's
//     blocked Read unblocks → the reader goroutine returns → <-done completes.
func (h *TerminalHandler) bridgeContainerSession(conn *websocket.Conn, session *ContainerExecSession) {
	done := make(chan struct{})

	// PTY output -> websocket (reader goroutine).
	//
	// I1: defer conn.Close() here so that when the child process exits and
	// Read returns EOF, the websocket connection is closed and the foreground
	// ReadMessage call unblocks immediately, preventing a handler leak.
	go func() {
		defer close(done)
		defer conn.Close() // I1: unblock foreground ReadMessage on PTY EOF
		buf := make([]byte, 4096)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				msg := TerminalOutputMessage{Type: "terminal_output", Data: string(buf[:n])}
				// M2: write deadline so a wedged client fails fast.
				conn.SetWriteDeadline(time.Now().Add(containerBridgeWriteTimeout))
				if werr := conn.WriteJSON(msg); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// websocket -> PTY input (foreground loop).
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var base struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &base); err != nil {
			continue
		}
		switch base.Type {
		case "terminal_input":
			var in TerminalInputMessage
			if err := json.Unmarshal(message, &in); err != nil {
				continue
			}
			if _, err := session.Write([]byte(in.Data)); err != nil {
				log.Printf("terminal: container write error: %v", err)
				break
			}
		case "terminal_resize":
			var rz TerminalResizeMessage
			if err := json.Unmarshal(message, &rz); err != nil {
				continue
			}
			if err := session.Resize(rz.Cols, rz.Rows); err != nil {
				log.Printf("terminal: container resize error: %v", err)
			}
		}
	}

	// I2: close the session *before* waiting for the reader goroutine.
	// This guarantees that any blocked Read (or WriteJSON) in the reader
	// goroutine will be interrupted promptly, so <-done never hangs.
	// session.Close() is idempotent — safe to call even though serveContainerExec
	// also defers it.
	session.Close()
	<-done
}

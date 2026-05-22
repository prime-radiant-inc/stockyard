package dashboard

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

// bridgeContainerSession pumps bytes between the websocket and a
// ContainerExecSession until either side closes.
func (h *TerminalHandler) bridgeContainerSession(conn *websocket.Conn, session *ContainerExecSession) {
	done := make(chan struct{})

	// PTY output -> websocket
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := session.Read(buf)
			if n > 0 {
				msg := TerminalOutputMessage{Type: "terminal_output", Data: string(buf[:n])}
				if werr := conn.WriteJSON(msg); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// websocket -> PTY input
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
	<-done
}

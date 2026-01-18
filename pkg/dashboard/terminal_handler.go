package dashboard

import (
	"net/http"

	"github.com/gorilla/websocket"
)

// TerminalHandler handles WebSocket connections for terminal sessions.
type TerminalHandler struct {
	manager     *TerminalManager
	defaultUser string
	upgrader    websocket.Upgrader
}

// NewTerminalHandler creates a new terminal WebSocket handler.
func NewTerminalHandler(manager *TerminalManager, defaultUser string) *TerminalHandler {
	return &TerminalHandler{
		manager:     manager,
		defaultUser: defaultUser,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// ServeHTTP handles WebSocket upgrade requests for terminal sessions.
// Currently a stub that closes the connection immediately.
func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.Close()
}

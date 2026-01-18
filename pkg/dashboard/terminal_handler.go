package dashboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/obra/stockyard/pkg/shell"
)

// TerminalHandler handles WebSocket connections for terminal sessions.
type TerminalHandler struct {
	manager     *TerminalManager
	daemon      DaemonAPI
	defaultUser string
	upgrader    websocket.Upgrader
}

// NewTerminalHandler creates a new terminal WebSocket handler.
func NewTerminalHandler(manager *TerminalManager, daemon DaemonAPI, defaultUser string) *TerminalHandler {
	return &TerminalHandler{
		manager:     manager,
		daemon:      daemon,
		defaultUser: defaultUser,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// ServeHTTP handles WebSocket upgrade requests for terminal sessions.
// URL format: /ws/terminal/{taskID}?user=<username>&cols=<cols>&rows=<rows>
func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract task ID from URL path
	taskID := extractTaskID(r.URL.Path)
	if taskID == "" {
		http.Error(w, "missing task ID", http.StatusBadRequest)
		return
	}

	// Get the user (from query param or default)
	user := r.URL.Query().Get("user")
	if user == "" {
		user = h.defaultUser
	}

	// Get initial terminal size from query params (with defaults)
	cols := 80
	rows := 24
	if c := r.URL.Query().Get("cols"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			cols = n
		}
	}
	if ro := r.URL.Query().Get("rows"); ro != "" {
		if n, err := strconv.Atoi(ro); err == nil && n > 0 {
			rows = n
		}
	}

	// Look up the task
	if h.daemon == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	task, err := h.daemon.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "failed to get task", http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	// Get the VM's vsock path for connection
	vsockPath, err := h.daemon.GetVsockPath(r.Context(), taskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("VM not available: %v", err), http.StatusServiceUnavailable)
		return
	}

	// Upgrade to WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("terminal: websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Create vsock session
	session, err := h.createVsockSession(vsockPath, user, cols, rows)
	if err != nil {
		h.sendError(conn, fmt.Sprintf("Failed to connect: %v", err))
		return
	}
	session.TaskID = taskID
	h.manager.AddSession(session)
	defer h.manager.RemoveSession(session.ID)
	defer session.Close()

	log.Printf("terminal: vsock session started for task %s (path %s, user %s, %dx%d)",
		taskID, vsockPath, user, cols, rows)

	// Handle bidirectional I/O
	h.handleVsockSession(conn, session)

	log.Printf("terminal: vsock session ended for task %s", taskID)
}

// extractTaskID extracts the task ID from a URL path like /ws/terminal/{taskID}
func extractTaskID(path string) string {
	// Remove leading slash and split
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")

	// Expected format: ws/terminal/{taskID}
	if len(parts) >= 3 && parts[0] == "ws" && parts[1] == "terminal" {
		return parts[2]
	}
	return ""
}

// createVsockSession creates a new vsock connection to the VM via Firecracker's UDS.
// Firecracker vsock uses a Unix socket with CONNECT protocol, not AF_VSOCK.
func (h *TerminalHandler) createVsockSession(vsockPath string, user string, cols, rows int) (*VsockSession, error) {
	if vsockPath == "" {
		return nil, fmt.Errorf("vsock path is empty")
	}

	// Connect to Firecracker's vsock UDS
	conn, err := net.Dial("unix", vsockPath)
	if err != nil {
		return nil, fmt.Errorf("dial vsock UDS %s: %w", vsockPath, err)
	}

	// Send CONNECT command with shell port
	connectCmd := fmt.Sprintf("CONNECT %d\n", shell.ShellPort)
	if _, err := conn.Write([]byte(connectCmd)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send CONNECT: %w", err)
	}

	// Read OK response (format: "OK <port>\n")
	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	if !strings.HasPrefix(response, "OK ") {
		conn.Close()
		return nil, fmt.Errorf("vsock CONNECT failed: %s", strings.TrimSpace(response))
	}

	session := &VsockSession{
		ID:   uuid.New().String(),
		User: user,
		conn: conn,
	}

	// Send Open message with terminal info
	if err := session.SendOpen("xterm-256color", cols, rows); err != nil {
		session.Close()
		return nil, fmt.Errorf("send open: %w", err)
	}

	return session, nil
}

// handleVsockSession manages bidirectional I/O between WebSocket and vsock.
func (h *TerminalHandler) handleVsockSession(conn *websocket.Conn, session *VsockSession) {
	done := make(chan struct{})

	// Read from vsock and send to WebSocket
	go func() {
		defer close(done)
		for {
			msgType, payload, err := session.ReadMessage()
			if err != nil {
				// Check if it's a normal close
				if !strings.Contains(err.Error(), "use of closed") {
					log.Printf("terminal: vsock read error: %v", err)
				}
				return
			}

			switch msgType {
			case shell.MsgData:
				msg := TerminalOutputMessage{
					Type: "terminal_output",
					Data: string(payload),
				}
				if err := conn.WriteJSON(msg); err != nil {
					log.Printf("terminal: websocket write error: %v", err)
					return
				}

			case shell.MsgExit:
				var exitMsg shell.ExitMessage
				exitMsg.Unmarshal(payload)
				log.Printf("terminal: shell exited with code %d", exitMsg.Code)
				// Send a message to indicate session ended
				msg := TerminalOutputMessage{
					Type: "terminal_output",
					Data: fmt.Sprintf("\r\n\033[33m[Session ended with code %d]\033[0m\r\n", exitMsg.Code),
				}
				conn.WriteJSON(msg)
				return

			case shell.MsgError:
				var errMsg shell.ErrorMessage
				errMsg.Unmarshal(payload)
				log.Printf("terminal: VM error: %s", errMsg.Error)
				msg := TerminalOutputMessage{
					Type: "terminal_output",
					Data: fmt.Sprintf("\r\n\033[31mError: %s\033[0m\r\n", errMsg.Error),
				}
				conn.WriteJSON(msg)
				return

			default:
				log.Printf("terminal: unknown message type from VM: %d", msgType)
			}
		}
	}()

	// Read from WebSocket and send to vsock
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("terminal: websocket read error: %v", err)
			}
			break
		}

		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &baseMsg); err != nil {
			log.Printf("terminal: invalid message format: %v", err)
			continue
		}

		switch baseMsg.Type {
		case "terminal_input":
			var inputMsg TerminalInputMessage
			if err := json.Unmarshal(message, &inputMsg); err != nil {
				log.Printf("terminal: invalid input message: %v", err)
				continue
			}
			if err := session.SendData([]byte(inputMsg.Data)); err != nil {
				log.Printf("terminal: vsock write error: %v", err)
				break
			}

		case "terminal_resize":
			var resizeMsg TerminalResizeMessage
			if err := json.Unmarshal(message, &resizeMsg); err != nil {
				log.Printf("terminal: invalid resize message: %v", err)
				continue
			}
			if err := session.SendResize(resizeMsg.Cols, resizeMsg.Rows); err != nil {
				log.Printf("terminal: resize error: %v", err)
			}

		default:
			log.Printf("terminal: unknown message type: %s", baseMsg.Type)
		}
	}

	// Wait for vsock reader goroutine to complete
	<-done
}

// sendError sends an error message to the terminal before closing.
func (h *TerminalHandler) sendError(conn *websocket.Conn, errMsg string) {
	msg := TerminalOutputMessage{
		Type: "terminal_output",
		Data: fmt.Sprintf("\r\n\033[31mError: %s\033[0m\r\n", errMsg),
	}
	conn.WriteJSON(msg)
}

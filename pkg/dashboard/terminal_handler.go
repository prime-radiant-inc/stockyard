package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/mdlayher/vsock"
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
// URL format: /ws/terminal/{taskID}?user=<username>
// The optional "user" query parameter overrides the default user (use "root" for root access).
func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract task ID from URL path
	taskID := extractTaskID(r.URL.Path)
	if taskID == "" {
		http.Error(w, "missing task ID", http.StatusBadRequest)
		return
	}

	// Get the SSH user (from query param or default)
	sshUser := r.URL.Query().Get("user")
	if sshUser == "" {
		sshUser = h.defaultUser
	}

	// Look up the task and its IP address
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

	// Get the VM's local IP address from DHCP
	vmIP, err := h.daemon.GetVMIP(r.Context(), taskID)
	if err != nil || vmIP == "" {
		http.Error(w, "VM IP not available (VM may still be starting)", http.StatusServiceUnavailable)
		return
	}

	// Upgrade to WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("terminal: websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Create SSH session using the VM's local IP
	session, err := h.createSSHSession(vmIP, sshUser)
	if err != nil {
		h.sendError(conn, fmt.Sprintf("Failed to connect: %v", err))
		return
	}
	session.TaskID = taskID
	defer session.Close()

	// Register session with manager
	h.manager.AddSession(session)
	defer h.manager.RemoveSession(session.ID)

	// Handle bidirectional I/O
	h.handleSession(conn, session)
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

// createSSHSession creates a new SSH connection to the specified host via local IP.
func (h *TerminalHandler) createSSHSession(hostname, user string) (*TerminalSession, error) {
	// Build SSH command with options for ephemeral VMs:
	// - StrictHostKeyChecking=no: Don't reject unknown hosts
	// - UserKnownHostsFile=/dev/null: Don't save host keys (ephemeral VMs)
	target := hostname
	if user != "" {
		target = user + "@" + hostname
	}

	cmd := exec.Command("ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		target,
	)

	// Start command with PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	// Set initial size
	pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	return &TerminalSession{
		ID:       uuid.New().String(),
		Hostname: hostname,
		User:     user,
		cmd:      cmd,
		pty:      ptmx,
	}, nil
}

// createVsockSession creates a new vsock connection to the VM.
func (h *TerminalHandler) createVsockSession(cid uint32, user string, cols, rows int) (*VsockSession, error) {
	if cid == 0 {
		return nil, fmt.Errorf("invalid CID: 0")
	}

	conn, err := vsock.Dial(cid, shell.ShellPort, nil)
	if err != nil {
		return nil, fmt.Errorf("vsock dial CID %d: %w", cid, err)
	}

	session := &VsockSession{
		ID:   uuid.New().String(),
		CID:  cid,
		User: user,
		conn: conn,
	}

	// Send Open message with terminal info
	if err := session.SendOpen("xterm-256color", cols, rows); err != nil {
		conn.Close()
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

// handleSession manages bidirectional I/O between WebSocket and PTY.
func (h *TerminalHandler) handleSession(conn *websocket.Conn, session *TerminalSession) {
	done := make(chan struct{})

	// Read from PTY and send to WebSocket
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := session.pty.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("terminal: pty read error: %v", err)
				}
				return
			}
			if n > 0 {
				msg := TerminalOutputMessage{
					Type: "terminal_output",
					Data: string(buf[:n]),
				}
				if err := conn.WriteJSON(msg); err != nil {
					log.Printf("terminal: websocket write error: %v", err)
					return
				}
			}
		}
	}()

	// Read from WebSocket and send to PTY
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("terminal: websocket read error: %v", err)
			}
			break
		}

		// Parse message to determine type
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
			if _, err := session.pty.Write([]byte(inputMsg.Data)); err != nil {
				log.Printf("terminal: pty write error: %v", err)
				break
			}

		case "terminal_resize":
			var resizeMsg TerminalResizeMessage
			if err := json.Unmarshal(message, &resizeMsg); err != nil {
				log.Printf("terminal: invalid resize message: %v", err)
				continue
			}
			if err := session.Resize(resizeMsg.Cols, resizeMsg.Rows); err != nil {
				log.Printf("terminal: resize error: %v", err)
			}

		default:
			log.Printf("terminal: unknown message type: %s", baseMsg.Type)
		}
	}

	// Wait for PTY reader goroutine to complete
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

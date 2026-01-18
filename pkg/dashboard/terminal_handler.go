package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
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
// URL format: /ws/terminal/{taskID}
func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract task ID from URL path
	taskID := extractTaskID(r.URL.Path)
	if taskID == "" {
		http.Error(w, "missing task ID", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("terminal: websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Build hostname from task ID
	hostname := fmt.Sprintf("stockyard-%s", taskID)

	// Create SSH session
	session, err := h.createSSHSession(hostname, h.defaultUser)
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

// createSSHSession creates a new SSH connection to the specified host.
func (h *TerminalHandler) createSSHSession(hostname, user string) (*TerminalSession, error) {
	// Get SSH agent auth
	authMethod, err := sshAgentAuth()
	if err != nil {
		return nil, fmt.Errorf("ssh agent auth: %w", err)
	}

	// Configure SSH client
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{authMethod},
		// Accept any host key (VMs are ephemeral)
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Connect to SSH server
	client, err := ssh.Dial("tcp", hostname+":22", config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}

	// Create SSH session
	sshSession, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("new session: %w", err)
	}

	// Request PTY with default size
	if err := sshSession.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		sshSession.Close()
		client.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	// Get stdin pipe
	stdin, err := sshSession.StdinPipe()
	if err != nil {
		sshSession.Close()
		client.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	// Get stdout pipe
	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		sshSession.Close()
		client.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Start shell
	if err := sshSession.Shell(); err != nil {
		sshSession.Close()
		client.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}

	return &TerminalSession{
		ID:       uuid.New().String(),
		Hostname: hostname,
		User:     user,
		client:   client,
		session:  sshSession,
		stdin:    stdin,
		stdout:   stdout,
	}, nil
}

// handleSession manages bidirectional I/O between WebSocket and SSH.
func (h *TerminalHandler) handleSession(conn *websocket.Conn, session *TerminalSession) {
	done := make(chan struct{})

	// Read from SSH stdout and send to WebSocket
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := session.stdout.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("terminal: stdout read error: %v", err)
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

	// Read from WebSocket and send to SSH stdin
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("terminal: websocket read error: %v", err)
			}
			return
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
			if _, err := session.stdin.Write([]byte(inputMsg.Data)); err != nil {
				log.Printf("terminal: stdin write error: %v", err)
				return
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
}

// sendError sends an error message to the terminal before closing.
func (h *TerminalHandler) sendError(conn *websocket.Conn, errMsg string) {
	msg := TerminalOutputMessage{
		Type: "terminal_output",
		Data: fmt.Sprintf("\r\n\033[31mError: %s\033[0m\r\n", errMsg),
	}
	conn.WriteJSON(msg)
}

// sshAgentAuth returns an SSH auth method using the SSH agent.
func sshAgentAuth() (ssh.AuthMethod, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("dial ssh agent: %w", err)
	}
	return ssh.PublicKeysCallback(agent.NewClient(conn).Signers), nil
}

# Dashboard Phase 4: Terminal Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add in-browser terminal access to VMs via xterm.js and WebSocket SSH proxy.

**Architecture:** SSH proxy in daemon connects to VMs via Tailscale hostname, pipes PTY I/O through WebSocket. xterm.js renders terminal in browser.

**Tech Stack:** xterm.js (CDN), golang.org/x/crypto/ssh, existing WebSocket hub

**Prerequisites:** Phase 1, 2, and 3 complete. VMs must have Tailscale SSH enabled.

---

## Task Overview

| Task | Description |
|------|-------------|
| 1 | Add SSH client library and terminal session struct |
| 2 | Add terminal WebSocket message types |
| 3 | Add terminal session manager |
| 4 | Add WebSocket terminal handler |
| 5 | Add xterm.js to VM detail page |
| 6 | Wire terminal UI to WebSocket |
| 7 | Add terminal resize support |
| 8 | Add terminal session cleanup |
| 9 | Final integration and testing |

---

## Task 1: Add SSH Client Library and Terminal Session Struct

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/terminal.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/terminal_test.go`

**Step 1: Write the failing test for TerminalSession struct**

```go
package dashboard

import (
	"testing"
)

func TestTerminalSession_Fields(t *testing.T) {
	session := &TerminalSession{
		ID:       "session-123",
		TaskID:   "task-456",
		Hostname: "stockyard-task-456",
		User:     "vscode",
	}

	if session.ID != "session-123" {
		t.Errorf("expected session-123, got %s", session.ID)
	}
	if session.TaskID != "task-456" {
		t.Errorf("expected task-456, got %s", session.TaskID)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./pkg/dashboard/... -run TestTerminalSession -v`
Expected: FAIL (TerminalSession not defined)

**Step 3: Write minimal implementation**

```go
package dashboard

import (
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// TerminalSession represents an active terminal connection to a VM.
type TerminalSession struct {
	ID       string
	TaskID   string
	Hostname string
	User     string

	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	stderr  io.Reader

	mu     sync.Mutex
	closed bool
}
```

**Step 4: Add go.mod dependency**

Run: `go get golang.org/x/crypto/ssh`

**Step 5: Run test to verify it passes**

Run: `go test ./pkg/dashboard/... -run TestTerminalSession -v`
Expected: PASS

**Step 6: Commit**

```bash
git add pkg/dashboard/terminal.go pkg/dashboard/terminal_test.go go.mod go.sum
git commit -m "feat(dashboard): add TerminalSession struct for SSH proxy"
```

---

## Task 2: Add Terminal WebSocket Message Types

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal_test.go`

**Step 1: Write test for message types**

```go
func TestTerminalMessage_Marshal(t *testing.T) {
	msg := TerminalInputMessage{
		Type: "terminal_input",
		Data: "ls -la\n",
	}

	if msg.Type != "terminal_input" {
		t.Errorf("expected terminal_input, got %s", msg.Type)
	}
}

func TestTerminalOutputMessage_Marshal(t *testing.T) {
	msg := TerminalOutputMessage{
		Type: "terminal_output",
		Data: "total 42\n",
	}

	if msg.Type != "terminal_output" {
		t.Errorf("expected terminal_output, got %s", msg.Type)
	}
}

func TestTerminalResizeMessage(t *testing.T) {
	msg := TerminalResizeMessage{
		Type: "terminal_resize",
		Cols: 120,
		Rows: 40,
	}

	if msg.Cols != 120 || msg.Rows != 40 {
		t.Errorf("expected 120x40, got %dx%d", msg.Cols, msg.Rows)
	}
}
```

**Step 2: Run tests to verify they fail**

**Step 3: Implement message types**

```go
// TerminalInputMessage is sent from browser to daemon with user input.
type TerminalInputMessage struct {
	Type string `json:"type"` // "terminal_input"
	Data string `json:"data"` // Raw terminal input
}

// TerminalOutputMessage is sent from daemon to browser with terminal output.
type TerminalOutputMessage struct {
	Type string `json:"type"` // "terminal_output"
	Data string `json:"data"` // Raw terminal output
}

// TerminalResizeMessage is sent from browser to daemon when terminal resizes.
type TerminalResizeMessage struct {
	Type string `json:"type"` // "terminal_resize"
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}
```

**Step 4: Run tests, commit**

```bash
git add pkg/dashboard/terminal.go pkg/dashboard/terminal_test.go
git commit -m "feat(dashboard): add terminal WebSocket message types"
```

---

## Task 3: Add Terminal Session Manager

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal_test.go`

**Step 1: Write tests for TerminalManager**

```go
func TestTerminalManager_CreateSession(t *testing.T) {
	tm := NewTerminalManager()

	// Create a mock session (can't actually connect in unit test)
	session := &TerminalSession{
		ID:       "test-session",
		TaskID:   "task-123",
		Hostname: "stockyard-task-123",
		User:     "vscode",
	}

	tm.AddSession(session)

	found := tm.GetSession("test-session")
	if found == nil {
		t.Fatal("expected to find session")
	}
	if found.TaskID != "task-123" {
		t.Errorf("expected task-123, got %s", found.TaskID)
	}
}

func TestTerminalManager_RemoveSession(t *testing.T) {
	tm := NewTerminalManager()

	session := &TerminalSession{
		ID:     "test-session",
		TaskID: "task-123",
	}
	tm.AddSession(session)
	tm.RemoveSession("test-session")

	if tm.GetSession("test-session") != nil {
		t.Error("expected session to be removed")
	}
}

func TestTerminalManager_GetSessionsByTask(t *testing.T) {
	tm := NewTerminalManager()

	tm.AddSession(&TerminalSession{ID: "s1", TaskID: "task-1"})
	tm.AddSession(&TerminalSession{ID: "s2", TaskID: "task-1"})
	tm.AddSession(&TerminalSession{ID: "s3", TaskID: "task-2"})

	sessions := tm.GetSessionsByTask("task-1")
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions for task-1, got %d", len(sessions))
	}
}
```

**Step 2: Run tests to verify they fail**

**Step 3: Implement TerminalManager**

```go
// TerminalManager manages active terminal sessions.
type TerminalManager struct {
	sessions map[string]*TerminalSession
	mu       sync.RWMutex
}

// NewTerminalManager creates a new terminal session manager.
func NewTerminalManager() *TerminalManager {
	return &TerminalManager{
		sessions: make(map[string]*TerminalSession),
	}
}

// AddSession adds a terminal session.
func (tm *TerminalManager) AddSession(session *TerminalSession) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.sessions[session.ID] = session
}

// GetSession returns a session by ID.
func (tm *TerminalManager) GetSession(id string) *TerminalSession {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.sessions[id]
}

// RemoveSession removes a session by ID.
func (tm *TerminalManager) RemoveSession(id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	delete(tm.sessions, id)
}

// GetSessionsByTask returns all sessions for a task.
func (tm *TerminalManager) GetSessionsByTask(taskID string) []*TerminalSession {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var result []*TerminalSession
	for _, s := range tm.sessions {
		if s.TaskID == taskID {
			result = append(result, s)
		}
	}
	return result
}
```

**Step 4: Run tests, commit**

```bash
git add pkg/dashboard/terminal.go pkg/dashboard/terminal_test.go
git commit -m "feat(dashboard): add TerminalManager for session tracking"
```

---

## Task 4: Add WebSocket Terminal Handler

**Files:**
- Create: `/home/jesse/git/stockyard/pkg/dashboard/terminal_handler.go`
- Create: `/home/jesse/git/stockyard/pkg/dashboard/terminal_handler_test.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server.go`

**Step 1: Write test for terminal handler creation**

```go
package dashboard

import (
	"testing"
)

func TestTerminalHandler_Creation(t *testing.T) {
	tm := NewTerminalManager()
	handler := NewTerminalHandler(tm, "vscode")

	if handler == nil {
		t.Fatal("expected handler to be created")
	}
	if handler.defaultUser != "vscode" {
		t.Errorf("expected vscode, got %s", handler.defaultUser)
	}
}
```

**Step 2: Implement TerminalHandler struct**

```go
package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
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
```

**Step 3: Add route to server.go**

In Server struct, add:
```go
terminalManager *TerminalManager
terminalHandler *TerminalHandler
```

In NewServer, add:
```go
terminalManager := NewTerminalManager()
s.terminalManager = terminalManager
s.terminalHandler = NewTerminalHandler(terminalManager, "vscode")
```

Add route:
```go
mux.HandleFunc("/ws/terminal/", s.terminalHandler.ServeHTTP)
```

**Step 4: Run tests, commit**

```bash
git add pkg/dashboard/terminal_handler.go pkg/dashboard/terminal_handler_test.go pkg/dashboard/server.go
git commit -m "feat(dashboard): add terminal WebSocket handler"
```

---

## Task 5: Add xterm.js to VM Detail Page

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Add xterm.js CDN links**

Add to `<head>`:
```html
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm@5.3.0/css/xterm.css">
<script src="https://cdn.jsdelivr.net/npm/xterm@5.3.0/lib/xterm.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/xterm-addon-web-links@0.9.0/lib/xterm-addon-web-links.min.js"></script>
```

**Step 2: Add terminal panel**

Add after logs panel:
```html
<!-- Terminal panel -->
<div class="bg-white rounded-lg border border-gray-200 p-4"
     x-data="{
         showTerminal: false,
         connected: false,
         connecting: false
     }">
    <div class="flex items-center justify-between mb-4">
        <h2 class="font-semibold text-gray-900">Terminal</h2>
        <div class="flex items-center gap-2">
            <span x-show="connected" class="text-xs text-green-600 flex items-center gap-1">
                <span class="w-2 h-2 bg-green-500 rounded-full"></span>
                Connected
            </span>
            <span x-show="connecting" class="text-xs text-yellow-600">
                Connecting...
            </span>
            <button @click="showTerminal = !showTerminal; if(showTerminal && !connected) $dispatch('connect-terminal')"
                    class="text-xs text-blue-600 hover:underline"
                    x-text="showTerminal ? 'Hide' : 'Open Terminal'">
            </button>
        </div>
    </div>

    <div x-show="showTerminal" x-transition class="rounded overflow-hidden">
        <div id="terminal-container" class="h-80 bg-black"></div>
    </div>
</div>
```

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): add xterm.js terminal panel to VM detail"
```

---

## Task 6: Wire Terminal UI to WebSocket

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/templates/vm_detail.html`

**Step 1: Add terminal initialization script**

```html
<script>
document.addEventListener('alpine:init', () => {
    Alpine.data('terminalPanel', () => ({
        terminal: null,
        fitAddon: null,
        ws: null,
        connected: false,
        connecting: false,
        showTerminal: false,

        init() {
            this.$watch('showTerminal', (show) => {
                if (show && !this.terminal) {
                    this.initTerminal();
                }
            });
        },

        initTerminal() {
            const container = document.getElementById('terminal-container');
            if (!container) return;

            this.terminal = new Terminal({
                cursorBlink: true,
                fontSize: 14,
                fontFamily: 'Menlo, Monaco, "Courier New", monospace',
                theme: {
                    background: '#1a1a1a',
                    foreground: '#f0f0f0',
                }
            });

            this.fitAddon = new FitAddon.FitAddon();
            this.terminal.loadAddon(this.fitAddon);
            this.terminal.loadAddon(new WebLinksAddon.WebLinksAddon());

            this.terminal.open(container);
            this.fitAddon.fit();

            this.terminal.onData((data) => {
                if (this.ws && this.ws.readyState === WebSocket.OPEN) {
                    this.ws.send(JSON.stringify({
                        type: 'terminal_input',
                        data: data
                    }));
                }
            });

            window.addEventListener('resize', () => {
                if (this.fitAddon) {
                    this.fitAddon.fit();
                    this.sendResize();
                }
            });

            this.connect();
        },

        connect() {
            this.connecting = true;
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            this.ws = new WebSocket(`${protocol}//${window.location.host}/ws/terminal/{{.Task.ID}}`);

            this.ws.onopen = () => {
                this.connected = true;
                this.connecting = false;
                this.terminal.focus();
                this.sendResize();
            };

            this.ws.onmessage = (e) => {
                const msg = JSON.parse(e.data);
                if (msg.type === 'terminal_output') {
                    this.terminal.write(msg.data);
                }
            };

            this.ws.onclose = () => {
                this.connected = false;
                this.connecting = false;
                this.terminal.write('\r\n\x1b[31mConnection closed\x1b[0m\r\n');
            };

            this.ws.onerror = () => {
                this.connected = false;
                this.connecting = false;
                this.terminal.write('\r\n\x1b[31mConnection error\x1b[0m\r\n');
            };
        },

        sendResize() {
            if (this.ws && this.ws.readyState === WebSocket.OPEN && this.terminal) {
                this.ws.send(JSON.stringify({
                    type: 'terminal_resize',
                    cols: this.terminal.cols,
                    rows: this.terminal.rows
                }));
            }
        },

        disconnect() {
            if (this.ws) {
                this.ws.close();
            }
        }
    }));
});
</script>
```

**Step 2: Update terminal panel to use Alpine component**

Update the terminal div to use `x-data="terminalPanel()"`.

**Step 3: Commit**

```bash
git add pkg/dashboard/templates/vm_detail.html
git commit -m "feat(dashboard): wire xterm.js to terminal WebSocket"
```

---

## Task 7: Add Terminal Resize Support

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal_handler.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal_handler_test.go`

**Step 1: Write test for resize handling**

```go
func TestTerminalSession_Resize(t *testing.T) {
	session := &TerminalSession{
		ID:     "test",
		TaskID: "task-1",
	}

	// Should not panic when session not connected
	err := session.Resize(120, 40)
	if err == nil {
		t.Error("expected error when session not connected")
	}
}
```

**Step 2: Implement resize method**

```go
// Resize changes the terminal size.
func (ts *TerminalSession) Resize(cols, rows int) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.session == nil {
		return fmt.Errorf("session not connected")
	}

	return ts.session.WindowChange(rows, cols)
}
```

**Step 3: Handle resize messages in WebSocket handler**

In the read loop, handle resize messages:
```go
case "terminal_resize":
    var resize TerminalResizeMessage
    if err := json.Unmarshal(message, &resize); err == nil {
        session.Resize(resize.Cols, resize.Rows)
    }
```

**Step 4: Run tests, commit**

```bash
git add pkg/dashboard/terminal_handler.go pkg/dashboard/terminal_handler_test.go
git commit -m "feat(dashboard): add terminal resize support"
```

---

## Task 8: Add Terminal Session Cleanup

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal_test.go`

**Step 1: Write test for session cleanup**

```go
func TestTerminalSession_Close(t *testing.T) {
	session := &TerminalSession{
		ID:     "test",
		TaskID: "task-1",
	}

	// Should not panic on double close
	session.Close()
	session.Close()

	if !session.closed {
		t.Error("expected session to be marked closed")
	}
}

func TestTerminalManager_CloseAllForTask(t *testing.T) {
	tm := NewTerminalManager()

	s1 := &TerminalSession{ID: "s1", TaskID: "task-1"}
	s2 := &TerminalSession{ID: "s2", TaskID: "task-1"}
	s3 := &TerminalSession{ID: "s3", TaskID: "task-2"}

	tm.AddSession(s1)
	tm.AddSession(s2)
	tm.AddSession(s3)

	tm.CloseAllForTask("task-1")

	if len(tm.GetSessionsByTask("task-1")) != 0 {
		t.Error("expected all task-1 sessions to be removed")
	}
	if len(tm.GetSessionsByTask("task-2")) != 1 {
		t.Error("expected task-2 session to remain")
	}
}
```

**Step 2: Implement Close and CloseAllForTask**

```go
// Close closes the terminal session.
func (ts *TerminalSession) Close() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.closed {
		return nil
	}
	ts.closed = true

	if ts.session != nil {
		ts.session.Close()
	}
	if ts.client != nil {
		ts.client.Close()
	}
	return nil
}

// CloseAllForTask closes all terminal sessions for a task.
func (tm *TerminalManager) CloseAllForTask(taskID string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for id, s := range tm.sessions {
		if s.TaskID == taskID {
			s.Close()
			delete(tm.sessions, id)
		}
	}
}
```

**Step 3: Run tests, commit**

```bash
git add pkg/dashboard/terminal.go pkg/dashboard/terminal_test.go
git commit -m "feat(dashboard): add terminal session cleanup"
```

---

## Task 9: Final Integration and Testing

**Files:**
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/terminal_handler.go`
- Modify: `/home/jesse/git/stockyard/pkg/dashboard/server.go`

**Step 1: Implement full SSH connection logic**

Complete the TerminalHandler.ServeHTTP method:

```go
func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract task ID from URL: /ws/terminal/{taskID}
	taskID := strings.TrimPrefix(r.URL.Path, "/ws/terminal/")
	if taskID == "" {
		http.Error(w, "task ID required", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Get task to find hostname
	// (This requires access to daemon, passed via closure or interface)
	hostname := "stockyard-" + taskID

	// Create SSH connection
	session, err := h.createSSHSession(hostname, h.defaultUser)
	if err != nil {
		conn.WriteJSON(TerminalOutputMessage{
			Type: "terminal_output",
			Data: fmt.Sprintf("Failed to connect: %v\r\n", err),
		})
		return
	}
	defer session.Close()

	h.manager.AddSession(session)
	defer h.manager.RemoveSession(session.ID)

	// Handle bidirectional I/O
	h.handleSession(conn, session)
}
```

**Step 2: Implement createSSHSession**

```go
func (h *TerminalHandler) createSSHSession(hostname, user string) (*TerminalSession, error) {
	// SSH config with agent auth
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeysCallback(sshAgentAuth),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: proper host key verification
	}

	client, err := ssh.Dial("tcp", hostname+":22", config)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	sshSession, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("new session: %w", err)
	}

	// Request PTY
	if err := sshSession.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{}); err != nil {
		sshSession.Close()
		client.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdin, _ := sshSession.StdinPipe()
	stdout, _ := sshSession.StdoutPipe()
	stderr, _ := sshSession.StderrPipe()

	if err := sshSession.Shell(); err != nil {
		sshSession.Close()
		client.Close()
		return nil, fmt.Errorf("shell: %w", err)
	}

	return &TerminalSession{
		ID:       uuid.New().String(),
		TaskID:   strings.TrimPrefix(hostname, "stockyard-"),
		Hostname: hostname,
		User:     user,
		client:   client,
		session:  sshSession,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
	}, nil
}
```

**Step 3: Run all tests**

Run: `go test ./pkg/dashboard/... -v`
Expected: PASS

**Step 4: Commit**

```bash
git add pkg/dashboard/terminal_handler.go pkg/dashboard/server.go
git commit -m "feat(dashboard): complete terminal SSH proxy integration"
```

---

## Summary

Phase 4 implementation adds:
- SSH proxy for in-browser terminal access
- xterm.js terminal emulator in VM detail page
- WebSocket-based terminal I/O
- Terminal resize support
- Session management and cleanup

After Phase 4, users can:
- Click "Open Terminal" on any running VM
- Get an interactive shell session in the browser
- Resize terminal window
- Copy/paste with standard shortcuts

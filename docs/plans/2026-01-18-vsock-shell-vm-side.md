# vsock Shell Service (VM Side) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create a vsock-based shell service that runs inside VMs, allowing the host to connect and get an interactive terminal without SSH.

**Architecture:** A Go binary (`stockyard-shell`) listens on vsock port 52, accepts connections with timeout, spawns a PTY with login shell for the requested user (with TERM and window size from Open message), and bridges I/O between vsock and PTY. Handles SIGTERM for graceful shutdown.

**Tech Stack:** Go, github.com/mdlayher/vsock, github.com/creack/pty, systemd

---

## Task 1: Create Protocol Package with Constants

**Files:**
- Create: `pkg/shell/protocol.go`
- Create: `pkg/shell/protocol_test.go`

**Step 1: Write the failing test for message encoding**

```go
// pkg/shell/protocol_test.go
package shell

import (
	"bytes"
	"testing"
)

func TestShellPort(t *testing.T) {
	if ShellPort != 52 {
		t.Errorf("ShellPort = %d, want 52", ShellPort)
	}
}

func TestWriteMessage_Data(t *testing.T) {
	var buf bytes.Buffer
	err := WriteMessage(&buf, MsgData, []byte("hello"))
	if err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	// Expected: type(1) + length(4) + payload
	expected := []byte{
		0x02,                   // MsgData
		0x00, 0x00, 0x00, 0x05, // length 5 (big-endian)
		'h', 'e', 'l', 'l', 'o',
	}

	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("got %v, want %v", buf.Bytes(), expected)
	}
}

func TestReadMessage_Data(t *testing.T) {
	data := []byte{
		0x02,                   // MsgData
		0x00, 0x00, 0x00, 0x05, // length 5
		'h', 'e', 'l', 'l', 'o',
	}

	r := bytes.NewReader(data)
	msgType, payload, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	if msgType != MsgData {
		t.Errorf("got type %d, want %d", msgType, MsgData)
	}
	if string(payload) != "hello" {
		t.Errorf("got payload %q, want %q", payload, "hello")
	}
}

func TestReadMessage_TooLarge(t *testing.T) {
	// Craft a message claiming 2MB payload
	data := []byte{
		0x02,                   // MsgData
		0x00, 0x20, 0x00, 0x00, // length 2MB (big-endian)
	}

	r := bytes.NewReader(data)
	_, _, err := ReadMessage(r)
	if err == nil {
		t.Error("expected error for oversized payload")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: FAIL (package doesn't exist)

**Step 3: Write implementation**

```go
// pkg/shell/protocol.go
package shell

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ShellPort is the vsock port for the shell service.
// Used by both stockyard-shell (VM) and dashboard (host).
const ShellPort = 52

// Message types for vsock shell protocol
const (
	MsgOpen   uint8 = 0x01 // Host -> VM: open session
	MsgData   uint8 = 0x02 // Bidirectional: terminal data
	MsgResize uint8 = 0x03 // Host -> VM: resize terminal
	MsgExit   uint8 = 0x04 // VM -> Host: shell exited
	MsgError  uint8 = 0x05 // VM -> Host: error occurred
)

// MaxPayloadSize limits message payload to 1MB
const MaxPayloadSize = 1024 * 1024

// WriteMessage writes a framed message: type(1) + length(4) + payload
func WriteMessage(w io.Writer, msgType uint8, payload []byte) error {
	// Write type
	if _, err := w.Write([]byte{msgType}); err != nil {
		return fmt.Errorf("write type: %w", err)
	}

	// Write length (big-endian)
	length := uint32(len(payload))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write length: %w", err)
	}

	// Write payload
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write payload: %w", err)
		}
	}

	return nil
}

// ReadMessage reads a framed message, returns type and payload
func ReadMessage(r io.Reader) (uint8, []byte, error) {
	// Read type
	typeBuf := make([]byte, 1)
	if _, err := io.ReadFull(r, typeBuf); err != nil {
		return 0, nil, fmt.Errorf("read type: %w", err)
	}
	msgType := typeBuf[0]

	// Read length
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return 0, nil, fmt.Errorf("read length: %w", err)
	}

	if length > MaxPayloadSize {
		return 0, nil, fmt.Errorf("payload too large: %d > %d", length, MaxPayloadSize)
	}

	// Read payload
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, fmt.Errorf("read payload: %w", err)
		}
	}

	return msgType, payload, nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: PASS

**Step 5: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add pkg/shell/protocol.go pkg/shell/protocol_test.go
git commit -m "feat(shell): add vsock shell protocol with shared port constant"
```

---

## Task 2: Add JSON Message Types with TERM and Size

**Files:**
- Modify: `pkg/shell/protocol.go`
- Modify: `pkg/shell/protocol_test.go`

**Step 1: Write failing test for JSON helpers**

```go
// Add to pkg/shell/protocol_test.go

func TestOpenMessage_Marshal(t *testing.T) {
	msg := OpenMessage{User: "mooby", Term: "xterm-256color", Cols: 80, Rows: 24}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	// Verify it's valid JSON with expected fields
	var decoded OpenMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.User != "mooby" || decoded.Term != "xterm-256color" || decoded.Cols != 80 || decoded.Rows != 24 {
		t.Errorf("round-trip failed: got %+v", decoded)
	}
}

func TestOpenMessage_Unmarshal(t *testing.T) {
	var msg OpenMessage
	err := msg.Unmarshal([]byte(`{"user":"root","term":"xterm","cols":120,"rows":40}`))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if msg.User != "root" {
		t.Errorf("got user %q, want %q", msg.User, "root")
	}
	if msg.Term != "xterm" {
		t.Errorf("got term %q, want %q", msg.Term, "xterm")
	}
	if msg.Cols != 120 || msg.Rows != 40 {
		t.Errorf("got size %dx%d, want 120x40", msg.Cols, msg.Rows)
	}
}

func TestResizeMessage(t *testing.T) {
	msg := ResizeMessage{Cols: 120, Rows: 40}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	var decoded ResizeMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatal(err)
	}

	if decoded.Cols != 120 || decoded.Rows != 40 {
		t.Errorf("got %dx%d, want 120x40", decoded.Cols, decoded.Rows)
	}
}

func TestExitMessage(t *testing.T) {
	msg := ExitMessage{Code: 1}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	var decoded ExitMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatal(err)
	}

	if decoded.Code != 1 {
		t.Errorf("got code %d, want 1", decoded.Code)
	}
}

func TestErrorMessage(t *testing.T) {
	msg := ErrorMessage{Error: "user not found"}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	var decoded ErrorMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatal(err)
	}

	if decoded.Error != "user not found" {
		t.Errorf("got error %q, want %q", decoded.Error, "user not found")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: FAIL (types not defined)

**Step 3: Add JSON message types to protocol.go**

```go
// Add to pkg/shell/protocol.go after the existing code
// Also add "encoding/json" to imports

// OpenMessage requests a shell session for a user.
// Includes terminal type and initial window size.
type OpenMessage struct {
	User string `json:"user"`
	Term string `json:"term"` // e.g., "xterm-256color"
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (m *OpenMessage) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func (m *OpenMessage) Unmarshal(data []byte) error {
	return json.Unmarshal(data, m)
}

// ResizeMessage requests terminal resize
type ResizeMessage struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (m *ResizeMessage) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func (m *ResizeMessage) Unmarshal(data []byte) error {
	return json.Unmarshal(data, m)
}

// ExitMessage indicates shell has exited
type ExitMessage struct {
	Code int `json:"code"`
}

func (m *ExitMessage) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func (m *ExitMessage) Unmarshal(data []byte) error {
	return json.Unmarshal(data, m)
}

// ErrorMessage indicates an error
type ErrorMessage struct {
	Error string `json:"error"`
}

func (m *ErrorMessage) Marshal() ([]byte, error) {
	return json.Marshal(m)
}

func (m *ErrorMessage) Unmarshal(data []byte) error {
	return json.Unmarshal(data, m)
}
```

**Step 4: Run test to verify it passes**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: PASS

**Step 5: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add pkg/shell/protocol.go pkg/shell/protocol_test.go
git commit -m "feat(shell): add JSON message types with TERM and window size"
```

---

## Task 3: Create Session with PTY (Root-Aware Tests)

**Files:**
- Create: `pkg/shell/session.go`
- Create: `pkg/shell/session_test.go`

**Step 1: Write tests that skip appropriately on non-root**

```go
// pkg/shell/session_test.go
package shell

import (
	"os"
	"os/user"
	"testing"
)

func TestValidateUser_Root(t *testing.T) {
	err := ValidateUser("root")
	if err != nil {
		t.Errorf("ValidateUser(root) failed: %v", err)
	}
}

func TestValidateUser_InvalidUser(t *testing.T) {
	err := ValidateUser("nonexistent_user_xyz_12345")
	if err == nil {
		t.Error("ValidateUser should fail for nonexistent user")
	}
}

func TestNewSession_RequiresRoot(t *testing.T) {
	// login -f requires root privileges
	if os.Getuid() != 0 {
		t.Skip("skipping: login -f requires root")
	}

	u, err := user.Current()
	if err != nil {
		t.Fatalf("cannot get current user: %v", err)
	}

	session, err := NewSession(u.Username, "xterm", 80, 24)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	if session.PTY() == nil {
		t.Error("session PTY is nil")
	}
}

func TestNewSession_InvalidUser(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping: login -f requires root")
	}

	_, err := NewSession("nonexistent_user_xyz_12345", "xterm", 80, 24)
	if err == nil {
		t.Error("NewSession should fail for nonexistent user")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: FAIL (ValidateUser not defined)

**Step 3: Implement Session**

```go
// pkg/shell/session.go
package shell

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"sync"

	"github.com/creack/pty"
)

// ValidateUser checks if a user exists on the system
func ValidateUser(username string) error {
	_, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user %q not found: %w", username, err)
	}
	return nil
}

// Session represents an active shell session
type Session struct {
	user    string
	cmd     *exec.Cmd
	ptyFile *os.File
	mu      sync.Mutex
	closed  bool
}

// NewSession creates a new shell session for the given user.
// Requires root privileges to use login -f.
// Sets TERM environment variable and initial window size.
func NewSession(username, term string, cols, rows int) (*Session, error) {
	if err := ValidateUser(username); err != nil {
		return nil, err
	}

	// Use login -f for a proper login shell with full environment setup
	// This handles PAM, sets up environment variables, etc.
	cmd := exec.Command("login", "-f", username)

	// Set TERM environment variable
	cmd.Env = append(os.Environ(), "TERM="+term)

	// Start with PTY
	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	return &Session{
		user:    username,
		cmd:     cmd,
		ptyFile: ptyFile,
	}, nil
}

// PTY returns the PTY file for I/O
func (s *Session) PTY() *os.File {
	return s.ptyFile
}

// Resize changes the PTY window size
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("session closed")
	}

	return pty.Setsize(s.ptyFile, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
}

// Wait waits for the shell to exit and returns the exit code
func (s *Session) Wait() (int, error) {
	err := s.cmd.Wait()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// Close terminates the session
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.ptyFile != nil {
		s.ptyFile.Close()
	}

	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}

	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: PASS (session tests skip on non-root)

**Step 5: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add pkg/shell/session.go pkg/shell/session_test.go
git commit -m "feat(shell): add session with PTY, TERM support, and root-aware tests"
```

---

## Task 4: Create stockyard-shell Binary with Timeouts and Signal Handling

**Files:**
- Create: `cmd/stockyard-shell/main.go`

**Step 1: Create the main binary**

```go
// cmd/stockyard-shell/main.go
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mdlayher/vsock"
	"github.com/obra/stockyard/pkg/shell"
)

const (
	openTimeout     = 5 * time.Second
	shutdownTimeout = 5 * time.Second
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("stockyard-shell starting on vsock port %d", shell.ShellPort)

	listener, err := vsock.Listen(shell.ShellPort, nil)
	if err != nil {
		log.Fatalf("Failed to listen on vsock port %d: %v", shell.ShellPort, err)
	}

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	var wg sync.WaitGroup

	// Handle shutdown signal
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down...", sig)
		cancel()
		listener.Close()

		// Wait for existing sessions with timeout
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			log.Printf("All sessions closed")
		case <-time.After(shutdownTimeout):
			log.Printf("Shutdown timeout, forcing exit")
		}
		os.Exit(0)
	}()

	log.Printf("Listening for connections...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			handleConnection(ctx, conn)
		}()
	}
}

func handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	log.Printf("New connection from %s", remoteAddr)

	// Set deadline for Open message
	conn.SetReadDeadline(time.Now().Add(openTimeout))

	// Read Open message
	msgType, payload, err := shell.ReadMessage(conn)
	if err != nil {
		log.Printf("Failed to read open message: %v", err)
		return // Don't send error for timeout - just close
	}

	// Clear the deadline for subsequent reads
	conn.SetReadDeadline(time.Time{})

	if msgType != shell.MsgOpen {
		log.Printf("Expected Open message, got type %d", msgType)
		sendError(conn, "expected Open message")
		return
	}

	var openMsg shell.OpenMessage
	if err := openMsg.Unmarshal(payload); err != nil {
		log.Printf("Failed to parse open message: %v", err)
		sendError(conn, "invalid open message")
		return
	}

	// Validate required fields
	if openMsg.User == "" {
		sendError(conn, "user is required")
		return
	}
	if openMsg.Cols <= 0 {
		openMsg.Cols = 80
	}
	if openMsg.Rows <= 0 {
		openMsg.Rows = 24
	}
	if openMsg.Term == "" {
		openMsg.Term = "xterm"
	}

	log.Printf("Opening shell for user %q (term=%s, size=%dx%d)",
		openMsg.User, openMsg.Term, openMsg.Cols, openMsg.Rows)

	// Create session
	session, err := shell.NewSession(openMsg.User, openMsg.Term, openMsg.Cols, openMsg.Rows)
	if err != nil {
		log.Printf("Failed to create session: %v", err)
		sendError(conn, fmt.Sprintf("failed to create session: %v", err))
		return
	}
	defer session.Close()

	// Create a context for this connection that cancels on parent cancel or connection close
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	// Bridge I/O between vsock and PTY
	var wg sync.WaitGroup

	// PTY -> vsock (stdout)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer connCancel()
		buf := make([]byte, 4096)
		for {
			n, err := session.PTY().Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("PTY read error: %v", err)
				}
				return
			}
			if n > 0 {
				if err := shell.WriteMessage(conn, shell.MsgData, buf[:n]); err != nil {
					log.Printf("vsock write error: %v", err)
					return
				}
			}
		}
	}()

	// vsock -> PTY (stdin) + handle control messages
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer connCancel()
		for {
			select {
			case <-connCtx.Done():
				return
			default:
			}

			msgType, payload, err := shell.ReadMessage(conn)
			if err != nil {
				if err != io.EOF {
					log.Printf("vsock read error: %v", err)
				}
				return
			}

			switch msgType {
			case shell.MsgData:
				if _, err := session.PTY().Write(payload); err != nil {
					log.Printf("PTY write error: %v", err)
					return
				}

			case shell.MsgResize:
				var resize shell.ResizeMessage
				if err := resize.Unmarshal(payload); err != nil {
					log.Printf("Invalid resize message: %v", err)
					continue
				}
				if err := session.Resize(resize.Cols, resize.Rows); err != nil {
					log.Printf("Resize error: %v", err)
				}

			default:
				log.Printf("Unknown message type: %d", msgType)
			}
		}
	}()

	// Wait for shell to exit
	exitCode, err := session.Wait()
	if err != nil {
		log.Printf("Wait error: %v", err)
		exitCode = 1
	}

	log.Printf("Shell exited with code %d", exitCode)

	// Send exit message (best effort)
	exitMsg := shell.ExitMessage{Code: exitCode}
	exitPayload, _ := exitMsg.Marshal()
	shell.WriteMessage(conn, shell.MsgExit, exitPayload)

	// Cancel to stop I/O goroutines
	connCancel()
	wg.Wait()
}

func sendError(conn net.Conn, message string) {
	errMsg := shell.ErrorMessage{Error: message}
	payload, _ := errMsg.Marshal()
	shell.WriteMessage(conn, shell.MsgError, payload)
}
```

**Step 2: Build and verify it compiles**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go build -o bin/stockyard-shell ./cmd/stockyard-shell`
Expected: Binary created at bin/stockyard-shell

**Step 3: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add cmd/stockyard-shell/main.go
git commit -m "feat(shell): add stockyard-shell binary with timeout and signal handling"
```

---

## Task 5: Add Protocol Integration Test

**Files:**
- Create: `pkg/shell/integration_test.go`

**Step 1: Write integration test using net.Pipe**

```go
// pkg/shell/integration_test.go
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
```

**Step 2: Run test**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: PASS

**Step 3: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add pkg/shell/integration_test.go
git commit -m "test(shell): add protocol integration tests"
```

---

## Task 6: Add systemd Service File

**Files:**
- Create: `vm-image/init/stockyard-shell.service`

**Step 1: Create systemd service file**

```ini
# vm-image/init/stockyard-shell.service
[Unit]
Description=Stockyard Shell Service (vsock terminal access)
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/stockyard-shell
Restart=always
RestartSec=1
StandardOutput=journal
StandardError=journal

# Graceful shutdown
TimeoutStopSec=5
KillMode=mixed
KillSignal=SIGTERM

[Install]
WantedBy=multi-user.target
```

**Step 2: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add vm-image/init/stockyard-shell.service
git commit -m "feat(vm-image): add systemd service for stockyard-shell (runs as root)"
```

---

## Task 7: Update VM Image Dockerfile

**Files:**
- Modify: `vm-image/Dockerfile`

**Step 1: Read current Dockerfile to find where to add stockyard-shell**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && grep -n "stockyard-snapshot" vm-image/Dockerfile`

**Step 2: Add stockyard-shell after stockyard-snapshot section**

Add these lines after the stockyard-snapshot COPY (around line 145):

```dockerfile
# Copy stockyard-shell binary for vsock terminal access
COPY scripts/stockyard-shell/stockyard-shell /usr/local/bin/stockyard-shell
RUN chmod +x /usr/local/bin/stockyard-shell

# Add the systemd service for stockyard-shell
COPY init/stockyard-shell.service /etc/systemd/system/stockyard-shell.service
RUN systemctl enable stockyard-shell.service 2>/dev/null || true
```

**Step 3: Create placeholder directory**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
mkdir -p vm-image/scripts/stockyard-shell
touch vm-image/scripts/stockyard-shell/.gitkeep
```

**Step 4: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add vm-image/Dockerfile vm-image/scripts/stockyard-shell/.gitkeep
git commit -m "feat(vm-image): integrate stockyard-shell into Docker build"
```

---

## Task 8: Update Makefiles for Build Dependencies

**Files:**
- Modify: `Makefile`
- Modify: `vm-image/Makefile`

**Step 1: Add build-shell target to root Makefile**

Add after existing build targets:

```makefile
# Build stockyard-shell for VM (static binary for Linux)
build-shell:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o vm-image/scripts/stockyard-shell/stockyard-shell ./cmd/stockyard-shell

.PHONY: build-shell
```

**Step 2: Read vm-image/Makefile to understand current structure**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && cat vm-image/Makefile`

**Step 3: Update vm-image/Makefile to depend on stockyard-shell**

The vm-image Makefile should call the parent Makefile to build the shell binary before running docker build. Add a dependency:

```makefile
# At the top, add a target to build the shell binary
../vm-image/scripts/stockyard-shell/stockyard-shell:
	$(MAKE) -C .. build-shell

# Make the docker build depend on it (add to existing build target prerequisites)
build: ../vm-image/scripts/stockyard-shell/stockyard-shell
```

Or if simpler, just add to the all target a call to build the shell first:

```makefile
all: build-deps build

build-deps:
	$(MAKE) -C .. build-shell
```

**Step 4: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add Makefile vm-image/Makefile
git commit -m "build: add build-shell target and vm-image dependency"
```

---

## Task 9: Final Build and Test

**Step 1: Run all tests**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
go test ./... -v
```
Expected: All pass (session tests skip on non-root)

**Step 2: Build stockyard-shell binary**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
make build-shell
ls -la vm-image/scripts/stockyard-shell/stockyard-shell
file vm-image/scripts/stockyard-shell/stockyard-shell
```
Expected: Static Linux amd64 binary

**Step 3: Verify build completes**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
go build ./...
```
Expected: No errors

**Step 4: Commit any final changes**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git status
# If clean, no commit needed
```

---

## Task 10: Document End-to-End Testing Steps

**Files:**
- Create: `docs/testing/vsock-shell-e2e.md`

**Step 1: Write E2E testing documentation**

```markdown
# vsock Shell End-to-End Testing

## Prerequisites

1. Working stockyard deployment
2. Rebuilt VM image with stockyard-shell

## Build and Deploy VM Image

```bash
# Build stockyard-shell
make build-shell

# Build VM image
cd vm-image
make

# Install to ZFS (adjust paths as needed)
sudo zfs destroy tank/stockyard/images/rootfs@base 2>/dev/null || true
sudo cp output/rootfs.ext4 /tank/stockyard/images/rootfs/rootfs.ext4
sudo zfs snapshot tank/stockyard/images/rootfs@base
sudo cp output/vmlinux.bin /var/lib/stockyard/vmlinux.bin
```

## Test Shell Service in VM

1. Create a new VM:
   ```bash
   stockyard run github.com/your/repo --name test-shell
   ```

2. Check service is running (SSH or console):
   ```bash
   systemctl status stockyard-shell
   journalctl -u stockyard-shell -f
   ```

3. Test from host (requires vsock tools or dashboard):
   - Open dashboard at http://localhost:65432
   - Navigate to VM detail page
   - Click "Open Terminal"
   - Verify shell prompt appears
   - Test typing, output, resize

## Troubleshooting

- **Service won't start**: Check `journalctl -u stockyard-shell`
- **Connection refused**: Verify vsock is enabled in Firecracker config
- **Permission denied**: Ensure service runs as root
- **No TERM**: Check Open message includes term field
```

**Step 2: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
mkdir -p docs/testing
git add docs/testing/vsock-shell-e2e.md
git commit -m "docs: add vsock shell end-to-end testing guide"
```

---

## Summary

After completing all tasks, you will have:

1. `pkg/shell/protocol.go` - Protocol with shared ShellPort constant, message types
2. `pkg/shell/session.go` - Session management with TERM and window size
3. `pkg/shell/integration_test.go` - Protocol integration tests
4. `cmd/stockyard-shell/main.go` - VM-side binary with timeout and signal handling
5. `vm-image/init/stockyard-shell.service` - systemd service (runs as root)
6. `vm-image/Dockerfile` - Updated to include stockyard-shell
7. `Makefile` - build-shell target
8. `vm-image/Makefile` - Dependency on shell binary
9. `docs/testing/vsock-shell-e2e.md` - Testing guide

**Key improvements from review:**
- Open message includes TERM and initial window size
- 5-second timeout for Open message
- SIGTERM handling for graceful shutdown
- Session tests skip on non-root
- vm-image build depends on shell binary
- Shared port constant
- E2E testing documentation

**Next steps (separate plan):**
- Update host-side terminal handler to use vsock instead of SSH
- Build and deploy updated VM image
- Test end-to-end

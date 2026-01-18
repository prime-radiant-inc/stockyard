# vsock Shell Service (VM Side) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create a vsock-based shell service that runs inside VMs, allowing the host to connect and get an interactive terminal without SSH.

**Architecture:** A Go binary (`stockyard-shell`) listens on vsock port 52, accepts connections, spawns a PTY with login shell for the requested user, and bridges I/O between vsock and PTY using a simple length-prefixed binary protocol.

**Tech Stack:** Go, github.com/mdlayher/vsock, github.com/creack/pty, systemd

---

## Task 1: Create Protocol Package

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

func TestWriteMessage_Open(t *testing.T) {
	var buf bytes.Buffer
	err := WriteMessage(&buf, MsgOpen, []byte(`{"user":"mooby"}`))
	if err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	// Expected: type(1) + length(4) + payload
	expected := []byte{
		0x01,                   // MsgOpen
		0x00, 0x00, 0x00, 0x10, // length 16 (big-endian)
	}
	expected = append(expected, []byte(`{"user":"mooby"}`)...)

	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("got %v, want %v", buf.Bytes(), expected)
	}
}

func TestReadMessage_Open(t *testing.T) {
	data := []byte{
		0x01,                   // MsgOpen
		0x00, 0x00, 0x00, 0x10, // length 16
	}
	data = append(data, []byte(`{"user":"mooby"}`)...)

	r := bytes.NewReader(data)
	msgType, payload, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	if msgType != MsgOpen {
		t.Errorf("got type %d, want %d", msgType, MsgOpen)
	}
	if string(payload) != `{"user":"mooby"}` {
		t.Errorf("got payload %q, want %q", payload, `{"user":"mooby"}`)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: FAIL (package doesn't exist)

**Step 3: Write minimal implementation**

```go
// pkg/shell/protocol.go
package shell

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Message types for vsock shell protocol
const (
	MsgOpen   uint8 = 0x01 // Host -> VM: open session {"user": "..."}
	MsgData   uint8 = 0x02 // Bidirectional: terminal data
	MsgResize uint8 = 0x03 // Host -> VM: resize {"cols": N, "rows": N}
	MsgExit   uint8 = 0x04 // VM -> Host: shell exited {"code": N}
	MsgError  uint8 = 0x05 // VM -> Host: error {"error": "..."}
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
git commit -m "feat(shell): add vsock shell protocol encoding/decoding"
```

---

## Task 2: Add JSON Message Types

**Files:**
- Modify: `pkg/shell/protocol.go`
- Modify: `pkg/shell/protocol_test.go`

**Step 1: Write failing test for JSON helpers**

```go
// Add to pkg/shell/protocol_test.go

func TestOpenMessage_Marshal(t *testing.T) {
	msg := OpenMessage{User: "mooby"}
	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(data) != `{"user":"mooby"}` {
		t.Errorf("got %q, want %q", data, `{"user":"mooby"}`)
	}
}

func TestOpenMessage_Unmarshal(t *testing.T) {
	var msg OpenMessage
	err := msg.Unmarshal([]byte(`{"user":"root"}`))
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if msg.User != "root" {
		t.Errorf("got user %q, want %q", msg.User, "root")
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
```

**Step 2: Run test to verify it fails**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: FAIL (types not defined)

**Step 3: Add JSON message types to protocol.go**

```go
// Add to pkg/shell/protocol.go after the existing code

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// OpenMessage requests a shell session for a user
type OpenMessage struct {
	User string `json:"user"`
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
git commit -m "feat(shell): add JSON message types for open, resize, exit, error"
```

---

## Task 3: Create Shell Session Handler

**Files:**
- Create: `pkg/shell/session.go`
- Create: `pkg/shell/session_test.go`

**Step 1: Write failing test for session creation**

```go
// pkg/shell/session_test.go
package shell

import (
	"os/user"
	"testing"
)

func TestValidateUser_ValidUser(t *testing.T) {
	// Current user should always be valid
	u, err := user.Current()
	if err != nil {
		t.Skip("cannot get current user")
	}

	err = ValidateUser(u.Username)
	if err != nil {
		t.Errorf("ValidateUser(%q) failed: %v", u.Username, err)
	}
}

func TestValidateUser_Root(t *testing.T) {
	err := ValidateUser("root")
	if err != nil {
		t.Errorf("ValidateUser(root) failed: %v", err)
	}
}

func TestValidateUser_InvalidUser(t *testing.T) {
	err := ValidateUser("nonexistent_user_12345")
	if err == nil {
		t.Error("ValidateUser should fail for nonexistent user")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: FAIL (ValidateUser not defined)

**Step 3: Write minimal implementation**

```go
// pkg/shell/session.go
package shell

import (
	"fmt"
	"os/user"
)

// ValidateUser checks if a user exists on the system
func ValidateUser(username string) error {
	_, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user %q not found: %w", username, err)
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: PASS

**Step 5: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add pkg/shell/session.go pkg/shell/session_test.go
git commit -m "feat(shell): add user validation"
```

---

## Task 4: Create Shell Spawning Logic

**Files:**
- Modify: `pkg/shell/session.go`
- Modify: `pkg/shell/session_test.go`

**Step 1: Write failing test for shell spawning**

```go
// Add to pkg/shell/session_test.go

func TestSession_SpawnAndClose(t *testing.T) {
	// This test requires running as root or the current user
	u, err := user.Current()
	if err != nil {
		t.Skip("cannot get current user")
	}

	session, err := NewSession(u.Username)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	// Session should have a valid PTY
	if session.PTY() == nil {
		t.Error("session PTY is nil")
	}

	// Should be able to close cleanly
	if err := session.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd ~/.config/superpowers/worktrees/stockyard/vsock-shell && go test ./pkg/shell/... -v`
Expected: FAIL (NewSession not defined)

**Step 3: Implement Session with PTY spawning**

```go
// Replace pkg/shell/session.go content
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
	user     string
	cmd      *exec.Cmd
	ptyFile  *os.File
	mu       sync.Mutex
	closed   bool
	exitCode int
}

// NewSession creates a new shell session for the given user
func NewSession(username string) (*Session, error) {
	if err := ValidateUser(username); err != nil {
		return nil, err
	}

	// Use login -f for a proper login shell with full environment setup
	// This handles PAM, sets up environment variables, etc.
	cmd := exec.Command("login", "-f", username)

	// Start with PTY
	ptyFile, err := pty.Start(cmd)
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
Expected: PASS (may need to run as root for login command, or test may skip)

**Step 5: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add pkg/shell/session.go pkg/shell/session_test.go
git commit -m "feat(shell): add session with PTY spawning via login -f"
```

---

## Task 5: Create stockyard-shell Binary

**Files:**
- Create: `cmd/stockyard-shell/main.go`

**Step 1: Create the main binary with vsock listener**

```go
// cmd/stockyard-shell/main.go
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"

	"github.com/mdlayher/vsock"
	"github.com/obra/stockyard/pkg/shell"
)

const (
	vsockPort = 52
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("stockyard-shell starting on vsock port %d", vsockPort)

	listener, err := vsock.Listen(vsockPort, nil)
	if err != nil {
		log.Fatalf("Failed to listen on vsock port %d: %v", vsockPort, err)
	}
	defer listener.Close()

	log.Printf("Listening for connections...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}

		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	log.Printf("New connection from %s", remoteAddr)

	// Read Open message
	msgType, payload, err := shell.ReadMessage(conn)
	if err != nil {
		log.Printf("Failed to read open message: %v", err)
		sendError(conn, "failed to read open message")
		return
	}

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

	log.Printf("Opening shell for user %q", openMsg.User)

	// Create session
	session, err := shell.NewSession(openMsg.User)
	if err != nil {
		log.Printf("Failed to create session: %v", err)
		sendError(conn, fmt.Sprintf("failed to create session: %v", err))
		return
	}
	defer session.Close()

	// Bridge I/O between vsock and PTY
	var wg sync.WaitGroup

	// PTY -> vsock (stdout)
	wg.Add(1)
	go func() {
		defer wg.Done()
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
		for {
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
	}

	log.Printf("Shell exited with code %d", exitCode)

	// Send exit message
	exitMsg := shell.ExitMessage{Code: exitCode}
	exitPayload, _ := exitMsg.Marshal()
	shell.WriteMessage(conn, shell.MsgExit, exitPayload)

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
git commit -m "feat(shell): add stockyard-shell binary with vsock listener"
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
ExecStart=/usr/local/bin/stockyard-shell
Restart=always
RestartSec=1
StandardOutput=journal
StandardError=journal

# Security hardening
NoNewPrivileges=false
# Note: Cannot use most sandboxing since we spawn login shells

[Install]
WantedBy=multi-user.target
```

**Step 2: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add vm-image/init/stockyard-shell.service
git commit -m "feat(vm-image): add systemd service for stockyard-shell"
```

---

## Task 7: Update VM Image Dockerfile

**Files:**
- Modify: `vm-image/Dockerfile`

**Step 1: Add stockyard-shell to Dockerfile**

Find the section where stockyard-snapshot is copied and add stockyard-shell nearby:

```dockerfile
# Add after the stockyard-snapshot COPY line (around line 145)
# Copy stockyard-shell binary for vsock terminal access
COPY scripts/stockyard-shell/stockyard-shell /usr/local/bin/stockyard-shell
RUN chmod +x /usr/local/bin/stockyard-shell

# Add the systemd service
COPY init/stockyard-shell.service /etc/systemd/system/stockyard-shell.service
RUN systemctl enable stockyard-shell.service 2>/dev/null || true
```

**Step 2: Create placeholder directory for binary**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
mkdir -p vm-image/scripts/stockyard-shell
```

**Step 3: Commit Dockerfile changes**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add vm-image/Dockerfile vm-image/scripts/stockyard-shell/.gitkeep
git commit -m "feat(vm-image): integrate stockyard-shell into Docker build"
```

---

## Task 8: Add Build Target for stockyard-shell

**Files:**
- Modify: `Makefile`

**Step 1: Add build target**

```makefile
# Add to Makefile after the existing build targets

# Build stockyard-shell for VM (static binary for Linux)
build-shell:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o vm-image/scripts/stockyard-shell/stockyard-shell ./cmd/stockyard-shell
```

**Step 2: Update the vm-image build to depend on shell binary**

The vm-image Makefile should build stockyard-shell before Docker build.

**Step 3: Commit**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git add Makefile
git commit -m "build: add build-shell target for stockyard-shell binary"
```

---

## Task 9: Integration Test

**Files:**
- Create: `pkg/shell/integration_test.go`

**Step 1: Write integration test (skipped without vsock)**

```go
// pkg/shell/integration_test.go
package shell

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestIntegration_Protocol(t *testing.T) {
	// This test verifies the protocol works over a regular connection
	// (vsock testing requires VM environment)

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	// Server goroutine
	go func() {
		// Read open message
		msgType, payload, err := ReadMessage(server)
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		if msgType != MsgOpen {
			t.Errorf("expected MsgOpen, got %d", msgType)
			return
		}

		var open OpenMessage
		open.Unmarshal(payload)

		// Send some data back
		WriteMessage(server, MsgData, []byte("hello "+open.User))

		// Send exit
		exit := ExitMessage{Code: 0}
		payload, _ = exit.Marshal()
		WriteMessage(server, MsgExit, payload)
	}()

	// Client side
	open := OpenMessage{User: "testuser"}
	payload, _ := open.Marshal()
	if err := WriteMessage(client, MsgOpen, payload); err != nil {
		t.Fatalf("client write open: %v", err)
	}

	// Read data response
	client.SetReadDeadline(time.Now().Add(time.Second))
	msgType, payload, err := ReadMessage(client)
	if err != nil {
		t.Fatalf("client read data: %v", err)
	}
	if msgType != MsgData {
		t.Errorf("expected MsgData, got %d", msgType)
	}
	if string(payload) != "hello testuser" {
		t.Errorf("got %q, want %q", payload, "hello testuser")
	}

	// Read exit
	msgType, payload, err = ReadMessage(client)
	if err != nil {
		t.Fatalf("client read exit: %v", err)
	}
	if msgType != MsgExit {
		t.Errorf("expected MsgExit, got %d", msgType)
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
git commit -m "test(shell): add protocol integration test"
```

---

## Task 10: Final Build and Verify

**Step 1: Build everything**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
go build ./...
make build-shell
```

**Step 2: Verify binary exists**

```bash
ls -la vm-image/scripts/stockyard-shell/stockyard-shell
file vm-image/scripts/stockyard-shell/stockyard-shell
```
Expected: Static Linux binary

**Step 3: Run all tests**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
go test ./... -v
```
Expected: All pass

**Step 4: Final commit if any loose changes**

```bash
cd ~/.config/superpowers/worktrees/stockyard/vsock-shell
git status
# Commit any remaining changes
```

---

## Summary

After completing all tasks, you will have:

1. `pkg/shell/` - Protocol and session management package
2. `cmd/stockyard-shell/` - VM-side binary
3. `vm-image/init/stockyard-shell.service` - systemd service
4. `vm-image/Dockerfile` - Updated to include stockyard-shell
5. `Makefile` - Build target for stockyard-shell

Next steps (separate plan):
- Update host-side terminal handler to use vsock instead of SSH
- Build and deploy updated VM image

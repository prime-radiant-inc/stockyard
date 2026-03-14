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

	// Validate command is present
	if len(openMsg.Command) == 0 {
		log.Printf("No command specified in open message")
		sendError(conn, "command is required")
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

	log.Printf("Executing command for user %q: %v (term=%s, size=%dx%d)",
		openMsg.User, openMsg.Command, openMsg.Term, openMsg.Cols, openMsg.Rows)

	// Create session with the specified command
	session, err := shell.NewSession(openMsg.User, openMsg.Term, openMsg.Cols, openMsg.Rows, openMsg.Command, openMsg.Env)
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
	var ioWg sync.WaitGroup

	// PTY -> vsock (stdout)
	ioWg.Add(1)
	go func() {
		defer ioWg.Done()
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
	ioWg.Add(1)
	go func() {
		defer ioWg.Done()
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

	// Wait for the PTY→vsock goroutine to drain remaining output before
	// sending MsgExit. The PTY read will return EOF once the process is
	// gone and the kernel buffer is empty. Without this, fast commands
	// (like "echo hello") lose output because MsgExit arrives before MsgData.
	ioWg.Wait()

	// Send exit message (best effort)
	exitMsg := shell.ExitMessage{Code: exitCode}
	if exitPayload, err := exitMsg.Marshal(); err == nil {
		shell.WriteMessage(conn, shell.MsgExit, exitPayload)
	}

	connCancel()
}

func sendError(conn net.Conn, message string) {
	errMsg := shell.ErrorMessage{Error: message}
	if payload, err := errMsg.Marshal(); err == nil {
		shell.WriteMessage(conn, shell.MsgError, payload)
	}
}

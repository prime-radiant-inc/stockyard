// Package daemon provides state management for the stockyard daemon.
package daemon

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/obra/stockyard/pkg/shell"
)

// runVsockCommand connects to the VM's stockyard-shell over vsock, sends the
// command, collects output to disk, and returns the exit code.
//
// Connection protocol (Firecracker vsock proxy):
//  1. Dial the Unix domain socket at task.VsockPath.
//  2. Write "CONNECT <port>\n" and read back "OK <cid>\n".
//  3. Exchange shell protocol messages (MsgOpen, MsgData, MsgExit).
func (qm *QueueManager) runVsockCommand(task *Task, cmd *Command) (int, error) {
	vsockPath := task.VsockPath
	if vsockPath == "" {
		return 1, fmt.Errorf("task %s has no vsock path", task.ID)
	}

	// Ensure output directory exists before opening the file.
	outDir := filepath.Dir(cmd.OutputPath)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return 1, fmt.Errorf("create output dir: %w", err)
	}

	outFile, err := os.Create(cmd.OutputPath)
	if err != nil {
		return 1, fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Connect to Firecracker vsock UDS.
	conn, err := net.Dial("unix", vsockPath)
	if err != nil {
		return 1, fmt.Errorf("dial vsock %s: %w", vsockPath, err)
	}
	defer conn.Close()

	// Send CONNECT handshake per the Firecracker vsock proxy protocol.
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", shell.ShellPort); err != nil {
		return 1, fmt.Errorf("send CONNECT: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return 1, fmt.Errorf("read CONNECT response: %w", err)
	}
	if !strings.HasPrefix(line, "OK ") {
		return 1, fmt.Errorf("vsock CONNECT failed: %s", strings.TrimSpace(line))
	}

	// Send MsgOpen. Use the configured VM user for privilege dropping inside the guest.
	openMsg := &shell.OpenMessage{
		User:    qm.cfg.VM.User,
		Term:    "dumb",
		Cols:    220,
		Rows:    50,
		Command: cmd.Command,
		Env:     cmd.Env,
	}
	payload, err := openMsg.Marshal()
	if err != nil {
		return 1, fmt.Errorf("marshal open: %w", err)
	}
	if err := shell.WriteMessage(conn, shell.MsgOpen, payload); err != nil {
		return 1, fmt.Errorf("write open: %w", err)
	}

	// Read messages from the buffered reader (not raw conn) to avoid
	// losing bytes that bufio may have read ahead during the CONNECT handshake.
	for {
		msgType, payload, err := shell.ReadMessage(reader)
		if err != nil {
			return 1, fmt.Errorf("read message: %w", err)
		}

		switch msgType {
		case shell.MsgData:
			// Write output data to the log file; ignore write errors so a full
			// disk doesn't kill a running command.
			outFile.Write(payload)

		case shell.MsgExit:
			var exitMsg shell.ExitMessage
			exitMsg.Unmarshal(payload)
			return exitMsg.Code, nil

		case shell.MsgError:
			var errMsg shell.ErrorMessage
			errMsg.Unmarshal(payload)
			return 1, fmt.Errorf("VM error: %s", errMsg.Error)
		}
	}
}

// stockyard-snapshot is a client for requesting snapshots from the host.
// It communicates via vsock to the stockyard service running on the host.
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/mdlayher/vsock"
)

const (
	// vsockHostCID is the CID for the host in a VM context
	vsockHostCID = 2
	// defaultVSockPort is the default port for the stockyard snapshot service
	defaultVSockPort = 52000
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: stockyard-snapshot <label>")
		fmt.Fprintln(os.Stderr, "       stockyard-snapshot --event=<event>")
		os.Exit(1)
	}

	var label string
	if strings.HasPrefix(os.Args[1], "--event=") {
		label = "event:" + strings.TrimPrefix(os.Args[1], "--event=")
	} else {
		label = strings.Join(os.Args[1:], " ")
	}

	label = sanitizeLabel(label)

	if err := requestSnapshot(label); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create snapshot: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Snapshot requested: %s\n", label)
}

// sanitizeLabel cleans a label for use in ZFS snapshot names.
// Only alphanumeric characters, hyphens, underscores, and periods are allowed.
func sanitizeLabel(s string) string {
	var result strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			result.WriteRune(c)
		} else {
			result.WriteRune('-')
		}
	}
	return result.String()
}

// requestSnapshot sends a snapshot request to the host via vsock.
func requestSnapshot(label string) error {
	conn, err := dialVsock(vsockHostCID, defaultVSockPort)
	if err != nil {
		return fmt.Errorf("failed to connect to host: %w", err)
	}
	defer conn.Close()

	// Set deadline for the operation
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Protocol: send label length (4 bytes) + label
	labelBytes := []byte(label)
	if err := binary.Write(conn, binary.LittleEndian, uint32(len(labelBytes))); err != nil {
		return fmt.Errorf("failed to send label length: %w", err)
	}

	if _, err := conn.Write(labelBytes); err != nil {
		return fmt.Errorf("failed to send label: %w", err)
	}

	// Read response: status (1 byte) + message length (4 bytes) + message
	var status byte
	if err := binary.Read(conn, binary.LittleEndian, &status); err != nil {
		return fmt.Errorf("failed to read status: %w", err)
	}

	var msgLen uint32
	if err := binary.Read(conn, binary.LittleEndian, &msgLen); err != nil {
		return fmt.Errorf("failed to read message length: %w", err)
	}

	if msgLen > 0 && msgLen < 65536 {
		msg := make([]byte, msgLen)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return fmt.Errorf("failed to read message: %w", err)
		}

		if status != 0 {
			return fmt.Errorf("snapshot failed: %s", string(msg))
		}
	}

	if status != 0 {
		return fmt.Errorf("snapshot failed with status %d", status)
	}

	return nil
}

// dialVsock connects to a vsock address.
// It tries vsock first, then falls back to a Unix socket for testing.
func dialVsock(cid uint32, port uint32) (net.Conn, error) {
	// Try vsock first (for real VMs)
	conn, err := tryVsock(cid, port)
	if err == nil {
		return conn, nil
	}

	// Fall back to Unix socket for testing/development
	sockPath := "/run/stockyard/snapshot.sock"
	conn, err = net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("vsock and unix fallback both failed: vsock error: %v", err)
	}
	return conn, nil
}

// tryVsock attempts to connect using AF_VSOCK.
func tryVsock(cid uint32, port uint32) (net.Conn, error) {
	// Check if vsock is available
	vsockPath := "/dev/vsock"
	if _, err := os.Stat(vsockPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("vsock not available: %s does not exist", vsockPath)
	}

	// Dial using the mdlayher/vsock package
	conn, err := vsock.Dial(cid, port, nil)
	if err != nil {
		return nil, fmt.Errorf("vsock dial failed: %w", err)
	}
	return conn, nil
}

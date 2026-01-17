// pkg/vsock/protocol.go
package vsock

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Protocol for snapshot requests over vsock:
// Request:  [label_len:uint32][label:bytes]
// Response: [status:uint8][msg_len:uint32][msg:bytes]
//
// status: 0 = success, 1 = failure

const (
	StatusSuccess = 0
	StatusFailure = 1
)

// EncodeSnapshotRequest encodes a snapshot request
func EncodeSnapshotRequest(w io.Writer, label string) error {
	labelBytes := []byte(label)
	if err := binary.Write(w, binary.LittleEndian, uint32(len(labelBytes))); err != nil {
		return fmt.Errorf("write label length: %w", err)
	}
	if _, err := w.Write(labelBytes); err != nil {
		return fmt.Errorf("write label: %w", err)
	}
	return nil
}

// DecodeSnapshotRequest decodes a snapshot request
func DecodeSnapshotRequest(r io.Reader) (string, error) {
	var labelLen uint32
	if err := binary.Read(r, binary.LittleEndian, &labelLen); err != nil {
		return "", fmt.Errorf("read label length: %w", err)
	}

	if labelLen > 1024 {
		return "", fmt.Errorf("label too long: %d", labelLen)
	}

	labelBytes := make([]byte, labelLen)
	if _, err := io.ReadFull(r, labelBytes); err != nil {
		return "", fmt.Errorf("read label: %w", err)
	}

	return string(labelBytes), nil
}

// EncodeSnapshotResponse encodes a snapshot response
func EncodeSnapshotResponse(w io.Writer, success bool, message string) error {
	status := uint8(StatusSuccess)
	if !success {
		status = StatusFailure
	}

	if err := binary.Write(w, binary.LittleEndian, status); err != nil {
		return fmt.Errorf("write status: %w", err)
	}

	msgBytes := []byte(message)
	if err := binary.Write(w, binary.LittleEndian, uint32(len(msgBytes))); err != nil {
		return fmt.Errorf("write message length: %w", err)
	}

	if len(msgBytes) > 0 {
		if _, err := w.Write(msgBytes); err != nil {
			return fmt.Errorf("write message: %w", err)
		}
	}

	return nil
}

// DecodeSnapshotResponse decodes a snapshot response
func DecodeSnapshotResponse(r io.Reader) (success bool, message string, err error) {
	var status uint8
	if err := binary.Read(r, binary.LittleEndian, &status); err != nil {
		return false, "", fmt.Errorf("read status: %w", err)
	}

	var msgLen uint32
	if err := binary.Read(r, binary.LittleEndian, &msgLen); err != nil {
		return false, "", fmt.Errorf("read message length: %w", err)
	}

	if msgLen > 0 {
		if msgLen > 4096 {
			return false, "", fmt.Errorf("message too long: %d", msgLen)
		}

		msgBytes := make([]byte, msgLen)
		if _, err := io.ReadFull(r, msgBytes); err != nil {
			return false, "", fmt.Errorf("read message: %w", err)
		}
		message = string(msgBytes)
	}

	return status == StatusSuccess, message, nil
}

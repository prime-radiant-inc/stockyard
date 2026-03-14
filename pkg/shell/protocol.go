package shell

import (
	"encoding/binary"
	"encoding/json"
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

// OpenMessage requests a shell session for a user.
// Includes terminal type and initial window size.
// Command, if non-empty, specifies the program to run instead of the default login shell.
// Env provides additional environment variables to set for the session.
type OpenMessage struct {
	User    string            `json:"user"`
	Term    string            `json:"term"` // e.g., "xterm-256color"
	Cols    int               `json:"cols"`
	Rows    int               `json:"rows"`
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
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

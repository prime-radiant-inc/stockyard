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

func TestWriteMessage_EmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	err := WriteMessage(&buf, MsgData, []byte{})
	if err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}
	expected := []byte{0x02, 0x00, 0x00, 0x00, 0x00}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("got %v, want %v", buf.Bytes(), expected)
	}
}

func TestReadMessage_EmptyPayload(t *testing.T) {
	data := []byte{0x02, 0x00, 0x00, 0x00, 0x00}
	r := bytes.NewReader(data)
	msgType, payload, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if msgType != MsgData {
		t.Errorf("got type %d, want %d", msgType, MsgData)
	}
	if len(payload) != 0 {
		t.Errorf("got payload length %d, want 0", len(payload))
	}
}

func TestReadMessage_IncompleteType(t *testing.T) {
	r := bytes.NewReader([]byte{})
	_, _, err := ReadMessage(r)
	if err == nil {
		t.Error("expected error for incomplete type byte")
	}
}

func TestReadMessage_IncompletePayload(t *testing.T) {
	data := []byte{0x02, 0x00, 0x00, 0x00, 0x05, 'h', 'e'} // Claims 5 bytes, only 2
	r := bytes.NewReader(data)
	_, _, err := ReadMessage(r)
	if err == nil {
		t.Error("expected error for incomplete payload")
	}
}

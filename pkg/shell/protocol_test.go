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

func TestOpenMessageWithCommand(t *testing.T) {
	msg := OpenMessage{
		User:    "mooby",
		Term:    "xterm-256color",
		Cols:    120,
		Rows:    40,
		Command: []string{"claude", "-p", "implement OAuth"},
		Env:     map[string]string{"CLAUDE_MODEL": "opus"},
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded OpenMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.Command) != 3 || decoded.Command[0] != "claude" {
		t.Errorf("command = %v, want [claude -p implement OAuth]", decoded.Command)
	}
	if decoded.Env["CLAUDE_MODEL"] != "opus" {
		t.Errorf("env CLAUDE_MODEL = %q, want %q", decoded.Env["CLAUDE_MODEL"], "opus")
	}
}

func TestOpenMessageCommandRequired(t *testing.T) {
	msg := OpenMessage{
		User: "mooby",
		Term: "xterm",
		Cols: 80,
		Rows: 24,
		// Command intentionally empty
	}

	data, err := msg.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded OpenMessage
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded.Command) != 0 {
		t.Errorf("expected empty command, got %v", decoded.Command)
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

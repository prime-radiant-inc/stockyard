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

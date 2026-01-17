// cmd/stockyard/attach_test.go
package main

import (
	"testing"
)

func TestAttachCommand_RequiresTaskID(t *testing.T) {
	rootCmd.SetArgs([]string{"attach"})
	err := rootCmd.Execute()

	if err == nil {
		t.Fatal("expected error when task-id not provided")
	}
}

// cmd/stockyard/restart_test.go
package main

import "testing"

func TestRestartCommand_RequiresTaskID(t *testing.T) {
	rootCmd.SetArgs([]string{"restart"})
	err := rootCmd.Execute()
	if err == nil {
		t.Error("expected error for missing task ID")
	}
}

func TestRestartCommand_Help(t *testing.T) {
	if restartCmd.Use != "restart <task-id>" {
		t.Errorf("expected Use 'restart <task-id>', got %q", restartCmd.Use)
	}
	if restartCmd.Short == "" {
		t.Error("expected non-empty Short description")
	}
}

// cmd/stockyard/attach_test.go
package main

import (
	"testing"

	pb "github.com/obra/stockyard/pkg/api/v1"
)

func TestAttachCommand_RequiresTaskID(t *testing.T) {
	rootCmd.SetArgs([]string{"attach"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when task-id not provided")
	}
}

func TestBuildAttachCommand_AppleContainer(t *testing.T) {
	task := &pb.Task{
		Id:      "abc12345",
		Status:  "running",
		Backend: "apple-container",
		VmId:    "abc12345",
	}
	name, argv, err := buildAttachCommand(task, "mooby", "container", nil)
	if err != nil {
		t.Fatalf("buildAttachCommand: %v", err)
	}
	if name != "container" {
		t.Errorf("expected program 'container', got %q", name)
	}
	joined := join(argv)
	for _, want := range []string{"exec", "-t", "-i", "-u", "mooby", "stockyard-abc12345"} {
		if !contains(joined, want) {
			t.Errorf("apple-container argv missing %q; got %v", want, argv)
		}
	}
}

func TestBuildAttachCommand_SSHForEmptyBackend(t *testing.T) {
	// Empty backend (legacy Firecracker) must take the SSH path.
	task := &pb.Task{
		Id:                "abc12345",
		Status:            "running",
		Backend:           "",
		TailscaleHostname: "stockyard-abc12345",
	}
	name, _, err := buildAttachCommand(task, "mooby", "container", nil)
	if err != nil {
		t.Fatalf("buildAttachCommand: %v", err)
	}
	if name != "ssh" {
		t.Errorf("empty backend should use ssh, got %q", name)
	}
}

// tiny test helpers
func join(a []string) string {
	s := ""
	for _, x := range a {
		s += x + " "
	}
	return s
}
func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

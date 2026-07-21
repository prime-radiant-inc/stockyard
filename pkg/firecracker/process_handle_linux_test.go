//go:build linux

package firecracker

import (
	"os"
	"reflect"
	"testing"
)

func TestLinuxProcessIdentityUsesKernelExecutableAndSeparateArguments(t *testing.T) {
	configuredPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configuredIdentity, err := resolveConfiguredExecutableIdentity(configuredPath)
	if err != nil {
		t.Fatalf("resolve configured test executable: %v", err)
	}
	kernelIdentity, err := readLinuxExecutableIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("read kernel executable identity: %v", err)
	}
	if !sameExecutableIdentity(configuredIdentity, kernelIdentity) {
		t.Fatal("kernel executable identity does not match the configured executable file")
	}

	arguments, err := readLinuxProcessArguments(os.Getpid())
	if err != nil {
		t.Fatalf("read process arguments: %v", err)
	}
	if !reflect.DeepEqual(arguments, os.Args[1:]) {
		t.Fatalf("process arguments = %q, want exact argv boundaries %q", arguments, os.Args[1:])
	}
}

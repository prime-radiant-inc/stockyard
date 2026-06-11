// cmd/stockyard/snapshot_test.go
package main

import "testing"

func TestSnapshotGroup_Structure(t *testing.T) {
	if snapshotCmd.Use != "snapshot" {
		t.Errorf("expected Use 'snapshot', got %q", snapshotCmd.Use)
	}
	if snapshotCmd.Short == "" {
		t.Error("expected non-empty Short description")
	}
	if snapshotCmd.Runnable() {
		t.Error("snapshot group must not be runnable itself (no Run/RunE)")
	}
	for _, name := range []string{"create", "ls", "restore"} {
		found := false
		for _, sub := range snapshotCmd.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected subcommand %q under snapshot group", name)
		}
	}
}

func TestSnapshotCreate_UsageAndArgs(t *testing.T) {
	if snapshotCreateCmd.Use != "create <task-id> [label]" {
		t.Errorf("expected Use 'create <task-id> [label]', got %q", snapshotCreateCmd.Use)
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{}); err == nil {
		t.Error("expected arg-validation error for zero args")
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{"task-1"}); err != nil {
		t.Errorf("expected one arg to be accepted, got %v", err)
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{"task-1", "label"}); err != nil {
		t.Errorf("expected two args to be accepted, got %v", err)
	}
	if err := snapshotCreateCmd.Args(snapshotCreateCmd, []string{"task-1", "label", "extra"}); err == nil {
		t.Error("expected arg-validation error for three args")
	}
}

func TestSnapshotLs_AliasAndArgs(t *testing.T) {
	if snapshotLsCmd.Use != "ls <task-id>" {
		t.Errorf("expected Use 'ls <task-id>', got %q", snapshotLsCmd.Use)
	}
	if len(snapshotLsCmd.Aliases) != 1 || snapshotLsCmd.Aliases[0] != "list" {
		t.Errorf("expected alias 'list', got %v", snapshotLsCmd.Aliases)
	}
	if err := snapshotLsCmd.Args(snapshotLsCmd, []string{}); err == nil {
		t.Error("expected arg-validation error for zero args")
	}
	if err := snapshotLsCmd.Args(snapshotLsCmd, []string{"task-1"}); err != nil {
		t.Errorf("expected one arg to be accepted, got %v", err)
	}
}

func TestSnapshotRestore_ArgsAndForceFlag(t *testing.T) {
	if snapshotRestoreCmd.Use != "restore <task-id> <snapshot-name>" {
		t.Errorf("expected Use 'restore <task-id> <snapshot-name>', got %q", snapshotRestoreCmd.Use)
	}
	if err := snapshotRestoreCmd.Args(snapshotRestoreCmd, []string{"task-1"}); err == nil {
		t.Error("expected arg-validation error for one arg")
	}
	if err := snapshotRestoreCmd.Args(snapshotRestoreCmd, []string{"task-1", "snap-1"}); err != nil {
		t.Errorf("expected two args to be accepted, got %v", err)
	}
	flag := snapshotRestoreCmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag on snapshot restore")
	}
	if flag.Shorthand != "f" {
		t.Errorf("expected -f shorthand for --force, got %q", flag.Shorthand)
	}
}

func TestOldTopLevelCommandsRemoved(t *testing.T) {
	found := false
	for _, c := range rootCmd.Commands() {
		switch c.Name() {
		case "snapshots", "restore":
			t.Errorf("old top-level command %q must be removed", c.Name())
		case "snapshot":
			found = true
			if c.Runnable() {
				t.Error("top-level 'snapshot' must be a group, not the old runnable create command")
			}
		}
	}
	if !found {
		t.Error("'snapshot' group is not registered on rootCmd")
	}
}

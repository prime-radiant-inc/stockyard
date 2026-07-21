package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func cleanOrphanAuditReaders() orphanAuditReaders {
	return orphanAuditReaders{
		listTasks: func(context.Context) ([]orphanAuditTask, error) {
			return []orphanAuditTask{{ID: "task-1", VMID: "vm-1"}}, nil
		},
		listVMDirs: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: "vm-1"}}, nil
		},
		listProcesses: func(context.Context) ([]orphanAuditProcess, error) {
			return []orphanAuditProcess{{OwnerID: "vm-1", PID: 123, Running: true}}, nil
		},
		listRootfsDatasets: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: "vm-1"}}, nil
		},
		listWorkspaceDatasets: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: "task-1"}}, nil
		},
		listTaps: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: "vm-1"}}, nil
		},
		listIPAllocations: func(context.Context) (map[string]string, error) {
			return map[string]string{"task-1": "10.0.0.2"}, nil
		},
	}
}

func decodeOrphanAudit(t *testing.T, output bytes.Buffer) orphanAudit {
	t.Helper()
	var got orphanAudit
	if err := decodeSingleJSON(output.Bytes(), &got); err != nil {
		t.Fatalf("decode audit JSON: %v", err)
	}
	return got
}

func TestOrphanAuditCleanInventoryIsReadOnlyAndSucceeds(t *testing.T) {
	readers := cleanOrphanAuditReaders()
	var output bytes.Buffer
	if err := runOrphanAudit(context.Background(), readers, &output); err != nil {
		t.Fatalf("runOrphanAudit: %v", err)
	}
	got := decodeOrphanAudit(t, output)
	if len(got.Mismatches) != 0 || len(got.UnknownReads) != 0 {
		t.Fatalf("clean audit = %#v", got)
	}
}

func TestOrphanAuditReportsRunningOrphanProcess(t *testing.T) {
	readers := cleanOrphanAuditReaders()
	readers.listProcesses = func(context.Context) ([]orphanAuditProcess, error) {
		return []orphanAuditProcess{{OwnerID: "orphan-vm", PID: 456, Running: true}}, nil
	}
	var output bytes.Buffer
	if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
		t.Fatal("runOrphanAudit accepted a running orphan process")
	}
	got := decodeOrphanAudit(t, output)
	if len(got.Mismatches) != 1 || got.Mismatches[0].Resource != "process" || got.Mismatches[0].OwnerID != "orphan-vm" || !got.Mismatches[0].Running {
		t.Fatalf("running orphan result = %#v", got)
	}
}

func TestOrphanAuditRejectsOrphanResources(t *testing.T) {
	tests := []struct {
		name string
		set  func(*orphanAuditReaders)
	}{
		{
			name: "VM directory",
			set: func(readers *orphanAuditReaders) {
				readers.listVMDirs = func(context.Context) ([]orphanAuditResource, error) {
					return []orphanAuditResource{{OwnerID: "orphan-vm"}}, nil
				}
			},
		},
		{
			name: "rootfs dataset",
			set: func(readers *orphanAuditReaders) {
				readers.listRootfsDatasets = func(context.Context) ([]orphanAuditResource, error) {
					return []orphanAuditResource{{OwnerID: "orphan-vm"}}, nil
				}
			},
		},
		{
			name: "workspace dataset",
			set: func(readers *orphanAuditReaders) {
				readers.listWorkspaceDatasets = func(context.Context) ([]orphanAuditResource, error) {
					return []orphanAuditResource{{OwnerID: "orphan-task"}}, nil
				}
			},
		},
		{
			name: "tap",
			set: func(readers *orphanAuditReaders) {
				readers.listTaps = func(context.Context) ([]orphanAuditResource, error) {
					return []orphanAuditResource{{OwnerID: "orphan-vm"}}, nil
				}
			},
		},
		{
			name: "IP allocation",
			set: func(readers *orphanAuditReaders) {
				readers.listIPAllocations = func(context.Context) (map[string]string, error) {
					return map[string]string{"orphan-task": "10.0.0.9"}, nil
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readers := cleanOrphanAuditReaders()
			tt.set(&readers)
			var output bytes.Buffer
			if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
				t.Fatal("runOrphanAudit accepted an orphan")
			}
			got := decodeOrphanAudit(t, output)
			if len(got.Mismatches) != 1 || len(got.UnknownReads) != 0 {
				t.Fatalf("orphan result = %#v", got)
			}
		})
	}
}

func TestOrphanAuditRejectsDuplicateMalformedAndUnknownEvidence(t *testing.T) {
	tests := []struct {
		name    string
		set     func(*orphanAuditReaders)
		unknown bool
	}{
		{
			name: "duplicate VM ownership",
			set: func(readers *orphanAuditReaders) {
				readers.listVMDirs = func(context.Context) ([]orphanAuditResource, error) {
					return []orphanAuditResource{{OwnerID: "vm-1"}, {OwnerID: "vm-1"}}, nil
				}
			},
		},
		{
			name: "malformed task row",
			set: func(readers *orphanAuditReaders) {
				readers.listTasks = func(context.Context) ([]orphanAuditTask, error) {
					return []orphanAuditTask{{ID: "task-1", VMID: ""}}, nil
				}
			},
		},
		{
			name: "duplicate IP",
			set: func(readers *orphanAuditReaders) {
				readers.listTasks = func(context.Context) ([]orphanAuditTask, error) {
					return []orphanAuditTask{{ID: "task-1", VMID: "vm-1"}, {ID: "task-2", VMID: "vm-2"}}, nil
				}
				readers.listIPAllocations = func(context.Context) (map[string]string, error) {
					return map[string]string{"task-1": "10.0.0.2", "task-2": "10.0.0.2"}, nil
				}
			},
		},
		{
			name: "malformed IP allocation",
			set: func(readers *orphanAuditReaders) {
				readers.listIPAllocations = func(context.Context) (map[string]string, error) { return map[string]string{"task-1": "not-an-ip"}, nil }
			},
		},
		{
			name: "unknown tap read",
			set: func(readers *orphanAuditReaders) {
				readers.listTaps = func(context.Context) ([]orphanAuditResource, error) { return nil, errors.New("permission denied") }
			},
			unknown: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readers := cleanOrphanAuditReaders()
			tt.set(&readers)
			var output bytes.Buffer
			if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
				t.Fatal("runOrphanAudit accepted invalid evidence")
			}
			got := decodeOrphanAudit(t, output)
			if tt.unknown {
				if len(got.UnknownReads) != 1 || got.UnknownReads[0].Source != "taps" {
					t.Fatalf("unknown result = %#v", got)
				}
				return
			}
			if len(got.Mismatches) == 0 {
				t.Fatalf("mismatch result = %#v", got)
			}
		})
	}
}

func TestAuditProcessSocketRejectsMalformedOwnership(t *testing.T) {
	for _, arguments := range [][]string{
		{"--api-sock"},
		{"--api-sock", "/one.sock", "--api-sock", "/two.sock"},
	} {
		if _, found, err := auditProcessSocket(arguments); !found || err == nil {
			t.Fatalf("auditProcessSocket(%q) = found %t, err %v; want malformed ownership", arguments, found, err)
		}
	}
}

func TestListAuditIPAllocationsRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_pool.json")
	if err := os.WriteFile(path, []byte(`{"allocated":{"task-1":"10.0.0.2"}} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listAuditIPAllocations(path); err == nil {
		t.Fatal("listAuditIPAllocations accepted trailing JSON")
	}
}

func TestGCAuditJSONRequiresOrphansAndDryRun(t *testing.T) {
	for _, flags := range []struct {
		name                string
		orphans, dryRun, js bool
		wantErr             bool
	}{
		{name: "strict audit", orphans: true, dryRun: true, js: true},
		{name: "without orphans", dryRun: true, js: true, wantErr: true},
		{name: "without dry run", orphans: true, js: true, wantErr: true},
		{name: "plain gc", wantErr: false},
	} {
		t.Run(flags.name, func(t *testing.T) {
			err := validateGCAuditFlags(flags.orphans, flags.dryRun, flags.js)
			if (err != nil) != flags.wantErr {
				t.Fatalf("validateGCAuditFlags() error = %v, want error %t", err, flags.wantErr)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/obra/stockyard/pkg/config"
)

const auditTaskID = "a1b2c3d4"

func cleanOrphanAuditReaders() orphanAuditReaders {
	return orphanAuditReaders{
		listTasks: func(context.Context) ([]orphanAuditTask, error) {
			return []orphanAuditTask{{ID: auditTaskID, VMID: auditTaskID, Status: "running"}}, nil
		},
		listVMDirs: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
		},
		listProcesses: func(context.Context) ([]orphanAuditProcess, error) {
			return []orphanAuditProcess{{OwnerID: auditTaskID, PID: 123, Running: true}}, nil
		},
		listRootfsDatasets: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
		},
		listWorkspaceDatasets: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
		},
		listTaps: func(context.Context) ([]orphanAuditResource, error) {
			return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
		},
		listIPAllocations: func(context.Context) (map[string]string, error) {
			return map[string]string{auditTaskID: "10.0.100.2"}, nil
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

func TestOrphanAuditRequiresEveryOrdinaryDurableResource(t *testing.T) {
	tests := []struct {
		name string
		set  func(*orphanAuditReaders)
	}{
		{"VM directory", func(readers *orphanAuditReaders) {
			readers.listVMDirs = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		}},
		{"rootfs dataset", func(readers *orphanAuditReaders) {
			readers.listRootfsDatasets = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		}},
		{"workspace dataset", func(readers *orphanAuditReaders) {
			readers.listWorkspaceDatasets = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		}},
		{"IP allocation", func(readers *orphanAuditReaders) {
			readers.listIPAllocations = func(context.Context) (map[string]string, error) { return map[string]string{}, nil }
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readers := cleanOrphanAuditReaders()
			tt.set(&readers)
			var output bytes.Buffer
			if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
				t.Fatalf("runOrphanAudit accepted missing %s", tt.name)
			}
			if got := decodeOrphanAudit(t, output); len(got.Mismatches) == 0 {
				t.Fatalf("missing %s was not reported: %#v", tt.name, got)
			}
		})
	}
}

func TestOrphanAuditEnforcesStateSpecificProcessAndTapOwnership(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		processes []orphanAuditProcess
		taps      []orphanAuditResource
		wantErr   bool
	}{
		{name: "running requires both", status: "running", taps: []orphanAuditResource{{OwnerID: auditTaskID}}, wantErr: true},
		{name: "starting requires both", status: "starting", processes: []orphanAuditProcess{{OwnerID: auditTaskID, PID: 123, Running: true}}, wantErr: true},
		{name: "stopped requires neither", status: "stopped", processes: []orphanAuditProcess{{OwnerID: auditTaskID, PID: 123, Running: true}}, wantErr: true},
		{name: "failed permits neither", status: "failed"},
		{name: "failed permits process only", status: "failed", processes: []orphanAuditProcess{{OwnerID: auditTaskID, PID: 123, Running: true}}},
		{name: "failed permits tap only", status: "failed", taps: []orphanAuditResource{{OwnerID: auditTaskID}}},
		{name: "failed permits process and tap", status: "failed", processes: []orphanAuditProcess{{OwnerID: auditTaskID, PID: 123, Running: true}}, taps: []orphanAuditResource{{OwnerID: auditTaskID}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readers := cleanOrphanAuditReaders()
			readers.listTasks = func(context.Context) ([]orphanAuditTask, error) {
				return []orphanAuditTask{{ID: auditTaskID, VMID: auditTaskID, Status: tt.status}}, nil
			}
			readers.listProcesses = func(context.Context) ([]orphanAuditProcess, error) { return tt.processes, nil }
			readers.listTaps = func(context.Context) ([]orphanAuditResource, error) { return tt.taps, nil }
			var output bytes.Buffer
			err := runOrphanAudit(context.Background(), readers, &output)
			if (err != nil) != tt.wantErr {
				t.Fatalf("runOrphanAudit() error = %v, want error %t", err, tt.wantErr)
			}
		})
	}
}

func TestOrphanAuditCleanupPendingRequiresVerifiedAbsence(t *testing.T) {
	for _, set := range []func(*orphanAuditReaders){
		func(readers *orphanAuditReaders) {
			readers.listVMDirs = func(context.Context) ([]orphanAuditResource, error) {
				return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
			}
		},
		func(readers *orphanAuditReaders) {
			readers.listRootfsDatasets = func(context.Context) ([]orphanAuditResource, error) {
				return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
			}
		},
		func(readers *orphanAuditReaders) {
			readers.listWorkspaceDatasets = func(context.Context) ([]orphanAuditResource, error) {
				return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
			}
		},
		func(readers *orphanAuditReaders) {
			readers.listProcesses = func(context.Context) ([]orphanAuditProcess, error) {
				return []orphanAuditProcess{{OwnerID: auditTaskID, PID: 123, Running: true}}, nil
			}
		},
		func(readers *orphanAuditReaders) {
			readers.listTaps = func(context.Context) ([]orphanAuditResource, error) {
				return []orphanAuditResource{{OwnerID: auditTaskID}}, nil
			}
		},
	} {
		readers := cleanOrphanAuditReaders()
		readers.listTasks = func(context.Context) ([]orphanAuditTask, error) {
			return []orphanAuditTask{{ID: auditTaskID, VMID: auditTaskID, Status: "cleanup_pending"}}, nil
		}
		readers.listVMDirs = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		readers.listProcesses = func(context.Context) ([]orphanAuditProcess, error) { return nil, nil }
		readers.listRootfsDatasets = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		readers.listWorkspaceDatasets = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		readers.listTaps = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		set(&readers)
		var output bytes.Buffer
		if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
			t.Fatal("cleanup_pending accepted remaining resource")
		}
	}
}

func TestOrphanAuditRejectsDuplicateRuntimeOwnership(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*orphanAuditReaders)
	}{
		{
			name: "processes",
			set: func(readers *orphanAuditReaders) {
				readers.listProcesses = func(context.Context) ([]orphanAuditProcess, error) {
					return []orphanAuditProcess{{OwnerID: auditTaskID, PID: 123, Running: true}, {OwnerID: auditTaskID, PID: 456, Running: true}}, nil
				}
			},
		},
		{
			name: "taps",
			set: func(readers *orphanAuditReaders) {
				readers.listTaps = func(context.Context) ([]orphanAuditResource, error) {
					return []orphanAuditResource{{OwnerID: auditTaskID}, {OwnerID: auditTaskID}}, nil
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			readers := cleanOrphanAuditReaders()
			test.set(&readers)
			var output bytes.Buffer
			if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
				t.Fatalf("runOrphanAudit accepted duplicate %s ownership", test.name)
			}
		})
	}
}

func TestOrphanAuditReportsRunningOrphanProcess(t *testing.T) {
	readers := cleanOrphanAuditReaders()
	readers.listProcesses = func(context.Context) ([]orphanAuditProcess, error) {
		return []orphanAuditProcess{{OwnerID: "deadbeef", PID: 456, Running: true}}, nil
	}
	var output bytes.Buffer
	if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
		t.Fatal("runOrphanAudit accepted a running orphan process")
	}
	got := decodeOrphanAudit(t, output)
	found := false
	for _, mismatch := range got.Mismatches {
		if mismatch.Resource == "process" && mismatch.OwnerID == "deadbeef" && mismatch.Running {
			found = true
		}
	}
	if !found {
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
			if len(got.Mismatches) == 0 || len(got.UnknownReads) != 0 {
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

func TestListAuditIPAllocationsRejectsMissingInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_pool.json")
	cfg := config.DefaultConfig()
	if _, err := listAuditIPAllocations(path, cfg.Firecracker.VMSubnet, cfg.Firecracker.VMGateway); err == nil {
		t.Fatal("listAuditIPAllocations accepted a missing inventory for an empty fleet")
	}
}

func TestOrphanAuditTreatsMissingIPInventoryAsUnknownForAnyFleetSize(t *testing.T) {
	for _, zeroRows := range []bool{false, true} {
		readers := cleanOrphanAuditReaders()
		if zeroRows {
			readers.listTasks = func(context.Context) ([]orphanAuditTask, error) { return nil, nil }
			readers.listVMDirs = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
			readers.listProcesses = func(context.Context) ([]orphanAuditProcess, error) { return nil, nil }
			readers.listRootfsDatasets = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
			readers.listWorkspaceDatasets = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
			readers.listTaps = func(context.Context) ([]orphanAuditResource, error) { return nil, nil }
		}
		readers.listIPAllocations = func(context.Context) (map[string]string, error) { return nil, os.ErrNotExist }
		var output bytes.Buffer
		if err := runOrphanAudit(context.Background(), readers, &output); err == nil {
			t.Fatal("runOrphanAudit accepted a missing durable IP inventory")
		}
		got := decodeOrphanAudit(t, output)
		if len(got.UnknownReads) != 1 || got.UnknownReads[0].Source != "ip_allocations" {
			t.Fatalf("missing IP inventory result = %#v", got)
		}
	}
}

func TestListAuditIPAllocationsRejectsMalformedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ip_pool.json")
	if err := os.WriteFile(path, []byte(`{"allocated":{"task-1":"10.0.0.2"}} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if _, err := listAuditIPAllocations(path, cfg.Firecracker.VMSubnet, cfg.Firecracker.VMGateway); err == nil {
		t.Fatal("listAuditIPAllocations accepted trailing JSON")
	}
}

func TestParseAuditIPAllocationsRejectsDuplicateKeysAndInvalidAddresses(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, state := range []string{
		`{"allocated":{},"allocated":{}}`,
		`{"unexpected":true}`,
		`{"allocated":{"a1b2c3d4":"10.0.100.2","a1b2c3d4":"10.0.100.3"}}`,
		`{"allocated":{"a1b2c3d4":"10.0.100.2","deadbeef":"10.0.100.2"}}`,
		`{"allocated":{"a1b2c3d4":"10.0.100.1"}}`,
		`{"allocated":{"a1b2c3d4":"10.0.100.0"}}`,
		`{"allocated":{"a1b2c3d4":"10.0.100.255"}}`,
		`{"allocated":{"a1b2c3d4":"10.0.101.2"}}`,
		`{"allocated":{"a1b2c3d4":"2001:db8::1"}}`,
		`{"allocated":{"a1b2c3d4":"010.0.100.2"}}`,
	} {
		if _, err := parseAuditIPAllocations([]byte(state), cfg.Firecracker.VMSubnet, cfg.Firecracker.VMGateway); err == nil {
			t.Fatalf("parseAuditIPAllocations accepted %s", state)
		}
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

# Remove Git Repo Coupling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove `repo` and `ref` from stockyard's CLI, API, and internal data model since git repository management is not stockyard's responsibility.

**Architecture:** Delete `repo`/`ref` fields from the proto, CLI flags, daemon request structs, dashboard types, state/DB layer, and templates. We control the only running instance, so we can be aggressive: delete proto fields outright (no `reserved`), drop DB columns directly, and nuke the dashboard's "group by repo" view.

**Tech Stack:** Go, Protocol Buffers (protoc), SQLite, HTML templates

**Spec:** `docs/specs/remove-git-repo-coupling.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `api/stockyard.proto` | Modify | Remove `repo`/`ref` fields from `CreateTaskRequest` and `Task` |
| `pkg/api/v1/stockyard.pb.go` | Regenerate | Auto-generated from proto |
| `pkg/api/v1/stockyard_grpc.pb.go` | Regenerate | Auto-generated from proto |
| `cmd/stockyard/run.go` | Modify | Remove `--repo`/`--ref` flags and validation |
| `cmd/stockyard/list.go` | Modify | Remove REPO column from output |
| `pkg/daemon/tasks.go` | Modify | Remove `Repo`/`Ref` from `CreateTaskRequest`, metadata, activity feed calls |
| `pkg/daemon/grpc.go` | Modify | Remove `req.Repo`/`req.Ref` from gRPC handler, remove from `taskToProto` |
| `pkg/daemon/state.go` | Modify | Remove `Repo`/`Ref` from `Task` struct, drop DB columns, update all queries |
| `pkg/daemon/state_test.go` | Modify | Remove `Repo`/`Ref` from test task structs |
| `pkg/dashboard/daemon.go` | Modify | Remove `RepoURL`/`GitRef` from `Task`, `Repo`/`Ref` from `CreateTaskRequest` |
| `pkg/dashboard/adapter.go` | Modify | Remove `Repo`/`Ref` from `DaemonTask`, `DaemonCreateTaskRequest`, `convertTask` |
| `pkg/dashboard/server.go` | Modify | Remove repo validation, "group by repo" logic, repo fields from create handler |
| `pkg/dashboard/activity.go` | Modify | Remove `RepoURL` from `ActivityEvent`, update `VMStarted` signature |
| `pkg/dashboard/templates/fleet.html` | Modify | Remove repo column, "group by repo" tab, repo field from create form |
| `pkg/dashboard/templates/vm_detail.html` | Modify | Remove repo display |
| `pkg/dashboard/templates/vm_preview.html` | Modify | Remove repo display |
| `pkg/dashboard/templates/activity_panel.html` | Modify | Remove repo display |
| `pkg/dashboard/templates/activity.html` | Modify | Remove repo display |
| `pkg/dashboard/server_test.go` | Modify | Remove repo from test fixtures and assertions |
| `pkg/dashboard/adapter_test.go` | Modify | Remove repo from test fixtures and assertions |
| `pkg/dashboard/templates_test.go` | Modify | Remove repo from test data |
| `pkg/firecracker/cloudinit.go` | Check | Remove repo from `MMDSMetadata` if present |
| `docs/specs/remove-git-repo-coupling.md` | Update | Mark as IMPLEMENTED when done |

---

### Task 1: Update Proto Definition

**Files:**
- Modify: `api/stockyard.proto`

- [ ] **Step 1: Remove repo and ref from CreateTaskRequest**

In `api/stockyard.proto`, delete the `repo` and `ref` field lines. Renumber remaining fields starting from 1:

```protobuf
message CreateTaskRequest {
    string name = 1;
    repeated string command = 2;
    map<string, string> env = 3;
    int32 cpus = 4;
    string memory = 5;
    bool no_tailscale = 6;
    string tailscale_auth_key = 7;
    repeated string ssh_authorized_keys = 8;
}
```

- [ ] **Step 2: Remove repo and ref from Task message**

```protobuf
message Task {
    string id = 1;
    string name = 2;
    string status = 3;
    string tailscale_hostname = 4;
    string created_at = 5;
    string stopped_at = 6;
}
```

- [ ] **Step 3: Regenerate Go code**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && make proto`

- [ ] **Step 4: Commit**

```bash
git add api/stockyard.proto pkg/api/v1/stockyard.pb.go pkg/api/v1/stockyard_grpc.pb.go
git commit -m "proto: remove repo and ref fields from CreateTaskRequest and Task"
```

---

### Task 2: Update CLI

**Files:**
- Modify: `cmd/stockyard/run.go`
- Modify: `cmd/stockyard/list.go`

- [ ] **Step 1: Remove repo/ref flags and validation from run.go**

Remove the `runRepo` and `runRef` variables, the `--repo is required` check, the `ref` default logic, the `Repo`/`Ref` fields from the request, the status printf that references repo, and the flag registrations.

The updated `run.go` should:
- Remove `runRepo` and `runRef` var declarations
- Remove the `if runRepo == ""` check
- Remove the `ref` variable and default logic
- Remove `Repo` and `Ref` from the `CreateTaskRequest`
- Change the status printf from `"Creating task for %s@%s..."` to `"Creating task..."`
- Remove the two `runCmd.Flags().StringVar` calls for `repo` and `ref` from `init()`
- Update the command's `Long` example to not use `--repo`/`--ref`

- [ ] **Step 2: Remove REPO column from list.go**

Change the header from `"ID\tNAME\tREPO\tSTATUS\tCREATED"` to `"ID\tNAME\tSTATUS\tCREATED"` and remove `t.Repo` from the format string.

- [ ] **Step 3: Build to verify compilation**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && make build`
Expected: builds successfully with no errors

- [ ] **Step 4: Commit**

```bash
git add cmd/stockyard/run.go cmd/stockyard/list.go
git commit -m "cli: remove --repo and --ref flags"
```

---

### Task 3: Update Daemon Core

**Files:**
- Modify: `pkg/daemon/tasks.go`
- Modify: `pkg/daemon/grpc.go`
- Modify: `pkg/daemon/state.go`
- Modify: `pkg/daemon/state_test.go`

- [ ] **Step 1: Remove Repo/Ref from daemon CreateTaskRequest (tasks.go)**

In `pkg/daemon/tasks.go`:
- Remove `Repo` and `Ref` from the `CreateTaskRequest` struct
- Remove the `if req.Repo == ""` validation
- Remove the `if req.Ref == ""` default
- Remove `"repo": req.Repo` and `"ref": req.Ref` from the `Metadata` map in `vmCfg`
- Change `af.VMStarted(taskID, req.Name, req.Repo, "")` to `af.VMStarted(taskID, req.Name, "")`
- Change `af.VMStarted(taskID, task.Name, task.Repo, "")` to `af.VMStarted(taskID, task.Name, "")` in `RestartTask`

- [ ] **Step 2: Remove Repo/Ref from gRPC handler (grpc.go)**

In `pkg/daemon/grpc.go`:
- Remove `Repo: req.Repo` and `Ref: req.Ref` from the `CreateTask` call
- Remove `Repo: t.Repo` and `Ref: t.Ref` from `taskToProto`

- [ ] **Step 3: Remove Repo/Ref from state (state.go)**

In `pkg/daemon/state.go`:
- Remove `Repo` and `Ref` from the `Task` struct
- In `migrate()`: remove `repo` and `ref` from the `CREATE TABLE` schema. Add a migration to drop the columns from existing databases:

```go
// Drop repo/ref columns from existing databases
// SQLite 3.35.0+ supports DROP COLUMN
migrations := []string{
    // ... existing migrations ...
    `ALTER TABLE tasks DROP COLUMN repo`,
    `ALTER TABLE tasks DROP COLUMN ref`,
}
```

- In `CreateTask`: remove `repo` and `ref` from the INSERT query and values entirely
- In `GetTask`, `ListTasks`, `GetTaskByCID`: remove `repo` and `ref` from SELECT columns and scan calls

- [ ] **Step 4: Update state_test.go**

Remove `Repo` and `Ref` from all `Task` struct literals in tests. Remove the assertion that checks `got.Repo`. Remove `Repo: "repo1"` etc from `ListTasks` test data.

- [ ] **Step 5: Run tests**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/daemon/...`
Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
git add pkg/daemon/tasks.go pkg/daemon/grpc.go pkg/daemon/state.go pkg/daemon/state_test.go
git commit -m "daemon: remove repo/ref from task model and state layer"
```

---

### Task 4: Update Dashboard

**Files:**
- Modify: `pkg/dashboard/daemon.go`
- Modify: `pkg/dashboard/adapter.go`
- Modify: `pkg/dashboard/activity.go`
- Modify: `pkg/dashboard/server.go`
- Modify: `pkg/dashboard/adapter_test.go`
- Modify: `pkg/dashboard/server_test.go`
- Modify: `pkg/dashboard/templates_test.go`

- [ ] **Step 1: Update dashboard types (daemon.go)**

In `pkg/dashboard/daemon.go`:
- Remove `RepoURL` and `GitRef` from `Task` struct
- Remove `Repo` and `Ref` from `CreateTaskRequest` struct

- [ ] **Step 2: Update adapter (adapter.go)**

In `pkg/dashboard/adapter.go`:
- Remove `Repo` and `Ref` from `DaemonTask` struct
- Remove `Repo` and `Ref` from `DaemonCreateTaskRequest` struct
- Remove `RepoURL: dt.Repo` and `GitRef: dt.Ref` from `convertTask`
- Remove `Repo: req.Repo` and `Ref: req.Ref` from `CreateTask`

- [ ] **Step 3: Update activity feed (activity.go)**

In `pkg/dashboard/activity.go`:
- Remove `RepoURL` field from `ActivityEvent` struct
- Change `VMStarted` signature from `(taskID, taskName, repoURL, owner string)` to `(taskID, taskName, owner string)`
- Remove `RepoURL: repoURL` from the event construction

- [ ] **Step 4: Update server (server.go)**

In `pkg/dashboard/server.go`:
- Remove the "Group tasks by RepoURL" block (lines ~96-104) and `groupedByRepo` from the template data
- In the create task handler: remove `Repo`/`Ref` from the request struct, remove the `if req.Repo == ""` validation, remove the `if req.Ref == ""` default, remove `Repo`/`Ref` from the `CreateTaskRequest`

- [ ] **Step 5: Update tests**

In `pkg/dashboard/server_test.go`:
- Remove `RepoURL` from test task structs
- Remove `"repo"` from JSON request bodies (update to not require it)
- Remove assertions that check for repo

In `pkg/dashboard/adapter_test.go`:
- Remove `Repo`/`Ref` from test `DaemonTask` and `DaemonCreateTaskRequest` structs
- Remove assertions checking `RepoURL`/`Repo`/`Ref`

In `pkg/dashboard/templates_test.go`:
- Remove `"RepoURL"` from test data maps
- Remove `"github.com/test/repo"` grouping from template test data
- Remove assertion checking for repo in HTML output

- [ ] **Step 6: Run tests**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && go test ./pkg/dashboard/...`
Expected: all tests pass

- [ ] **Step 7: Commit**

```bash
git add pkg/dashboard/
git commit -m "dashboard: remove repo/ref from types, adapter, and server"
```

---

### Task 5: Update Dashboard Templates

**Files:**
- Modify: `pkg/dashboard/templates/fleet.html`
- Modify: `pkg/dashboard/templates/vm_detail.html`
- Modify: `pkg/dashboard/templates/vm_preview.html`
- Modify: `pkg/dashboard/templates/activity_panel.html`
- Modify: `pkg/dashboard/templates/activity.html`

- [ ] **Step 1: Update fleet.html**

- Remove the "repo" tab button and the `viewTab === 'repo'` section (the "grouped by repo" view)
- Remove the repo column (`<td>`) from task tables (appears twice — running and stopped sections)
- Remove `repo` and `ref` fields from the create VM form (`newVM.repo`, `newVM.ref`)
- Remove `repo: ''` and `ref: 'main'` from the Alpine.js `newVM` initialization
- Remove `repo: this.newVM.repo` from the form submission

- [ ] **Step 2: Update vm_detail.html**

Remove the repo display row (around line 493 where it shows `{{.Task.RepoURL}}`)

- [ ] **Step 3: Update vm_preview.html**

Remove `<p class="text-sm text-gray-500">{{.Task.RepoURL}}</p>`

- [ ] **Step 4: Update activity templates**

In `activity_panel.html` and `activity.html`:
Remove the `{{if .RepoURL}}` conditional blocks that display the repo URL

- [ ] **Step 5: Build and verify**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && make build`
Expected: builds successfully

- [ ] **Step 6: Commit**

```bash
git add pkg/dashboard/templates/
git commit -m "templates: remove repo/ref from all dashboard views"
```

---

### Task 6: Final Verification and Cleanup

**Files:**
- Modify: `docs/specs/remove-git-repo-coupling.md`

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && make test`
Expected: all tests pass

- [ ] **Step 2: Grep for any remaining repo/ref references**

Run: `grep -rn "\.Repo\b\|\.Ref\b\|runRepo\|runRef\|RepoURL\|GitRef" --include="*.go" /Users/matt/Code/mhat/prime/stockyard/`

Expected: no matches in source files (test fixtures may have been cleaned up already). Ignore matches in `docs/`, `vendor/`, or generated minified JS.

- [ ] **Step 3: Build all binaries**

Run: `cd /Users/matt/Code/mhat/prime/stockyard && make build`
Expected: all binaries build successfully

- [ ] **Step 4: Update spec status**

Change the status in `docs/specs/remove-git-repo-coupling.md` from `PROPOSED` to `IMPLEMENTED`.

- [ ] **Step 5: Final commit**

```bash
git add docs/specs/remove-git-repo-coupling.md
git commit -m "docs: mark remove-git-repo-coupling spec as implemented"
```

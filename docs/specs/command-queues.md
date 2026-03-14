# Command Queues

## Problem

Stockyard creates VMs and provides interactive access (SSH, dashboard terminal), but has no mechanism to programmatically execute commands inside a VM. The `Command` field in `CreateTaskRequest` is stored but never executed. An orchestrating agent needs to boot a VM, run setup commands, launch a coding agent, collect results, and tear down the VM — all programmatically.

The existing `Command` and `Env` fields in `CreateTaskRequest` are deprecated. Command execution is now handled by `QueueCommand` after the VM boots. The fields should be removed from the proto.

## Design

### Core Concept

A **queue** is a named, ordered list of commands managed by the daemon. Commands within a serial queue run sequentially. Different queues run independently of each other. The VM has no knowledge of queues — it just receives and executes individual commands over vsock connections.

### VM Side: stockyard-shell

stockyard-shell already listens on vsock port 52 and handles concurrent connections. Each connection opens a PTY, bridges I/O, and reports an exit code. The only change is to `OpenMessage`:

```json
{
  "user": "mooby",
  "term": "xterm",
  "cols": 80,
  "rows": 24,
  "command": ["claude", "-p", "implement OAuth"],
  "env": {"CLAUDE_MODEL": "opus"}
}
```

- `command` is required. No default behavior. The dashboard sends `["login", "-f", "mooby"]` explicitly for interactive shells.
- `env` is merged on top of the VM's system environment. Command env wins on conflict.
- `user` identifies the VM user for privilege dropping. stockyard-shell runs as root but must drop privileges so commands always execute as the VM user. The implementation must ensure this is secure — a security-focused review is required.
- `NewSession` execs the given command as the VM user instead of hardcoding `login -f`.
- All other protocol messages (`MsgData`, `MsgResize`, `MsgExit`, `MsgError`) are unchanged.

### Environment Layering

Two layers:

1. **VM env** — secrets and system config injected at VM creation via cloud-init (`ANTHROPIC_API_KEY`, `GITHUB_TOKEN`, etc.). Written to `/etc/profile.d/stockyard.sh`. Every process sees these. All queues, all commands.
2. **Command env** — per-command overrides sent in `QueueCommand`. Merged on top. Conflicts: command wins.


### Daemon Side: Queue Management

The daemon creates a `QueueManager` per task when the task starts. Two queues are created automatically:

- **`default`** — serial. For orchestrated work. Commands run sequentially.
- **`admin`** — concurrent. For interactive/debug shells. Never blocked by other work.

Both are protected and cannot be destroyed.

Callers can create additional named queues via gRPC with a chosen concurrency mode.

#### Queue Properties

- **`mode`** — either `serial` (commands run sequentially, one at a time) or `concurrent` (commands run in parallel). Default: `serial`. Set at creation time.
- **`protected`** — whether the queue can be destroyed (set by daemon for built-in queues).

#### Command Properties

- **`command`** — the command to execute (required).
- **`env`** — per-command environment overrides (optional).
- **`stop_on_failure`** — if this command exits non-zero, stop the queue (default: true). Per-command, not per-queue. Only meaningful in serial queues — concurrent queues ignore this flag since commands run independently.

#### Command Lifecycle

Each command transitions through: `pending` -> `running` -> `completed` / `failed`.

**Serial queues** (`mode: serial`):
1. Command is appended to the pending list.
2. If nothing is running, the daemon opens a vsock connection, sends `MsgOpen` with the command, and starts reading output.
3. Output is persisted to disk as it arrives.
4. On `MsgExit`, the daemon records the exit code.
5. If exit code is non-zero and `stop_on_failure` is true, the queue becomes `stopped`. Remaining pending commands stay pending.
6. Otherwise, the next pending command starts.

**Concurrent queues** (`mode: concurrent`):
1. Command is appended to the list.
2. The daemon immediately opens a vsock connection and starts the command, regardless of other running commands.
3. Failures are recorded but never stop the queue. `stop_on_failure` is ignored.

### Output Persistence

Each command's output is written to:

```
<data-dir>/tasks/<task-id>/commands/<command-id>/output.log
```

The daemon writes to this file as it receives `MsgData` from vsock. The file is the source of truth for all output access: streaming, CLI logs, dashboard display.

Stdout and stderr are muxed together (inherent to PTY). One interleaved stream, same as a terminal.

Command metadata (spec, status, exit code, timestamps, queue name) is stored in SQLite alongside existing task records.

### gRPC API

**Queue management:**

| RPC | Description |
|-----|-------------|
| `CreateQueue(task_id, queue_name, mode)` | Create a custom queue. Mode is `serial` (default) or `concurrent`. Fails if name exists. |
| `FlushQueue(task_id, queue_name)` | Clear pending commands from a queue. |
| `ResumeQueue(task_id, queue_name)` | Resume a stopped serial queue. Kicks off the next pending command. |
| `DestroyQueue(task_id, queue_name)` | Remove a queue. Fails for protected queues. |
| `ListQueues(task_id)` | List all queues with config and protected status. |

**Command execution:**

| RPC | Description |
|-----|-------------|
| `QueueCommand(task_id, queue_name, command[], env, stop_on_failure)` | Append a command to an existing queue. Returns command_id. |
| `GetCommandStatus(task_id, command_id)` | Status, exit code, output location. |
| `StreamCommandOutput(task_id, command_id)` | Server-side stream. Tails persisted output file. |
| `StreamQueueOutput(task_id, queue_name, follow)` | Server-side stream. Walks all commands in order, streaming each one's output. With follow, tails the running command and advances. |
| `GetQueueStatus(task_id, queue_name)` | Queue's command list with statuses. |

### CLI

```
# Execute commands
stockyard exec <task-id> -- go mod download
stockyard exec <task-id> --queue=admin -- login -f mooby
stockyard exec <task-id> --no-stop-on-failure -- make lint

# Queue management
stockyard queue create <task-id> test-suite --mode=concurrent
stockyard queue list <task-id>
stockyard queue status <task-id> default
stockyard queue flush <task-id> test-suite
stockyard queue destroy <task-id> test-suite

# Command inspection
stockyard command status <task-id> <command-id>
stockyard command logs <task-id> <command-id>
stockyard command logs <task-id> <command-id> --follow

# Queue output
stockyard queue tail <task-id> [queue-name]
stockyard queue tail <task-id> default --follow
stockyard queue resume <task-id> <queue-name>
```

## Typical Lifecycle

From an orchestrator's perspective:

```
1. CreateTask(name, cpus, memory, env)         -> task_id, VM boots
2. QueueCommand(task_id, "default",
     ["go", "mod", "download"], {})             -> command_id_1
3. QueueCommand(task_id, "default",
     ["claude", "-p", "implement OAuth"],
     {"CLAUDE_MODEL": "opus"})                  -> command_id_2
4. Poll GetCommandStatus / StreamCommandOutput
5. Retrieve artifacts from VM
6. DestroyTask(task_id)                         -> VM gone
```

Meanwhile, a developer debugging via the dashboard:
```
QueueCommand(task_id, "admin",
  ["login", "-f", "mooby"], {})                 -> independent, concurrent
```

The dashboard's terminal handler should be updated to route through `QueueCommand` on the `admin` queue rather than opening vsock connections directly. This gives consistent command tracking and output persistence for all sessions.

If `go mod download` fails and its `stop_on_failure` is true, `command_id_2` never runs. The orchestrator sees the failure via `GetCommandStatus` and decides what to do.

### Cleanup

When a task is destroyed (`DestroyTask`), all associated command state must be cleaned up:
- All queues and their command records removed from SQLite
- Command output files removed from disk (`<data-dir>/tasks/<task-id>/commands/`)
- Any active vsock connections closed

This extends the existing `DestroyTask` cleanup path, which already handles the VM, ZFS dataset, Tailscale node, and IP allocation.

## What This Design Does Not Cover

- **Artifact retrieval** — how to get files out of the VM after commands run. Currently possible via scp over Tailscale. A dedicated mechanism may come later.
- **Queue recovery after failure** — `ResumeQueue` flips a stopped serial queue back to active and kicks off the next pending command (skipping past the failure). The failed command stays in history.
- **Command cancellation** — killing a running command. Could be added by closing the vsock connection or sending a signal.
- **Queue-level env** — if needed, can be added later as a layer between VM env and command env.
- **State passing between commands** — how commands pass environment or state to subsequent commands. TBD.

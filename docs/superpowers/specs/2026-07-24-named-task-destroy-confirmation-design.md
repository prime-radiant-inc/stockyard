# Named Task Destruction Confirmation — Design

- **Date:** 2026-07-24
- **Status:** Approved design, pre-implementation

## Problem

`stockyard destroy <task-id> --force` currently destroys any task returned by
`GetTask`. Although the command fetches the complete task record before
destroying it, the command does not use the task's name in its safety decision.
An operator or script that selects the wrong row can therefore destroy a named
task with the same single `--force` acknowledgement used for an unnamed,
disposable task.

This is a command-line selection hazard. It is not a defect in the daemon's
task teardown lifecycle, and it does not require a new authorization boundary.

## Decisions

1. The guard applies only to the manual `stockyard destroy` CLI command.
   Daemon RPCs, garbage collection, the dashboard, and client-library callers
   keep their existing behavior.
2. `--force` remains the universal mutation gate.
3. Destroying a named task additionally requires
   `--confirm-name <exact-task-name>`.
4. The confirmation value must match the stored task name byte-for-byte. The
   CLI does not trim whitespace or perform case folding.
5. Unnamed tasks keep the current `--force` behavior.
6. No protobuf, daemon, persistence, or client-library changes are required.

## CLI Contract

| Task | Invocation | Result |
|---|---|---|
| Any task | no `--force` | Show a preview; do not call `DestroyTask` |
| Unnamed task | `--force` | Destroy the task |
| Named task | `--force` | Refuse with a nonzero error; do not call `DestroyTask` |
| Named task | `--force --confirm-name <wrong-name>` | Refuse with a nonzero error; do not call `DestroyTask` |
| Named task | `--force --confirm-name <exact-name>` | Destroy the task |

`--confirm-name` without `--force` does not permit destruction: the command
still takes the preview-only path. For an unnamed task, `--confirm-name` is not
part of the safety decision; `--force` remains sufficient.

## Command Flow

The command continues to fetch the task before deciding whether to destroy it.
After confirming that the task exists:

1. Without `--force`, print the task preview and return without mutation.
   - For a named task, include the exact stored name and a shell-quoted example
     containing both `--force` and `--confirm-name`.
   - For an unnamed task, retain the existing `--force` guidance.
2. With `--force`, inspect the stored task name.
   - If the name is nonempty and `--confirm-name` is absent or differs from the
     stored value, return a clear error before issuing a destroy RPC.
   - Otherwise, call `DestroyTask` exactly as the command does today.

Task names are immutable, and task IDs are not intentionally reused. The
existing `GetTask` followed by `DestroyTask` sequence is therefore sufficient
for this CLI guard; moving the confirmation into the daemon would broaden the
change without strengthening the operator selection check.

## Implementation Boundary

The change belongs in `cmd/stockyard/destroy.go`. The command will adopt the
existing injectable command-factory pattern used by other Stockyard CLI
commands so its behavior can be tested through a fake gRPC service. Flag state
will be owned by the command instance rather than shared between tests.

The daemon remains the single owner of task teardown. The CLI only decides
whether it has enough operator confirmation to invoke that existing operation.

## Error Handling

- Missing or mismatched confirmation for a named task produces a nonzero CLI
  error that identifies the task and states that `--confirm-name` must match
  exactly.
- A refusal happens before `DestroyTask`, so a confirmation error has no
  partial-effects or cleanup path.
- Existing task lookup and daemon destruction errors continue to propagate.
- Names shown in guidance are shell-quoted so whitespace and punctuation are
  unambiguous.

## Testing

Command tests will run against a fake in-memory gRPC service and record whether
`DestroyTask` was called. They will cover:

- named and unnamed preview paths do not destroy;
- an unnamed task with `--force` does destroy;
- a named task with `--force` but no confirmation does not destroy;
- a named task with a mismatched confirmation does not destroy;
- a named task with an exact confirmation does destroy;
- `--confirm-name` without `--force` remains non-mutating; and
- lookup failures remain non-mutating and return their error.

The tests will assert RPC behavior and returned errors directly. Output checks
will be limited to the public guidance contract rather than matching the full
rendered command text.

## Non-Goals

- Adding confirmation requirements to the daemon or protobuf API.
- Changing garbage collection or dashboard destruction.
- Introducing an interactive prompt.
- Renaming tasks or changing task-name validation.
- Adding a second confirmation step for unnamed tasks.

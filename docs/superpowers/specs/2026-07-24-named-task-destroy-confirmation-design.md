# Named Task Destruction Confirmation — Design

- **Date:** 2026-07-24
- **Status:** Approved design; staff-review amendments incorporated; awaiting final review

## Problem

`stockyard destroy <task-id> --force` currently destroys any task returned by
`GetTask`. Although the command fetches the complete task record before
destroying it, the command does not use the task's name in its safety decision.
An operator or script that selects the wrong row can therefore destroy a named
task with the same single `--force` acknowledgement used for an unnamed,
disposable task. Requiring the expected name adds a useful cognitive
cross-check when that name comes from the operator's intent rather than from
the same selection that produced the task ID.

This is a command-line selection hazard. It is not a defect in the daemon's
task teardown lifecycle, and it does not require a new authorization boundary.

## Decisions

1. The guard applies to every invocation of the `stockyard destroy` CLI
   command, whether interactive or scripted. Daemon RPCs, garbage collection,
   the dashboard, image-registry teardown, and client-library callers keep
   their existing behavior.
2. `--force` remains the universal mutation gate.
3. Destroying a named task additionally requires
   `--confirm-name <exact-task-name>`.
4. The confirmation value must match the stored task name byte-for-byte. The
   CLI does not trim whitespace or perform case folding.
5. Unnamed tasks keep the current `--force` behavior, but an explicitly
   supplied `--confirm-name` on an unnamed task refuses instead of being
   ignored.
6. The CLI verifies that `GetTask` returned the exact requested task ID before
   trusting the returned name.
7. This is a cognitive selection check, not authorization, a unique-identity
   proof, or an atomic compare-and-destroy operation.
8. No protobuf, daemon, persistence, name-validation, or client-library
   changes are required.

## CLI Contract

| Task | Invocation | Result |
|---|---|---|
| Any task | no `--force` | Show a preview; do not call `DestroyTask` |
| Unnamed task | `--force` | Destroy the task |
| Unnamed task | `--force --confirm-name <any-value>` | Refuse with a nonzero error; do not call `DestroyTask` |
| Named task | `--force` | Refuse with a nonzero error; do not call `DestroyTask` |
| Named task | `--force --confirm-name <wrong-name>` | Refuse with a nonzero error; do not call `DestroyTask` |
| Named task | `--force --confirm-name <exact-name>` | Destroy the task |

`--confirm-name` without `--force` does not permit destruction: the command
still takes the preview-only path. For an unnamed task, `--force` alone remains
sufficient, but an explicitly supplied `--confirm-name` fails closed: the flag
asserts an expected name the task does not have, so the command refuses rather
than silently ignore it.

## Confirmation Semantics

Task names are human-readable labels, not unique identifiers. The confirmation
is useful when a human or script supplies an independently known expected name.
It cannot detect a selection error when:

- the confirmation name was copied from the same selected record as the ID;
- multiple tasks share the same name; or
- the intended and selected tasks have the same name.

Those are accepted limitations of a CLI-only cognitive check. The task ID
remains the authoritative target passed to `DestroyTask`; the name adds a
second operator assertion but does not replace or strengthen that identity.

## Name Rendering and Shell Guidance

Task names are currently unrestricted. They may contain whitespace,
punctuation, shell metacharacters, control characters, or—when created by a
non-CLI client—a NUL byte. The destroy command must not print a stored name raw
into a terminal or treat a diagnostic representation as executable shell text.

- Diagnostic output uses `strconv.QuoteToASCII` as a terminal-safe
  representation, so control bytes and Unicode formatting controls cannot
  alter the display.
- For names containing only printable runes, executable guidance quotes the
  name as one POSIX shell argument: surround the value with single quotes and
  replace each embedded single quote with `'"'"'`. Show a pasteable flag suffix
  in `--force --confirm-name=<quoted-value>` form so leading dashes remain
  unambiguous. Tell the operator to add that suffix to the same invocation;
  never reconstruct a complete command that could omit root-scoped connection
  selectors such as `--url`.
- Go `%q` and `strconv.Quote` are not shell escaping and must not be used to
  produce a pasteable command.
- For a name containing NUL or any non-printing/control rune, print only the
  terminal-safe diagnostic form and omit a pasteable command. If the exact
  stored value cannot be supplied through process arguments, the CLI remains
  fail-closed; the deliberately unchanged non-CLI destruction paths remain
  available.

## Command Flow

The command continues to fetch the task before deciding whether to destroy it.
After confirming that the task exists:

1. Verify that the returned task ID exactly matches the requested task ID. A
   nil task or mismatched response is an error and cannot reach `DestroyTask`.
2. Without `--force`, print the task preview and return without mutation.
   - For a named task, include the terminal-safe stored-name representation.
     When the name is safe to render as executable guidance, include the flag
     suffix containing both `--force` and `--confirm-name`.
   - For an unnamed task, retain the existing `--force` guidance.
3. With `--force`, inspect the stored task name.
   - If the name is empty and `--confirm-name` was explicitly supplied, return
     a clear error before issuing a destroy RPC.
   - If the name is nonempty and `--confirm-name` is absent or differs from the
     stored value, return a clear error before issuing a destroy RPC.
   - Otherwise, call `DestroyTask` exactly once with the requested task ID.

## Implementation Boundary

The change belongs in `cmd/stockyard/destroy.go`. The command will adopt the
existing injectable command-factory pattern used by other Stockyard CLI
commands so its behavior can be tested through a fake gRPC service. Flag state
will be owned by the command instance rather than shared between tests. RPCs
will use `cmd.Context()`, and operator output will use Cobra's configured output
writer rather than process-global stdout.

The daemon remains the single owner of task teardown. The CLI only decides
whether it has enough operator confirmation to invoke that existing operation.

The command's long help and `--confirm-name` flag help will explain the
conditional requirement. A concise README section will show named and unnamed
destruction examples and tell existing scripts that `--force` alone now fails
closed for named tasks. Dated design and implementation plans remain historical
records and are not rewritten.

## Concurrency Boundary

`GetTask` and `DestroyTask` are separate RPCs. Task names are immutable in the
current model, but a different caller can destroy the row after the CLI lookup,
and a later task could theoretically reuse the eight-character ID before the
CLI's destroy request arrives. The confirmation guard does not make that
sequence atomic.

This low-probability replacement race is accepted for the operator-selection
goal. A true atomic identity guarantee would require an immutable per-creation
token or expected value in the daemon protocol and a comparison under the
daemon's task lock, which is outside this CLI-only change.

## Error Handling

- Missing or mismatched confirmation for a named task produces a nonzero CLI
  error that identifies the task using terminal-safe output and states that
  `--confirm-name` must match exactly.
- An explicitly supplied `--confirm-name` for an unnamed task produces a
  nonzero CLI error stating that the task has no name.
- A nil task or a response whose task ID differs from the requested ID is an
  error and cannot reach `DestroyTask`.
- A refusal happens before `DestroyTask`, so a confirmation error has no
  partial-effects or cleanup path.
- Existing task lookup and daemon destruction errors continue to propagate.
- A daemon destruction error is never followed by a success message.
- Non-printing names receive no pasteable guidance. If an exact value cannot be
  supplied through process arguments, notably when it contains NUL, the name
  comparison remains fail-closed.

## Testing

Command tests will run against a fake in-memory gRPC service. The fake records
every lookup and destroy request so tests can assert exact task IDs and call
counts, not merely whether destruction happened. They will cover:

- named and unnamed preview paths do not destroy;
- an unnamed task with `--force` does destroy;
- an unnamed task with an explicitly supplied `--confirm-name` refuses before
  any destroy RPC;
- a named task with `--force` but no confirmation does not destroy;
- a named task with a mismatched confirmation does not destroy;
- case-only and trailing-whitespace mismatches do not destroy;
- a named task with an exact confirmation, including printable whitespace,
  destroys exactly once with the requested ID;
- `--confirm-name` without `--force` remains non-mutating;
- lookup failures, nil tasks, and mismatched response IDs remain non-mutating;
- a daemon destruction error records one attempted destroy, returns the error,
  and emits no completion message;
- missing or extra task IDs fail before client creation;
- flags before and after the task ID, dash-prefixed confirmation values,
  missing flag values, and the `--` terminator cannot bypass argument
  validation; and
- a newly constructed command does not inherit another command's `--force` or
  `--confirm-name` state.

The shell-guidance helper will be round-trip tested as one argument through a
POSIX shell for spaces, single quotes, `$()`, backticks, leading dashes, and
printable Unicode. The preview test will assert that guidance is a flag suffix,
not a reconstructed command that can lose root flags. Control characters,
Unicode formatting controls, and NUL will be tested to ensure diagnostic
output contains no raw terminal controls and offers no pasteable suffix.

The tests will assert RPC behavior and returned errors directly. Output checks
will be limited to the public guidance contract rather than matching the full
rendered command text.

## Accepted Residual Risks

- A script can defeat the cognitive benefit by deriving both the task ID and
  confirmation name from the same selected row.
- Duplicate task names make the name assertion non-discriminating between
  those tasks.
- The separate lookup and destroy RPCs do not prevent the theoretical
  delete-and-ID-reuse race described above.
- Direct RPC, dashboard, garbage-collection, and image-registry destruction
  paths intentionally do not enforce this CLI confirmation.
- Existing `stockyard list` name rendering is outside this change; the destroy
  preview itself must remain terminal-safe.

## Non-Goals

- Adding confirmation requirements to the daemon or protobuf API.
- Changing garbage collection or dashboard destruction.
- Introducing an interactive prompt.
- Enforcing unique task names or changing task-name validation.
- Making task destruction an atomic conditional daemon operation.
- Reworking the `stockyard list` output format.
- Adding a second confirmation step for unnamed tasks.

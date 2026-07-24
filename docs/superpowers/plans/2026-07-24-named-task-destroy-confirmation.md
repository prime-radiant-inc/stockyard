# Named Task Destroy Confirmation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every `stockyard destroy` invocation require both `--force` and an exact `--confirm-name` value before it can destroy a named task, while preserving the current `--force` behavior for unnamed tasks.

**Architecture:** Refactor the destroy command behind an injected-client command factory with per-instance flag state. The CLI will bind the fetched task to the requested ID, render names safely, compare confirmation bytes locally, and call the unchanged destroy RPC only after all guards pass. Bufconn-backed command tests will record exact lookup and destroy requests; the daemon, protobuf, persistence model, and other destruction callers remain untouched.

**Tech Stack:** Go 1.25, Cobra 1.10.2, gRPC 1.68 with `bufconn`, and Go standard-library quoting, Unicode, and test packages.

## Global Constraints

- Change only the `stockyard destroy` CLI, its tests, shared test transport, current CLI help, and README documentation.
- Named tasks require both `--force` and `--confirm-name`; unnamed tasks continue to require only `--force`.
- Do not add a compatibility alias, fallback, or bypass that lets a named task proceed with `--force` alone.
- Compare the confirmation to the stored name byte-for-byte. Do not trim, normalize, or case-fold.
- Verify `GetTask` returned a non-nil task whose ID exactly equals the requested ID before trusting its name.
- Use `strconv.QuoteToASCII` for terminal-safe name diagnostics.
- Generate pasteable guidance only for valid UTF-8 names containing printable runes. Quote the entire value with POSIX single quotes and encode an embedded single quote as `'"'"'`.
- Show only the suffix `--force --confirm-name=<quoted-value>` to add to the same invocation. Never reconstruct a complete command that could omit `--url` or another root-scoped selector.
- For NUL, control, formatting-control, or invalid UTF-8 names, show the escaped diagnostic but no pasteable suffix. If the exact value cannot be supplied through argv, remain fail-closed.
- Treat this as a cognitive selection check, not authorization, unique identity, or atomic compare-and-destroy.
- Do not change protobufs, daemon behavior, task-name validation or uniqueness, `stockyard list`, garbage collection, dashboard deletion, image-registry deletion, or the client library.
- Add no dependency; the quoting and rendering implementation stays in the Go standard library.
- Preserve the user-owned untracked `AGENTS.md`; never stage it.

---

## File Map

- Modify `cmd/stockyard/destroy.go`: safe display/shell helpers, injected command factory, confirmation guard, contextual RPC calls, Cobra output, and command help.
- Create `cmd/stockyard/destroy_test.go`: helper contract tests and command-level behavioral matrix.
- Create `cmd/stockyard/grpc_test.go`: reusable bufconn client fixture for CLI command tests.
- Modify `cmd/stockyard/task_presence_test.go`: consume the generic bufconn fixture instead of its task-presence-specific copy.
- Modify `README.md`: document named and unnamed destruction and the intentional script migration.

### Task 1: Add Safe Name Rendering and POSIX Argument Quoting

**Files:**
- Modify: `cmd/stockyard/destroy.go:4-9`
- Create: `cmd/stockyard/destroy_test.go`

**Interfaces:**
- Consumes: Go standard library only.
- Produces: `quoteTaskNameForDisplay(name string) string` and `quotePOSIXShellArgument(value string) (string, bool)` for the destroy command and later command tests.

- [ ] **Step 1: Write failing helper-contract tests**

Create `cmd/stockyard/destroy_test.go` with:

```go
package main

import (
	"os/exec"
	"strconv"
	"testing"
	"unicode"
)

func TestQuoteTaskNameForDisplayIsTerminalSafeAndReversible(t *testing.T) {
	inputs := []string{
		"production",
		"release candidate",
		"line\nbreak",
		"\x1b[31mred",
		"rtl\u202etext",
		"nul\x00name",
		"café",
	}

	for _, input := range inputs {
		got := quoteTaskNameForDisplay(input)
		for _, r := range got {
			if !unicode.IsPrint(r) {
				t.Fatalf("display %q contains non-printing rune %U", got, r)
			}
		}
		decoded, err := strconv.Unquote(got)
		if err != nil {
			t.Fatalf("unquote display %q: %v", got, err)
		}
		if decoded != input {
			t.Fatalf("display round trip = %q, want %q", decoded, input)
		}
	}
}

func TestQuotePOSIXShellArgumentRoundTripsOneArgument(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "space", value: "release candidate"},
		{name: "single quote", value: "it's ready"},
		{name: "command substitution", value: "$(printf injected)"},
		{name: "backticks", value: "`printf injected`"},
		{name: "leading dash", value: "-production"},
		{name: "unicode", value: "café"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quoted, ok := quotePOSIXShellArgument(tt.value)
			if !ok {
				t.Fatalf("quotePOSIXShellArgument(%q) rejected printable input", tt.value)
			}
			script := "set -- " + quoted + "\nprintf '%s\\n' \"$#\"\nprintf '%s' \"$1\""
			output, err := exec.Command("sh", "-c", script).Output()
			if err != nil {
				t.Fatalf("shell round trip: %v", err)
			}
			want := "1\n" + tt.value
			if string(output) != want {
				t.Fatalf("shell output = %q, want %q", output, want)
			}
		})
	}
}

func TestQuotePOSIXShellArgumentRejectsUnsafeDisplayValues(t *testing.T) {
	values := []string{
		"line\nbreak",
		"tab\tvalue",
		"\x1b[31mred",
		"rtl\u202etext",
		"nul\x00name",
		string([]byte{0xff}),
	}

	for _, value := range values {
		if quoted, ok := quotePOSIXShellArgument(value); ok {
			t.Fatalf("quotePOSIXShellArgument(%q) = %q, want refusal", value, quoted)
		}
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the red state**

Run:

```bash
go test ./cmd/stockyard -run 'TestQuote(TaskNameForDisplay|POSIXShellArgument)' -count=1
```

Expected: build failure because `quoteTaskNameForDisplay` and `quotePOSIXShellArgument` are undefined.

- [ ] **Step 3: Implement the two minimal helpers**

Extend the `cmd/stockyard/destroy.go` import block with `strconv`, `strings`, `unicode`, and `unicode/utf8`, then add:

```go
func quoteTaskNameForDisplay(name string) string {
	return strconv.QuoteToASCII(name)
}

func quotePOSIXShellArgument(value string) (string, bool) {
	if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", false
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'", true
}
```

- [ ] **Step 4: Format and rerun the helper tests**

Run:

```bash
gofmt -w cmd/stockyard/destroy.go cmd/stockyard/destroy_test.go
go test ./cmd/stockyard -run 'TestQuote(TaskNameForDisplay|POSIXShellArgument)' -count=1
```

Expected: PASS. The shell tests must return the original value as exactly one argv element without evaluating `$()` or backticks.

- [ ] **Step 5: Commit the helper contract**

Run:

```bash
git status --short
git add cmd/stockyard/destroy.go cmd/stockyard/destroy_test.go
git commit -m "Quote destroy confirmation names safely" -m "Add terminal-safe diagnostic rendering and a narrowly scoped POSIX single-argument quoting helper. Reject control, formatting-control, NUL, and invalid UTF-8 values from pasteable guidance while preserving an escaped diagnostic representation."
```

Expected: one commit containing only the two listed files; `AGENTS.md` remains untracked.

### Task 2: Make the Destroy Command Injectable and Fail Closed on Bad Task Identity

**Files:**
- Modify: `cmd/stockyard/destroy.go:11-56`
- Modify: `cmd/stockyard/destroy_test.go`
- Create: `cmd/stockyard/grpc_test.go`
- Modify: `cmd/stockyard/task_presence_test.go:3-51,102`

**Interfaces:**
- Consumes: `quoteTaskNameForDisplay` and `quotePOSIXShellArgument` from Task 1, though the command does not call them until Task 3.
- Produces: `newDestroyCommand(newClient func() (*client.Client, error)) *cobra.Command` with per-instance `--force` state; `newStockyardTestClient(t *testing.T, server pb.StockyardServer) *client.Client` for command tests.

- [ ] **Step 1: Extract the existing bufconn fixture without changing behavior**

Create `cmd/stockyard/grpc_test.go`:

```go
package main

import (
	"context"
	"net"
	"testing"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

const stockyardTestBufSize = 1024 * 1024

func newStockyardTestClient(t *testing.T, server pb.StockyardServer) *client.Client {
	t.Helper()
	listener := bufconn.Listen(stockyardTestBufSize)
	grpcServer := grpc.NewServer()
	pb.RegisterStockyardServer(grpcServer, server)
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			t.Errorf("serve bufconn gRPC: %v", err)
		}
	}()
	t.Cleanup(grpcServer.Stop)

	c, err := client.NewWithDialer("passthrough:///bufnet", func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	})
	if err != nil {
		t.Fatalf("new bufconn client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
```

In `cmd/stockyard/task_presence_test.go`, remove the `net`, `grpc`, and `bufconn` imports, delete `taskPresenceBufSize` and `newTaskPresenceClient`, and change the existing call to:

```go
err := writeTaskPresence(context.Background(), newStockyardTestClient(t, tt.server), "t-123", &output)
```

- [ ] **Step 2: Verify the green refactor**

Run:

```bash
gofmt -w cmd/stockyard/grpc_test.go cmd/stockyard/task_presence_test.go
go test ./cmd/stockyard -run 'TestTaskPresence' -count=1
```

Expected: PASS with no behavior change.

- [ ] **Step 3: Add failing command-factory and identity tests**

Extend `cmd/stockyard/destroy_test.go` imports with `bytes`, `context`, `slices`, `strings`, `sync`, the generated protobuf package, the client package, and gRPC status packages. Add:

```go
type destroyCommandServer struct {
	pb.UnimplementedStockyardServer

	mu         sync.Mutex
	task       *pb.Task
	getErr     error
	destroyErr error
	getIDs     []string
	destroyIDs []string
}

func (s *destroyCommandServer) GetTask(_ context.Context, req *pb.GetTaskRequest) (*pb.GetTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getIDs = append(s.getIDs, req.GetTaskId())
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &pb.GetTaskResponse{Task: s.task}, nil
}

func (s *destroyCommandServer) DestroyTask(_ context.Context, req *pb.DestroyTaskRequest) (*pb.DestroyTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroyIDs = append(s.destroyIDs, req.GetTaskId())
	if s.destroyErr != nil {
		return nil, s.destroyErr
	}
	return &pb.DestroyTaskResponse{}, nil
}

func (s *destroyCommandServer) requestIDs() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.getIDs), slices.Clone(s.destroyIDs)
}

func newDestroyTestCommand(t *testing.T, server pb.StockyardServer) *cobra.Command {
	t.Helper()
	cmd := newDestroyCommand(func() (*client.Client, error) {
		return newStockyardTestClient(t, server), nil
	})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return cmd
}

func executeDestroyCommand(t *testing.T, server pb.StockyardServer, args ...string) (string, error) {
	t.Helper()
	cmd := newDestroyTestCommand(t, server)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return output.String(), err
}

func TestDestroyCommandUnnamedPaths(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantDestroyIDs []string
		wantOutput     string
	}{
		{
			name:       "preview",
			args:       []string{"task-123"},
			wantOutput: "Run with --force to confirm.",
		},
		{
			name:           "force after id",
			args:           []string{"task-123", "--force"},
			wantDestroyIDs: []string{"task-123"},
			wantOutput:     "Task destroyed.",
		},
		{
			name:           "force before id",
			args:           []string{"--force", "task-123"},
			wantDestroyIDs: []string{"task-123"},
			wantOutput:     "Task destroyed.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &destroyCommandServer{task: &pb.Task{Id: "task-123"}}
			output, err := executeDestroyCommand(t, server, tt.args...)
			if err != nil {
				t.Fatalf("execute destroy: %v", err)
			}
			getIDs, destroyIDs := server.requestIDs()
			if !slices.Equal(getIDs, []string{"task-123"}) {
				t.Fatalf("get IDs = %v, want [task-123]", getIDs)
			}
			if !slices.Equal(destroyIDs, tt.wantDestroyIDs) {
				t.Fatalf("destroy IDs = %v, want %v", destroyIDs, tt.wantDestroyIDs)
			}
			if !strings.Contains(output, tt.wantOutput) {
				t.Fatalf("output %q does not contain %q", output, tt.wantOutput)
			}
		})
	}
}

func TestDestroyCommandRejectsInvalidTaskResponses(t *testing.T) {
	tests := []struct {
		name   string
		server *destroyCommandServer
	}{
		{
			name:   "lookup error",
			server: &destroyCommandServer{getErr: status.Error(codes.Internal, "lookup failed")},
		},
		{
			name:   "nil task",
			server: &destroyCommandServer{},
		},
		{
			name:   "mismatched task id",
			server: &destroyCommandServer{task: &pb.Task{Id: "other-task"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := executeDestroyCommand(t, tt.server, "task-123", "--force"); err == nil {
				t.Fatal("destroy succeeded")
			}
			getIDs, destroyIDs := tt.server.requestIDs()
			if !slices.Equal(getIDs, []string{"task-123"}) {
				t.Fatalf("get IDs = %v, want [task-123]", getIDs)
			}
			if len(destroyIDs) != 0 {
				t.Fatalf("destroy IDs = %v, want none", destroyIDs)
			}
		})
	}
}

func TestDestroyCommandReturnsDestroyErrorWithoutSuccess(t *testing.T) {
	server := &destroyCommandServer{
		task:       &pb.Task{Id: "task-123"},
		destroyErr: status.Error(codes.Internal, "destroy failed"),
	}
	output, err := executeDestroyCommand(t, server, "task-123", "--force")
	if err == nil {
		t.Fatal("destroy succeeded")
	}
	_, destroyIDs := server.requestIDs()
	if !slices.Equal(destroyIDs, []string{"task-123"}) {
		t.Fatalf("destroy IDs = %v, want [task-123]", destroyIDs)
	}
	if strings.Contains(output, "Task destroyed.") {
		t.Fatalf("failure output contains success message: %q", output)
	}
}

func TestDestroyCommandValidatesArgumentsBeforeClientCreation(t *testing.T) {
	cases := [][]string{
		{},
		{"task-123", "extra"},
		{"--force"},
	}

	for _, args := range cases {
		clientCreations := 0
		cmd := newDestroyCommand(func() (*client.Client, error) {
			clientCreations++
			return nil, nil
		})
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("args %v succeeded", args)
		}
		if clientCreations != 0 {
			t.Fatalf("args %v created %d clients, want 0", args, clientCreations)
		}
	}
}

func TestDestroyCommandUsesCommandContext(t *testing.T) {
	server := &destroyCommandServer{task: &pb.Task{Id: "task-123"}}
	cmd := newDestroyTestCommand(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"task-123", "--force"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("destroy succeeded with canceled command context")
	}
	_, destroyIDs := server.requestIDs()
	if len(destroyIDs) != 0 {
		t.Fatalf("destroy IDs = %v, want none", destroyIDs)
	}
}

func TestDestroyCommandFlagStateIsPerInstance(t *testing.T) {
	server := &destroyCommandServer{task: &pb.Task{Id: "task-123"}}
	if _, err := executeDestroyCommand(t, server, "task-123", "--force"); err != nil {
		t.Fatalf("forced destroy: %v", err)
	}
	output, err := executeDestroyCommand(t, server, "task-123")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	_, destroyIDs := server.requestIDs()
	if !slices.Equal(destroyIDs, []string{"task-123"}) {
		t.Fatalf("destroy IDs = %v, want one forced call", destroyIDs)
	}
	if !strings.Contains(output, "Run with --force to confirm.") {
		t.Fatalf("fresh command did not take preview path: %q", output)
	}
}
```

The resulting import block in `destroy_test.go` must contain:

```go
import (
	"bytes"
	"context"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"

	pb "github.com/obra/stockyard/pkg/api/v1"
	"github.com/obra/stockyard/pkg/client"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)
```

- [ ] **Step 4: Run the new tests and verify the red state**

Run:

```bash
go test ./cmd/stockyard -run 'TestDestroyCommand' -count=1
```

Expected: build failure because `newDestroyCommand` is undefined.

- [ ] **Step 5: Refactor the command behind the injected factory**

In `cmd/stockyard/destroy.go`, import `github.com/obra/stockyard/pkg/client`, remove the `context` import and package-level `destroyForce`, and replace the command declaration and flag registration with:

```go
func newDestroyCommand(newClient func() (*client.Client, error)) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "destroy <task-id>",
		Short: "Destroy a task and its workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]

			c, err := newClient()
			if err != nil {
				return err
			}
			defer c.Close()

			task, err := c.GetTask(cmd.Context(), taskID)
			if err != nil {
				return fmt.Errorf("failed to get task: %w", err)
			}
			if task == nil {
				return fmt.Errorf("get task %q returned an empty task", taskID)
			}
			if task.GetId() != taskID {
				return fmt.Errorf("get task %q returned task %q", taskID, task.GetId())
			}

			output := cmd.OutOrStdout()
			if !force {
				fmt.Fprintf(output, "About to destroy task %s:\n", taskID)
				fmt.Fprintln(output, "\nThis will delete the VM and all workspace data.")
				fmt.Fprintln(output, "Run with --force to confirm.")
				return nil
			}

			fmt.Fprintf(output, "Destroying task %s...\n", taskID)
			if err := c.DestroyTask(cmd.Context(), taskID); err != nil {
				return fmt.Errorf("failed to destroy task: %w", err)
			}
			fmt.Fprintln(output, "Task destroyed.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force destruction")
	return cmd
}

var destroyCmd = newDestroyCommand(getClient)

func init() {
	rootCmd.AddCommand(destroyCmd)
}
```

- [ ] **Step 6: Format and run focused command tests**

Run:

```bash
gofmt -w cmd/stockyard/destroy.go cmd/stockyard/destroy_test.go cmd/stockyard/grpc_test.go cmd/stockyard/task_presence_test.go
go test ./cmd/stockyard -run 'Test(DestroyCommand|TaskPresence)' -count=1
```

Expected: PASS. Refusal and malformed-response paths record zero destroy calls; successful unnamed force records exactly one call for `task-123`.

- [ ] **Step 7: Run the complete CLI package tests**

Run:

```bash
go test ./cmd/stockyard -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit the command seam and identity binding**

Run:

```bash
git status --short
git add cmd/stockyard/destroy.go cmd/stockyard/destroy_test.go cmd/stockyard/grpc_test.go cmd/stockyard/task_presence_test.go
git commit -m "Make task destruction fail closed on identity errors" -m "Refactor the destroy command behind a per-instance injected-client factory, use Cobra context and output, verify GetTask returns the requested ID, and assert exact lookup and destroy RPC behavior through the shared bufconn fixture."
```

Expected: one commit containing only the four listed files; `AGENTS.md` remains untracked.

### Task 3: Require Exact Name Confirmation for Named Tasks

**Files:**
- Modify: `cmd/stockyard/destroy.go`
- Modify: `cmd/stockyard/destroy_test.go`

**Interfaces:**
- Consumes: `newDestroyCommand`, `quoteTaskNameForDisplay`, `quotePOSIXShellArgument`, `destroyCommandServer`, and `executeDestroyCommand`.
- Produces: the public `--confirm-name string` flag and the final named/unnamed destroy behavior.

- [ ] **Step 1: Add the failing named-task behavior matrix**

Append to `cmd/stockyard/destroy_test.go`:

```go
func TestDestroyCommandNamedConfirmation(t *testing.T) {
	tests := []struct {
		name           string
		taskName       string
		args           []string
		wantErr        bool
		wantDestroyIDs []string
		wantOutput     []string
	}{
		{
			name:     "named preview",
			taskName: "production",
			args:     []string{"task-123"},
			wantOutput: []string{
				`name "production"`,
				"Add --force --confirm-name='production' to the same command.",
			},
		},
		{
			name:     "missing confirmation",
			taskName: "production",
			args:     []string{"task-123", "--force"},
			wantErr:  true,
		},
		{
			name:     "wrong confirmation",
			taskName: "production",
			args:     []string{"task-123", "--force", "--confirm-name=staging"},
			wantErr:  true,
		},
		{
			name:     "case mismatch",
			taskName: "Production",
			args:     []string{"task-123", "--force", "--confirm-name=production"},
			wantErr:  true,
		},
		{
			name:     "trailing whitespace mismatch",
			taskName: "production ",
			args:     []string{"task-123", "--force", "--confirm-name=production"},
			wantErr:  true,
		},
		{
			name:           "exact printable whitespace",
			taskName:       "production ",
			args:           []string{"task-123", "--force", "--confirm-name=production "},
			wantDestroyIDs: []string{"task-123"},
			wantOutput:     []string{"Task destroyed."},
		},
		{
			name:           "dash-prefixed name and flags before id",
			taskName:       "-production",
			args:           []string{"--force", "--confirm-name=-production", "task-123"},
			wantDestroyIDs: []string{"task-123"},
			wantOutput:     []string{"Task destroyed."},
		},
		{
			name:     "confirmation without force remains preview",
			taskName: "production",
			args:     []string{"task-123", "--confirm-name=production"},
			wantOutput: []string{
				"Add --force --confirm-name='production' to the same command.",
			},
		},
		{
			name:           "unnamed ignores irrelevant confirmation",
			args:           []string{"task-123", "--force", "--confirm-name=anything"},
			wantDestroyIDs: []string{"task-123"},
			wantOutput:     []string{"Task destroyed."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &destroyCommandServer{task: &pb.Task{Id: "task-123", Name: tt.taskName}}
			output, err := executeDestroyCommand(t, server, tt.args...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("destroy succeeded")
				}
			} else if err != nil {
				t.Fatalf("execute destroy: %v", err)
			}
			getIDs, destroyIDs := server.requestIDs()
			if !slices.Equal(getIDs, []string{"task-123"}) {
				t.Fatalf("get IDs = %v, want [task-123]", getIDs)
			}
			if !slices.Equal(destroyIDs, tt.wantDestroyIDs) {
				t.Fatalf("destroy IDs = %v, want %v", destroyIDs, tt.wantDestroyIDs)
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Fatalf("output %q does not contain %q", output, want)
				}
			}
		})
	}
}

func TestDestroyCommandNonPrintingNamesHaveSafeGuidance(t *testing.T) {
	names := []string{
		"line\nbreak",
		"\x1b[31mred",
		"rtl\u202etext",
		"nul\x00name",
	}

	for _, name := range names {
		server := &destroyCommandServer{task: &pb.Task{Id: "task-123", Name: name}}
		output, err := executeDestroyCommand(t, server, "task-123")
		if err != nil {
			t.Fatalf("preview %q: %v", name, err)
		}
		if !strings.Contains(output, quoteTaskNameForDisplay(name)) {
			t.Fatalf("output %q lacks safe display for %q", output, name)
		}
		if strings.Contains(output, name) {
			t.Fatalf("output contains raw task name %q: %q", name, output)
		}
		if strings.Contains(output, "--confirm-name=") {
			t.Fatalf("output offers pasteable suffix for %q: %q", name, output)
		}
		_, destroyIDs := server.requestIDs()
		if len(destroyIDs) != 0 {
			t.Fatalf("destroy IDs = %v, want none", destroyIDs)
		}

		forceServer := &destroyCommandServer{task: &pb.Task{Id: "task-123", Name: name}}
		_, err = executeDestroyCommand(t, forceServer, "task-123", "--force")
		if err == nil {
			t.Fatalf("forced destroy %q succeeded without confirmation", name)
		}
		for _, r := range err.Error() {
			if !unicode.IsPrint(r) {
				t.Fatalf("error %q contains non-printing rune %U", err, r)
			}
		}
		if !strings.Contains(err.Error(), quoteTaskNameForDisplay(name)) {
			t.Fatalf("error %q lacks safe display for %q", err, name)
		}
		_, destroyIDs = forceServer.requestIDs()
		if len(destroyIDs) != 0 {
			t.Fatalf("forced destroy IDs = %v, want none", destroyIDs)
		}
	}
}

func TestDestroyCommandPreviewDoesNotReconstructCommand(t *testing.T) {
	name := "release $(printf injected) it's"
	quotedName, ok := quotePOSIXShellArgument(name)
	if !ok {
		t.Fatalf("printable name %q was rejected", name)
	}
	server := &destroyCommandServer{task: &pb.Task{Id: "task-123", Name: name}}
	output, err := executeDestroyCommand(t, server, "task-123")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	want := "Add --force --confirm-name=" + quotedName + " to the same command."
	if !strings.Contains(output, want) {
		t.Fatalf("output lacks same-command suffix guidance: %q", output)
	}
	if strings.Contains(output, "stockyard destroy") {
		t.Fatalf("output reconstructed a command that could lose root flags: %q", output)
	}
}

func TestDestroyCommandConfirmationStateIsPerInstance(t *testing.T) {
	server := &destroyCommandServer{task: &pb.Task{Id: "task-123", Name: "production"}}
	if _, err := executeDestroyCommand(t, server, "task-123", "--confirm-name=production"); err != nil {
		t.Fatalf("preview with confirmation: %v", err)
	}
	if _, err := executeDestroyCommand(t, server, "task-123", "--force"); err == nil {
		t.Fatal("fresh command inherited confirmation state")
	}
	_, destroyIDs := server.requestIDs()
	if len(destroyIDs) != 0 {
		t.Fatalf("destroy IDs = %v, want none", destroyIDs)
	}
}

func TestDestroyCommandConfirmationParsingFailsBeforeClientCreation(t *testing.T) {
	cases := [][]string{
		{"task-123", "--force", "--confirm-name"},
		{"task-123", "--force", "--", "extra"},
		{"task-123", "--unknown"},
	}

	for _, args := range cases {
		clientCreations := 0
		cmd := newDestroyCommand(func() (*client.Client, error) {
			clientCreations++
			return nil, nil
		})
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("args %v succeeded", args)
		}
		if clientCreations != 0 {
			t.Fatalf("args %v created %d clients, want 0", args, clientCreations)
		}
	}
}

func TestDestroyCommandHelpDocumentsNamedConfirmation(t *testing.T) {
	cmd := newDestroyCommand(func() (*client.Client, error) {
		t.Fatal("help inspection must not create a client")
		return nil, nil
	})
	if !strings.Contains(cmd.Long, "Named tasks require both --force and --confirm-name") {
		t.Fatalf("long help does not explain named confirmation: %q", cmd.Long)
	}
	flag := cmd.Flags().Lookup("confirm-name")
	if flag == nil {
		t.Fatal("--confirm-name flag is missing")
	}
	if !strings.Contains(flag.Usage, "exact task name") {
		t.Fatalf("--confirm-name usage = %q", flag.Usage)
	}
}
```

- [ ] **Step 2: Run the named-task tests and verify the red state**

Run:

```bash
go test ./cmd/stockyard -run 'TestDestroyCommand(Named|NonPrinting|PreviewDoesNot|Confirmation|Help)' -count=1
```

Expected: FAIL because `--confirm-name` does not exist, named `--force` still destroys, the preview lacks safe name guidance, and the long help lacks the new contract.

- [ ] **Step 3: Implement the exact-name guard and guidance**

Inside `newDestroyCommand`, add a local `confirmName string`. Set the command's `Long` field to:

```go
Long: `Destroy a task and its workspace.

Without --force, the command only previews the destruction. Unnamed tasks
require --force. Named tasks require both --force and --confirm-name, and the
confirmation must match the stored task name byte-for-byte.`,
```

After validating the returned task ID, replace the preview and force blocks with:

```go
taskName := task.GetName()
displayName := quoteTaskNameForDisplay(taskName)
output := cmd.OutOrStdout()
if !force {
	fmt.Fprintf(output, "About to destroy task %s", taskID)
	if taskName != "" {
		fmt.Fprintf(output, " (name %s)", displayName)
	}
	fmt.Fprintln(output, ":")
	fmt.Fprintln(output, "\nThis will delete the VM and all workspace data.")
	if taskName == "" {
		fmt.Fprintln(output, "Run with --force to confirm.")
	} else if quotedName, ok := quotePOSIXShellArgument(taskName); ok {
		fmt.Fprintf(output, "Add --force --confirm-name=%s to the same command.\n", quotedName)
	} else {
		fmt.Fprintf(
			output,
			"Task name: %s. Add --force and --confirm-name with that exact value; no pasteable suffix is shown because the name contains non-printing characters.\n",
			displayName,
		)
	}
	return nil
}

if taskName != "" && confirmName != taskName {
	return fmt.Errorf(
		"refusing to destroy named task %q (name %s): --confirm-name must match exactly",
		taskID,
		displayName,
	)
}

fmt.Fprintf(output, "Destroying task %s...\n", taskID)
if err := c.DestroyTask(cmd.Context(), taskID); err != nil {
	return fmt.Errorf("failed to destroy task: %w", err)
}
fmt.Fprintln(output, "Task destroyed.")
return nil
```

Register the new flag next to `--force`:

```go
cmd.Flags().StringVar(&confirmName, "confirm-name", "", "Confirm a named task by its exact task name")
```

- [ ] **Step 4: Format and rerun the focused named-task tests**

Run:

```bash
gofmt -w cmd/stockyard/destroy.go cmd/stockyard/destroy_test.go
go test ./cmd/stockyard -run 'TestDestroyCommand(Named|NonPrinting|PreviewDoesNot|Confirmation|Help)' -count=1
```

Expected: PASS. Missing, wrong-case, and trailing-space confirmations record zero destroy requests; exact confirmations record one request for `task-123`.

- [ ] **Step 5: Run the complete CLI package tests**

Run:

```bash
go test ./cmd/stockyard -count=1
```

Expected: PASS, including the POSIX-shell round trips, task-presence tests, parser failures, context cancellation, and flag-isolation checks.

- [ ] **Step 6: Inspect the user-facing help**

Run:

```bash
go run ./cmd/stockyard destroy --help
```

Expected: help states that named tasks require both flags and lists `--confirm-name` with its exact-name description. The command must not attempt a daemon connection.

- [ ] **Step 7: Commit the named-task guard**

Run:

```bash
git status --short
git add cmd/stockyard/destroy.go cmd/stockyard/destroy_test.go
git commit -m "Require names to destroy named tasks" -m "Keep --force sufficient for unnamed tasks, but require a byte-exact --confirm-name assertion for named tasks. Render names safely, show only a connection-preserving flag suffix, and cover refusals, parser edges, and non-printing names through real command behavior."
```

Expected: one commit containing only the two listed files; `AGENTS.md` remains untracked.

### Task 4: Publish the CLI Migration and Run Full Verification

**Files:**
- Modify: `README.md:24-47`

**Interfaces:**
- Consumes: the final `stockyard destroy` behavior and help from Task 3.
- Produces: public named/unnamed destruction examples and an explicit migration rule for existing scripts.

- [ ] **Step 1: Add the public destruction contract**

Insert this section after the existing "Creating VMs" section and before "Remote Access":

````markdown
## Destroying Tasks

Without `--force`, `stockyard destroy` previews the selected task and does not
delete anything:

```bash
stockyard destroy <task-id>
```

An unnamed task requires `--force`:

```bash
stockyard destroy <task-id> --force
```

A named task requires both `--force` and its exact name:

```bash
stockyard destroy <task-id> --force --confirm-name='my-task'
```

The name comparison is byte-for-byte. Existing scripts that destroy named tasks
with `--force` alone now fail closed and must supply an independently known
expected name. Copying both the ID and name from the same selected row defeats
the additional selection check.
````

- [ ] **Step 2: Check documentation formatting and scope**

Run:

```bash
git diff --check
git diff -- README.md cmd/stockyard/destroy.go cmd/stockyard/destroy_test.go
```

Expected: the whitespace check exits zero and the displayed diff contains only generic public CLI behavior. Do not rewrite dated plans that contain historical commands.

- [ ] **Step 3: Run focused CLI verification**

Run:

```bash
go test ./cmd/stockyard -count=1
```

Expected: PASS.

- [ ] **Step 4: Run the complete static test suite**

Run:

```bash
CGO_ENABLED=0 go test ./... -count=1
```

Expected: PASS across all packages.

- [ ] **Step 5: Run static analysis**

Run:

```bash
go vet ./...
```

Expected: exits zero with no findings.

- [ ] **Step 6: Build the shipped CLI with release-compatible settings**

Run:

```bash
make build-client
```

Expected: exits zero and produces `bin/stockyard`.

- [ ] **Step 7: Commit the public documentation**

Run:

```bash
git status --short
git add README.md
git commit -m "Document named task destruction" -m "Explain preview mode, the unchanged unnamed-task --force path, the exact-name requirement for named tasks, and the deliberate migration for scripts that previously supplied --force alone."
```

Expected: one commit containing only `README.md`; `AGENTS.md` remains untracked.

- [ ] **Step 8: Audit the completed branch**

Run:

```bash
git status --short
git diff --check main...HEAD
git log --oneline main..HEAD
```

Expected: the only working-tree entry is `?? AGENTS.md`; the diff check passes; branch history contains the two design commits, the implementation plan commit, and the four implementation commits from Tasks 1-4.

package main

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

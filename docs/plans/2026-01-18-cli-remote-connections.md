# CLI Remote Connections Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable the stockyard CLI to connect to remote daemons via `--url` flag or `STOCKYARD_URL` env var.

**Architecture:** Add URL parsing to the client package (`unix://`, `grpc://`, `grpcs://`). Add global `--url` flag to CLI. Add optional TCP listener to daemon. Connection resolution: flag → env → system config → default.

**Tech Stack:** Go, gRPC, cobra (CLI), TLS (optional)

**Reference:** `docs/specs/cli-remote-connections.md`

---

## Phase 1: Client URL Support

### Task 1: Add URL parsing to client package

**Files:**
- Create: `pkg/client/url.go`
- Create: `pkg/client/url_test.go`

**Step 1: Write failing test for URL parsing**

```go
// pkg/client/url_test.go
package client

import "testing"

func TestParseURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantAddr string
		wantTLS  bool
		wantErr  bool
	}{
		{"unix socket", "unix:///var/run/stockyard.sock", "unix:///var/run/stockyard.sock", false, false},
		{"grpc no TLS", "grpc://localhost:65432", "localhost:65432", false, false},
		{"grpcs with TLS", "grpcs://example.com:65432", "example.com:65432", true, false},
		{"bare host:port defaults to grpc", "myhost:65432", "myhost:65432", false, false},
		{"empty string", "", "", false, true},
		{"invalid scheme", "http://localhost:65432", "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, tls, err := ParseURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if addr != tt.wantAddr {
				t.Errorf("ParseURL(%q) addr = %q, want %q", tt.input, addr, tt.wantAddr)
			}
			if tls != tt.wantTLS {
				t.Errorf("ParseURL(%q) tls = %v, want %v", tt.input, tls, tt.wantTLS)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/client/... -run TestParseURL -v
```
Expected: FAIL - `ParseURL` undefined

**Step 3: Implement ParseURL**

```go
// pkg/client/url.go
package client

import (
	"fmt"
	"strings"
)

// ParseURL parses a stockyard URL and returns the address and whether TLS is required.
// Supported schemes:
//   - unix:///path/to/socket - Unix socket
//   - grpc://host:port - TCP without TLS
//   - grpcs://host:port - TCP with TLS
//   - host:port - defaults to grpc://
func ParseURL(rawURL string) (addr string, tls bool, err error) {
	if rawURL == "" {
		return "", false, fmt.Errorf("empty URL")
	}

	// Check for scheme
	if strings.HasPrefix(rawURL, "unix://") {
		return rawURL, false, nil
	}

	if strings.HasPrefix(rawURL, "grpcs://") {
		return strings.TrimPrefix(rawURL, "grpcs://"), true, nil
	}

	if strings.HasPrefix(rawURL, "grpc://") {
		return strings.TrimPrefix(rawURL, "grpc://"), false, nil
	}

	// Check for invalid schemes
	if strings.Contains(rawURL, "://") {
		return "", false, fmt.Errorf("unsupported URL scheme: %s (use unix://, grpc://, or grpcs://)", rawURL)
	}

	// Bare host:port - default to grpc://
	if strings.Contains(rawURL, ":") {
		return rawURL, false, nil
	}

	return "", false, fmt.Errorf("invalid URL format: %s", rawURL)
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/client/... -run TestParseURL -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/client/url.go pkg/client/url_test.go
git commit -m "feat(client): add URL parsing for remote connections"
```

---

### Task 2: Add NewFromURL constructor

**Files:**
- Modify: `pkg/client/client.go`
- Modify: `pkg/client/url_test.go`

**Step 1: Write failing test for NewFromURL**

```go
// Add to pkg/client/url_test.go

func TestNewFromURL_UnixSocket(t *testing.T) {
	// This tests that NewFromURL correctly parses unix:// URLs
	// We can't actually connect without a running daemon, so we just test URL parsing
	_, err := NewFromURL("unix:///nonexistent/socket.sock")
	// Connection will fail, but URL parsing should work
	if err == nil {
		t.Error("expected connection error for nonexistent socket")
	}
	// Error should mention connection, not URL parsing
	if !strings.Contains(err.Error(), "connect") && !strings.Contains(err.Error(), "dial") {
		t.Errorf("expected connection error, got: %v", err)
	}
}

func TestNewFromURL_InvalidScheme(t *testing.T) {
	_, err := NewFromURL("http://localhost:8080")
	if err == nil {
		t.Error("expected error for invalid scheme")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported scheme error, got: %v", err)
	}
}
```

Add `"strings"` to imports.

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/client/... -run TestNewFromURL -v
```
Expected: FAIL - `NewFromURL` undefined

**Step 3: Implement NewFromURL**

Add to `pkg/client/client.go`:

```go
// NewFromURL creates a new client connected to the daemon at the given URL.
// Supported URL formats:
//   - unix:///path/to/socket - Unix socket (local)
//   - grpc://host:port - TCP without TLS (remote)
//   - grpcs://host:port - TCP with TLS (remote)
//   - host:port - defaults to grpc://
func NewFromURL(url string) (*Client, error) {
	addr, useTLS, err := ParseURL(url)
	if err != nil {
		return nil, err
	}

	var opts []grpc.DialOption

	if useTLS {
		// TLS connection
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	} else {
		// No TLS
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// For unix sockets, use the full URL; for TCP, just the host:port
	target := addr
	if strings.HasPrefix(addr, "unix://") {
		target = addr
	}

	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewStockyardClient(conn),
	}, nil
}
```

Add imports to `pkg/client/client.go`:
```go
import (
	"crypto/tls"
	"strings"
	// ... existing imports ...
	"google.golang.org/grpc/credentials"
)
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/client/... -run TestNewFromURL -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/client/client.go pkg/client/url_test.go
git commit -m "feat(client): add NewFromURL for URL-based connections"
```

---

## Phase 2: CLI Global Flag

### Task 3: Add ResolveURL helper function

**Files:**
- Create: `pkg/client/resolve.go`
- Create: `pkg/client/resolve_test.go`

**Step 1: Write failing test for ResolveURL**

```go
// pkg/client/resolve_test.go
package client

import (
	"os"
	"testing"
)

func TestResolveURL(t *testing.T) {
	// Clean env before each test
	originalEnv := os.Getenv("STOCKYARD_URL")
	defer os.Setenv("STOCKYARD_URL", originalEnv)

	tests := []struct {
		name       string
		flagURL    string
		envURL     string
		configPath string
		want       string
	}{
		{
			name:    "flag takes precedence",
			flagURL: "grpc://flag-host:1234",
			envURL:  "grpc://env-host:5678",
			want:    "grpc://flag-host:1234",
		},
		{
			name:    "env used when no flag",
			flagURL: "",
			envURL:  "grpc://env-host:5678",
			want:    "grpc://env-host:5678",
		},
		{
			name:       "config socket used when no flag or env",
			flagURL:    "",
			envURL:     "",
			configPath: "/custom/socket.sock",
			want:       "unix:///custom/socket.sock",
		},
		{
			name:       "default when nothing set",
			flagURL:    "",
			envURL:     "",
			configPath: "",
			want:       "unix:///var/run/stockyard/stockyard.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("STOCKYARD_URL", tt.envURL)
			got := ResolveURL(tt.flagURL, tt.configPath)
			if got != tt.want {
				t.Errorf("ResolveURL(%q, %q) = %q, want %q", tt.flagURL, tt.configPath, got, tt.want)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/client/... -run TestResolveURL -v
```
Expected: FAIL - `ResolveURL` undefined

**Step 3: Implement ResolveURL**

```go
// pkg/client/resolve.go
package client

import "os"

const DefaultSocketPath = "/var/run/stockyard/stockyard.sock"

// ResolveURL determines the daemon URL using the following precedence:
// 1. flagURL (from --url flag)
// 2. STOCKYARD_URL environment variable
// 3. configSocketPath (from system config)
// 4. Default socket path
func ResolveURL(flagURL, configSocketPath string) string {
	// 1. Flag takes highest precedence
	if flagURL != "" {
		return flagURL
	}

	// 2. Environment variable
	if envURL := os.Getenv("STOCKYARD_URL"); envURL != "" {
		return envURL
	}

	// 3. Config socket path (convert to unix:// URL)
	if configSocketPath != "" {
		return "unix://" + configSocketPath
	}

	// 4. Default
	return "unix://" + DefaultSocketPath
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/client/... -run TestResolveURL -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/client/resolve.go pkg/client/resolve_test.go
git commit -m "feat(client): add ResolveURL for connection precedence"
```

---

### Task 4: Add --url global flag to CLI

**Files:**
- Modify: `cmd/stockyard/root.go`

**Step 1: Add global flag variable and flag definition**

```go
// cmd/stockyard/root.go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var urlFlag string

var rootCmd = &cobra.Command{
	Use:   "stockyard",
	Short: "Coding agent VM orchestrator",
	Long: `Stockyard runs coding agents in isolated Firecracker micro-VMs
with ZFS-based audit trail snapshots.

Quick Start:
  # Initialize stockyard
  stockyard init --instance my-dev

  # Start the daemon (in another terminal)
  stockyardd

  # Run a coding agent
  stockyard run --repo github.com/org/repo -- claude-code -p "your prompt"

  # Attach to the running VM
  stockyard attach <task-id>

  # List running tasks
  stockyard list`,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&urlFlag, "url", "", "Daemon URL (env: STOCKYARD_URL)\n  Formats: unix:///path, grpc://host:port, grpcs://host:port")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

**Step 2: Verify flag is registered**

```bash
go build -o bin/stockyard ./cmd/stockyard && ./bin/stockyard --help
```
Expected: See `--url` in global flags

**Step 3: Commit**

```bash
git add cmd/stockyard/root.go
git commit -m "feat(cli): add --url global flag"
```

---

### Task 5: Create helper to get client from resolved URL

**Files:**
- Create: `cmd/stockyard/client.go`

**Step 1: Create helper function**

```go
// cmd/stockyard/client.go
package main

import (
	"fmt"

	"github.com/obra/stockyard/pkg/client"
	"github.com/obra/stockyard/pkg/config"
)

// getClient returns a client connected to the daemon.
// It resolves the URL from: --url flag → STOCKYARD_URL env → config → default
func getClient() (*client.Client, error) {
	// Load config to get socket path (may not exist for remote-only usage)
	var configSocketPath string
	if cfg, err := config.Load(); err == nil {
		configSocketPath = cfg.Daemon.SocketPath
	}

	url := client.ResolveURL(urlFlag, configSocketPath)

	c, err := client.NewFromURL(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon at %s: %w\nIs stockyardd running?", url, err)
	}

	return c, nil
}
```

**Step 2: Verify it compiles**

```bash
go build ./cmd/stockyard/...
```
Expected: Success

**Step 3: Commit**

```bash
git add cmd/stockyard/client.go
git commit -m "feat(cli): add getClient helper for URL resolution"
```

---

### Task 6: Update list command to use getClient

**Files:**
- Modify: `cmd/stockyard/list.go`

**Step 1: Update list.go to use getClient**

```go
// cmd/stockyard/list.go
package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var listStatus string

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "List tasks",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := getClient()
		if err != nil {
			return err
		}
		defer c.Close()

		tasks, err := c.ListTasks(context.Background(), listStatus)
		if err != nil {
			return err
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tREPO\tSTATUS\tCREATED")
		for _, t := range tasks {
			name := t.Name
			if name == "" {
				name = "-"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				t.Id, name, t.Repo, t.Status, t.CreatedAt)
		}
		w.Flush()

		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (running, stopped, failed)")
	rootCmd.AddCommand(listCmd)
}
```

**Step 2: Test locally**

```bash
go build -o bin/stockyard ./cmd/stockyard && ./bin/stockyard list
```
Expected: Shows task list (same as before)

**Step 3: Commit**

```bash
git add cmd/stockyard/list.go
git commit -m "refactor(cli): update list to use getClient helper"
```

---

### Task 7: Update remaining commands to use getClient

**Files:**
- Modify: `cmd/stockyard/run.go`
- Modify: `cmd/stockyard/stop.go`
- Modify: `cmd/stockyard/restart.go`
- Modify: `cmd/stockyard/destroy.go`
- Modify: `cmd/stockyard/attach.go`
- Modify: `cmd/stockyard/logs.go`
- Modify: `cmd/stockyard/snapshot.go`
- Modify: `cmd/stockyard/snapshots.go`
- Modify: `cmd/stockyard/restore.go`
- Modify: `cmd/stockyard/cp.go`
- Modify: `cmd/stockyard/gc.go`
- Modify: `cmd/stockyard/resources.go`

**Step 1: Update each command**

For each file, replace:
```go
cfg, err := config.Load()
if err != nil {
    return err
}

c, err := client.New(cfg.Daemon.SocketPath)
if err != nil {
    return fmt.Errorf("failed to connect to daemon: %w\nIs stockyardd running?", err)
}
```

With:
```go
c, err := getClient()
if err != nil {
    return err
}
```

Remove unused `config` import from files that no longer need it.

**Step 2: Build and test**

```bash
go build -o bin/stockyard ./cmd/stockyard
./bin/stockyard list
./bin/stockyard --url unix:///var/run/stockyard/stockyard.sock list
```
Expected: Both work identically

**Step 3: Run full test suite**

```bash
go test ./cmd/stockyard/... -v
```
Expected: All tests pass

**Step 4: Commit**

```bash
git add cmd/stockyard/*.go
git commit -m "refactor(cli): update all commands to use getClient helper"
```

---

## Phase 3: Daemon TCP Listener

### Task 8: Add gRPC address config option

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/config_test.go`

**Step 1: Write failing test**

```go
// Add to pkg/config/config_test.go

func TestConfig_GRPCAddrDefault(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Daemon.GRPCAddr != "" {
		t.Errorf("expected GRPCAddr to be empty by default, got %q", cfg.Daemon.GRPCAddr)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./pkg/config/... -run TestConfig_GRPCAddrDefault -v
```
Expected: FAIL - GRPCAddr field doesn't exist

**Step 3: Add GRPCAddr to config**

In `pkg/config/config.go`, update `DaemonConfig`:

```go
type DaemonConfig struct {
	SocketPath string `json:"socket_path"`
	DataDir    string `json:"data_dir"`
	GRPCAddr   string `json:"grpc_addr,omitempty"` // Optional TCP address for remote gRPC (e.g., ":65433")
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./pkg/config/... -run TestConfig_GRPCAddrDefault -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add pkg/config/config.go pkg/config/config_test.go
git commit -m "feat(config): add grpc_addr option for remote TCP listener"
```

---

### Task 9: Add TCP listener to daemon

**Files:**
- Modify: `pkg/daemon/daemon.go`

**Step 1: Add TCP listener field**

Add to `Daemon` struct:

```go
type Daemon struct {
	// ... existing fields ...
	grpcListener net.Listener // TCP listener for remote gRPC (optional)
}
```

**Step 2: Add TCP listener startup in Start()**

In the `Start()` method, after the Unix socket listener setup, add:

```go
// Start optional TCP listener for remote gRPC access
if d.cfg.Daemon.GRPCAddr != "" {
	tcpListener, err := net.Listen("tcp", d.cfg.Daemon.GRPCAddr)
	if err != nil {
		d.listener.Close()
		return fmt.Errorf("failed to listen on TCP %s: %w", d.cfg.Daemon.GRPCAddr, err)
	}
	d.grpcListener = tcpListener
	fmt.Printf("gRPC server listening on %s\n", d.cfg.Daemon.GRPCAddr)

	go func() {
		if err := grpcSrv.Serve(tcpListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			fmt.Printf("gRPC TCP server error: %v\n", err)
		}
	}()
}
```

Add `"errors"` to imports.

**Step 3: Add TCP listener cleanup in Stop()**

In the `Stop()` method, add cleanup:

```go
if d.grpcListener != nil {
	d.grpcListener.Close()
}
```

**Step 4: Build and test**

```bash
go build -o bin/stockyardd ./cmd/stockyardd
```
Expected: Compiles successfully

**Step 5: Commit**

```bash
git add pkg/daemon/daemon.go
git commit -m "feat(daemon): add optional TCP listener for remote gRPC"
```

---

### Task 10: Test remote connection end-to-end

**Files:** None (manual testing)

**Step 1: Update system config to enable TCP**

```bash
sudo jq '.daemon.grpc_addr = ":65433"' /etc/stockyard/config.json > /tmp/config.json && sudo mv /tmp/config.json /etc/stockyard/config.json
```

**Step 2: Restart daemon**

```bash
make deploy
```

**Step 3: Test local connection**

```bash
./bin/stockyard list
```
Expected: Works

**Step 4: Test remote connection via localhost**

```bash
./bin/stockyard --url grpc://localhost:65433 list
```
Expected: Works, same output

**Step 5: Test env var**

```bash
STOCKYARD_URL=grpc://localhost:65433 ./bin/stockyard list
```
Expected: Works

**Step 6: Commit config change (optional)**

If you want TCP enabled by default, update the system config permanently.

---

## Phase 4: Documentation

### Task 11: Update CLI help

**Files:**
- Modify: `cmd/stockyard/root.go`

**Step 1: Update Long description**

Update the `Long` description in `rootCmd` to mention remote connections:

```go
Long: `Stockyard runs coding agents in isolated Firecracker micro-VMs
with ZFS-based audit trail snapshots.

Quick Start:
  # Initialize stockyard
  stockyard init --instance my-dev

  # Start the daemon (in another terminal)
  stockyardd

  # Run a coding agent
  stockyard run --repo github.com/org/repo -- claude-code -p "your prompt"

  # Attach to the running VM
  stockyard attach <task-id>

  # List running tasks
  stockyard list

Remote Access:
  # Connect to a remote daemon
  stockyard --url grpc://stockyard-server:65433 list

  # Or via environment variable
  export STOCKYARD_URL=grpc://stockyard-server:65433
  stockyard list`,
```

**Step 2: Commit**

```bash
git add cmd/stockyard/root.go
git commit -m "docs(cli): add remote access examples to help text"
```

---

### Task 12: Run full test suite and cleanup

**Files:** None

**Step 1: Run all tests**

```bash
go test ./... -v
```
Expected: All pass

**Step 2: Run linter**

```bash
make lint
```
Expected: No errors

**Step 3: Final commit**

```bash
git add -A
git commit -m "feat: CLI remote connections complete"
```

---

## Summary

| Phase | Tasks | Description |
|-------|-------|-------------|
| 1 | 1-2 | Client URL parsing and NewFromURL |
| 2 | 3-7 | CLI --url flag and getClient helper |
| 3 | 8-10 | Daemon TCP listener |
| 4 | 11-12 | Documentation and cleanup |

Total: 12 tasks, ~30-45 minutes implementation time.

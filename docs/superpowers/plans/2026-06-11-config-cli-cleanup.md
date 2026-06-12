# Config/CLI Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `stockyard init` the single, truthful setup command (backend-aware, image-seeding), delete `configure` and the dead config fields it fed, and derive the dashboard's subnet display from the gateway instead of a hardcoded literal.

**Architecture:** Pure CLI/config surgery plus one small derivation helper. `init` gains `--backend`/`--image` flags with a platform-aware default extracted into a testable `defaultBackend(goos)` helper; `configure.go` (sole `survey` consumer) is deleted; `SecretsConfig.Provider` and `FirecrackerConfig.VMSubnet` leave the struct (old config.json files keep loading — Go ignores unknown JSON keys); a new `network.SubnetForGateway` becomes the one source of truth for "gateway masked to /24", feeding both the IP pool and a new `Server.SetVMSubnet` display setter wired at the daemon's single `dashboard.NewServer` call site.

**Tech Stack:** Go 1.25, cobra v1.10.2, stdlib `net`/`encoding/json`. No new dependencies; one dependency removed (`github.com/AlecAivazis/survey/v2`).

**Spec:** `docs/superpowers/specs/2026-06-11-config-cli-cleanup-design.md` (approved as written)
**Ticket:** PRI-2177
**Branch:** `matt/pri-2177-configcli-cleanup-pass-dead-config-fields-stale`

---

## Verified Facts (read this before touching code)

Everything below was checked at HEAD `7c5892b` (post-#11/#12 merge). The plan states what is true, not what the spec assumed.

1. **Fresh-install `init` already works.** `config.Load()` → `LoadFromDir()` returns `DefaultConfig(), nil` when config.json does not exist (`pkg/config/config.go:108-111`). The `if err != nil` in init.go only fires on a real read/parse failure or a `ConfigDir()` failure. No fresh-install special-casing is needed.
2. **Dead-field readers, complete list.** `cfg.Secrets.Provider`: `pkg/config/config.go:30,69`, `pkg/config/config_test.go:21-23,32`, `cmd/stockyard/configure.go:38,40,43`. `cfg.Firecracker.VMSubnet`: `pkg/config/config.go:53,86`, `pkg/config/config_test.go:71-73`. The dashboard's `"VMSubnet": "10.0.100.0/24"` at `pkg/dashboard/server.go:480` is an independent map-literal (template key for `pkg/dashboard/templates/settings.html:53` `{{.VMSubnet}}`), not a config read. Nothing else in the tree reads either field.
3. **`configure.go` has no tests** (no `configure_test.go` exists — the spec's "and its tests" is vacuous) and is the **only** importer of `github.com/AlecAivazis/survey/v2` (verified by `rg "AlecAivazis" --type go`). `go mod tidy` after deletion drops it plus its indirect-only deps.
4. **Dashboard data flow.** `handleSettings` (`pkg/dashboard/server.go:470-481`) consults nothing — every value in its data map is hardcoded. The `Server` has no config access; the daemon constructs it at exactly one place: `pkg/daemon/daemon.go:366` (`dashboard.NewServer(adapter, d.cfg.VM.User, d.cfg.AppleContainer.ContainerBin)`), inside the `if d.cfg.HTTP.Enabled` block, with full `cfg` in scope. **Spec correction:** the "fake gateway provider returning 10.0.100.1" at `pkg/dashboard/server_test.go:107` is `MockDaemon.GetVMIP` — a per-task VM IP used by the terminal handler, not a settings/gateway path. The settings page never calls the daemon, so the least-invasive threading is a display-string setter (`SetVMSubnet`) called once at the daemon wiring point; the 38 `NewServer(...)` test call sites stay untouched.
5. **The /24 source of truth.** `pkg/daemon/daemon.go:161` calls `network.NewIPPoolFromGateway(cfg.Firecracker.VMGateway, 24)`; the derivation (parse IPv4 gateway → mask with `net.CIDRMask(24, 32)`) lives in `pkg/network/ip_pool.go:51-79`. Extracting it as `SubnetForGateway` and refactoring `NewIPPoolFromGateway` to use it gives the dashboard the pool's actual network. `(&net.IPNet{IP: ..., Mask: ...}).String()` renders `"10.0.100.0/24"`.
6. **Smoke constraints (darwin/apple-container).** `createAppleContainerBackend` (`pkg/daemon/backend_apple_container_darwin.go`) fail-fasts if `apple_container.image == ""` and probes `container system status` at daemon startup. `init` saves `DefaultConfig` paths — the **real** socket (`/var/run/stockyard/stockyard.sock`) and data dir — so the smoke must patch `daemon.socket_path`/`daemon.data_dir`/`secrets.dir` to scratch paths after `init`, before starting the daemon. `stockyard image ls` works on apple-container: `grpc.go:192 ListImages` falls through to the backend's `ImageLister` (the `container` CLI store).
7. **Test coverage gaps.** `make test` runs only `./pkg/... -v` (root Makefile `test-unit`); neither it nor CI runs `./cmd/...`. Run CLI tests explicitly.
8. **Docs reality.** Root Makefile has **no** deploy targets (CLAUDE.md's `make deploy-daemon`/`deploy-image`/`deploy` are phantoms). The real image flow lives in `vm-image/Makefile`: `deploy` (build + register via `stockyard image import default`), `deploy-image REGISTRY_IMAGE=<name>` (register only, no build), `container-image` (macOS OCI image, tagged `stockyard.local/stockyard-vm:container`), with live image replacement and no daemon restart. **Spec correction:** `vm-image/macos/README.md` does not exist at HEAD (no `vm-image/macos/` directory; PRI-2178 put the macOS image story in `vm-image/Makefile`, `docs/image-contract.md`, and the root `README.md` — `docs/image-contract.md` is current and needs no changes). The drift to fix is in `vm-image/README.md` (manual ZFS copy + `zfs snapshot` "Initial Setup"/"Updating" sections are pre-registry) and `CLAUDE.md`. The "rootfs_path seeds the registry default" semantic is confirmed by `pkg/daemon/image_registry.go:181` (`EnsureDefault seeds/heals the 'default' image from the configured rootfs`); the per-task override is `stockyard run --image` (`cmd/stockyard/run.go:138`).
9. **Backend names are bare strings.** No constants exist; the daemon switch (`pkg/daemon/daemon.go:100-134`) accepts `""` (→ firecracker), `"firecracker"`, `"apple-container"`, and errors on anything else. `init` uses the same literals.
10. **Dashboard templates are embedded** (`pkg/dashboard/templates.go` `//go:embed templates/*.html`), so a package test GETting `/settings` renders the real `settings.html`, not the "Settings page" fallback.

## File Structure

| File | Change |
|---|---|
| `cmd/stockyard/init.go` | Rewrite: `--backend`/`--image` flags, validation, `defaultBackend()`, truthful `printNextSteps()` |
| `cmd/stockyard/init_test.go` | Rewrite: flag-reset helper, platform-default/rejection/seeding/output tests |
| `cmd/stockyard/configure.go` | **Delete** |
| `go.mod` / `go.sum` | `survey` removed via `go mod tidy` |
| `pkg/config/config.go` | Remove `SecretsConfig.Provider`, `FirecrackerConfig.VMSubnet` + their `DefaultConfig()` seeds |
| `pkg/config/config_test.go` | Drop dead-field asserts; add legacy-config load regression test |
| `pkg/network/ip_pool.go` | Add `SubnetForGateway()`; `NewIPPoolFromGateway()` delegates to it |
| `pkg/network/ip_pool_test.go` | Add `TestSubnetForGateway` |
| `pkg/dashboard/server.go` | `Server.vmSubnet` field + `SetVMSubnet()`; `handleSettings` uses it |
| `pkg/dashboard/server_test.go` | Add settings-page derivation test |
| `pkg/daemon/daemon.go` | `vmSubnetPrefixLen` const; wire `SetVMSubnet` at the `NewServer` call site |
| `CLAUDE.md` | Remove phantom targets; document real build/test/image-deploy flows |
| `README.md` | Backend paragraph: explicit-backend, default-image-overridable, rootfs_path-seed semantics |
| `vm-image/README.md` | Replace pre-registry ZFS install/update instructions with `make deploy` flow |

Task order matters twice: Task 2 (delete configure.go) **must precede** Task 3 (configure.go reads `cfg.Secrets.Provider` — removing the field first breaks the build), and Task 4 (`SubnetForGateway`) must precede Task 5 (daemon wiring uses it).

---

### Task 1: Rework `stockyard init`

**Files:**
- Modify: `cmd/stockyard/init.go` (full rewrite, shown below)
- Modify: `cmd/stockyard/init_test.go` (full rewrite, shown below)

- [x] **Step 1: Write the failing tests**

Replace the entire contents of `cmd/stockyard/init_test.go` with:

```go
// cmd/stockyard/init_test.go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/obra/stockyard/pkg/config"
)

// resetInitFlags clears init flag state between rootCmd.Execute calls. Cobra
// keeps both the bound variables and each flag's Changed bit across
// executions in the same process; without this, values leak between tests
// and MarkFlagRequired stops failing once any test has set --instance.
func resetInitFlags() {
	initInstanceName = ""
	initBackend = ""
	initImage = ""
	for _, name := range []string{"instance", "backend", "image"} {
		if f := initCmd.Flags().Lookup(name); f != nil {
			f.Changed = false
			_ = f.Value.Set("")
		}
	}
}

// runInitCmd executes `stockyard init <args...>` with captured output.
func runInitCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetInitFlags()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	defer func() {
		rootCmd.SetOut(nil) // nil restores cobra's default (os.Stdout/Stderr)
		rootCmd.SetErr(nil)
	}()
	rootCmd.SetArgs(append([]string{"init"}, args...))
	err := rootCmd.Execute()
	return buf.String(), err
}

func TestDefaultBackend(t *testing.T) {
	// Pure GOOS matrix — this is how the linux default is tested on a darwin
	// dev machine (and vice versa).
	if got := defaultBackend("darwin"); got != "apple-container" {
		t.Errorf("defaultBackend(darwin) = %q, want apple-container", got)
	}
	if got := defaultBackend("linux"); got != "firecracker" {
		t.Errorf("defaultBackend(linux) = %q, want firecracker", got)
	}
}

func TestInitCommand_RequiresInstance(t *testing.T) {
	_, err := runInitCmd(t)
	if err == nil {
		t.Fatal("expected error when --instance not provided")
	}
}

func TestInitCommand_CreatesConfigWithPlatformDefaultBackend(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "stockyard")
	t.Setenv("STOCKYARD_CONFIG_DIR", configDir)

	out, err := runInitCmd(t, "--instance", "test-local")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, err := config.LoadFromDir(configDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.InstanceID != "test-local" {
		t.Errorf("instance ID: got %q, want %q", cfg.InstanceID, "test-local")
	}
	if cfg.Secrets.Prefix != "test-local" {
		t.Errorf("secrets prefix: got %q, want %q", cfg.Secrets.Prefix, "test-local")
	}
	want := defaultBackend(runtime.GOOS)
	if cfg.Backend != want {
		t.Errorf("backend: got %q, want platform default %q", cfg.Backend, want)
	}

	// The backend must be explicit in the file, not inferred at load time.
	// (DefaultConfig().Backend is "", so the loaded value above already proves
	// the key was written; the raw check guards against a future omitempty.)
	raw, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatalf("failed to read config.json: %v", err)
	}
	if !strings.Contains(string(raw), `"backend": "`+want+`"`) {
		t.Errorf("config.json should contain explicit backend %q; got:\n%s", want, raw)
	}

	if !strings.Contains(out, "backend: "+want) {
		t.Errorf("init output should announce the chosen backend; got:\n%s", out)
	}
}

func TestInitCommand_ExplicitBackendWritten(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "stockyard")
	t.Setenv("STOCKYARD_CONFIG_DIR", configDir)

	_, err := runInitCmd(t, "--instance", "fc-host", "--backend", "firecracker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, err := config.LoadFromDir(configDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.Backend != "firecracker" {
		t.Errorf("backend: got %q, want firecracker", cfg.Backend)
	}
}

func TestInitCommand_RejectsUnknownBackend(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "stockyard")
	t.Setenv("STOCKYARD_CONFIG_DIR", configDir)

	_, err := runInitCmd(t, "--instance", "x", "--backend", "qemu")
	if err == nil || !strings.Contains(err.Error(), "invalid --backend") {
		t.Fatalf("expected invalid-backend error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(configDir, "config.json")); !os.IsNotExist(statErr) {
		t.Error("config.json must not be written when validation fails")
	}
}

func TestInitCommand_ImageSeedsAppleContainerImage(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "stockyard")
	t.Setenv("STOCKYARD_CONFIG_DIR", configDir)

	_, err := runInitCmd(t, "--instance", "mac", "--backend", "apple-container",
		"--image", "stockyard.local/stockyard-vm:container")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg, err := config.LoadFromDir(configDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.AppleContainer.Image != "stockyard.local/stockyard-vm:container" {
		t.Errorf("apple_container.image: got %q, want the --image value", cfg.AppleContainer.Image)
	}
}

func TestInitCommand_RejectsImageWithFirecracker(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "stockyard")
	t.Setenv("STOCKYARD_CONFIG_DIR", configDir)

	_, err := runInitCmd(t, "--instance", "x", "--backend", "firecracker",
		"--image", "whatever:latest")
	if err == nil || !strings.Contains(err.Error(), "--image is only valid with --backend apple-container") {
		t.Fatalf("expected firecracker+image rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(configDir, "config.json")); !os.IsNotExist(statErr) {
		t.Error("config.json must not be written when validation fails")
	}
}

func TestInitCommand_OverwritesExisting(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "stockyard")
	t.Setenv("STOCKYARD_CONFIG_DIR", configDir)

	initialCfg := config.DefaultConfig()
	initialCfg.InstanceID = "old-instance"
	initialCfg.Secrets.Prefix = "old-instance"
	if err := initialCfg.SaveToDir(configDir); err != nil {
		t.Fatalf("failed to create initial config: %v", err)
	}

	out, err := runInitCmd(t, "--instance", "new-instance")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `overwriting existing instance ID "old-instance"`) {
		t.Errorf("expected overwrite warning, got:\n%s", out)
	}

	cfg, err := config.LoadFromDir(configDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.InstanceID != "new-instance" {
		t.Errorf("instance ID: got %q, want %q", cfg.InstanceID, "new-instance")
	}
	if cfg.Secrets.Prefix != "new-instance" {
		t.Errorf("secrets prefix: got %q, want %q", cfg.Secrets.Prefix, "new-instance")
	}
}

func TestInitCommand_NextStepsAreTruthful(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "stockyard")
	t.Setenv("STOCKYARD_CONFIG_DIR", configDir)

	out, err := runInitCmd(t, "--instance", "mac", "--backend", "apple-container")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Secrets (optional",
		"op://Stockyard/mac/",
		"/etc/stockyard/secrets",
		"anthropic-api-key",
		"github-token",
		"tailscale-auth-key",
		"container image ls",
		"make -C vm-image container-image",
		"container system start",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("apple-container next-steps missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Create 1Password vault") {
		t.Errorf("old vault-first instructions must be gone; got:\n%s", out)
	}

	out, err = runInitCmd(t, "--instance", "fc", "--backend", "firecracker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"make -C vm-image deploy",
		"stockyard image import",
		"stockyardd.service",
		"Secrets (optional",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("firecracker next-steps missing %q; got:\n%s", want, out)
		}
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./cmd/stockyard/ -run 'TestInit|TestDefaultBackend' -v`
Expected: **compile failure** — `undefined: initBackend`, `undefined: initImage`, `undefined: defaultBackend`.

- [x] **Step 3: Write the implementation**

Replace the entire contents of `cmd/stockyard/init.go` with:

```go
// cmd/stockyard/init.go
package main

import (
	"fmt"
	"io"
	"runtime"

	"github.com/obra/stockyard/pkg/config"
	"github.com/spf13/cobra"
)

var (
	initInstanceName string
	initBackend      string
	initImage        string
)

// defaultBackend returns the platform-default VM backend for a GOOS value:
// apple-container on darwin, firecracker everywhere else. Extracted (rather
// than branching on runtime.GOOS inline) so both arms are testable on any
// development machine.
func defaultBackend(goos string) string {
	if goos == "darwin" {
		return "apple-container"
	}
	return "firecracker"
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize stockyard configuration",
	Long: `Initialize stockyard configuration: instance name, VM backend, and
(for apple-container) the default task image. Everything else — socket path,
DHCP ranges, secrets directory — is edited directly in config.json.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		backend := initBackend
		if backend == "" {
			backend = defaultBackend(runtime.GOOS)
		}
		if backend != "firecracker" && backend != "apple-container" {
			return fmt.Errorf("invalid --backend %q (valid: firecracker, apple-container)", backend)
		}
		if initImage != "" && backend == "firecracker" {
			return fmt.Errorf("--image is only valid with --backend apple-container; " +
				"the firecracker default image comes from rootfs_path via " +
				"'stockyard image import' (see 'make -C vm-image deploy')")
		}

		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		out := cmd.OutOrStdout()
		if cfg.InstanceID != "" {
			fmt.Fprintf(out, "Warning: overwriting existing instance ID %q\n", cfg.InstanceID)
		}

		cfg.InstanceID = initInstanceName
		cfg.Secrets.Prefix = initInstanceName
		// Always write the backend explicitly so the file says what the
		// daemon will do, even when it matches the platform default.
		cfg.Backend = backend
		if initImage != "" {
			cfg.AppleContainer.Image = initImage
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		configDir, err := config.ConfigDir()
		if err != nil {
			configDir = "~/.config/stockyard"
		}
		fmt.Fprintf(out, "Initialized stockyard instance %q (backend: %s)\n", initInstanceName, backend)
		fmt.Fprintf(out, "Config saved to %s/config.json\n", configDir)
		printNextSteps(out, cfg)

		return nil
	},
}

// printNextSteps prints truthful per-backend setup guidance. Secrets are
// optional on both backends: the daemon resolves each secret through a
// FallbackProvider — 1Password first, then files in secrets.dir
// (cmd/stockyardd/main.go) — and tasks run without them.
func printNextSteps(out io.Writer, cfg *config.Config) {
	fmt.Fprintf(out, "\nNext steps:\n")
	switch cfg.Backend {
	case "apple-container":
		fmt.Fprintf(out, "  1. Make sure a task image exists in the container CLI's local store:\n")
		fmt.Fprintf(out, "       container image ls\n")
		fmt.Fprintf(out, "     Build the reference image with: make -C vm-image container-image\n")
		fmt.Fprintf(out, "     (tagged stockyard.local/stockyard-vm:container)\n")
		if cfg.AppleContainer.Image == "" {
			fmt.Fprintf(out, "  2. Set the default task image: re-run init with --image <ref>,\n")
			fmt.Fprintf(out, "     or edit apple_container.image in config.json\n")
		} else {
			fmt.Fprintf(out, "  2. Default task image: %s\n", cfg.AppleContainer.Image)
			fmt.Fprintf(out, "     (override per task with 'stockyard run --image <ref>')\n")
		}
		fmt.Fprintf(out, "  3. Start the container service, then the daemon:\n")
		fmt.Fprintf(out, "       container system start && stockyardd\n")
	default: // firecracker
		fmt.Fprintf(out, "  1. Build and register the default VM image:\n")
		fmt.Fprintf(out, "       make -C vm-image deploy\n")
		fmt.Fprintf(out, "     (builds the rootfs and registers it via 'stockyard image import default';\n")
		fmt.Fprintf(out, "      named variants: make -C vm-image deploy-image REGISTRY_IMAGE=<name>)\n")
		fmt.Fprintf(out, "  2. Start the daemon: stockyardd\n")
		fmt.Fprintf(out, "     (Linux hosts: install scripts/stockyardd.service and run 'systemctl start stockyardd')\n")
	}

	secretsDir := cfg.Secrets.Dir
	if secretsDir == "" {
		secretsDir = "/etc/stockyard/secrets" // mirrors cmd/stockyardd/main.go
	}
	vault := cfg.Secrets.Vault
	if vault == "" {
		vault = "Stockyard"
	}
	fmt.Fprintf(out, "\nSecrets (optional — tasks run without them):\n")
	fmt.Fprintf(out, "  For each secret the daemon tries 1Password (op://%s/%s/<name>)\n", vault, cfg.InstanceID)
	fmt.Fprintf(out, "  and falls back to files in %s:\n", secretsDir)
	fmt.Fprintf(out, "    - anthropic-api-key\n")
	fmt.Fprintf(out, "    - github-token\n")
	fmt.Fprintf(out, "    - tailscale-auth-key\n")
}

func init() {
	initCmd.Flags().StringVar(&initInstanceName, "instance", "", "Instance name (required)")
	initCmd.Flags().StringVar(&initBackend, "backend", "",
		"VM backend: firecracker or apple-container (default: apple-container on macOS, firecracker elsewhere)")
	initCmd.Flags().StringVar(&initImage, "image", "",
		"Default task image for the apple-container backend (seeds apple_container.image)")
	initCmd.MarkFlagRequired("instance")
	rootCmd.AddCommand(initCmd)
}
```

Notes on intent, so nobody "fixes" these during review:
- Validation runs **before** `config.Load()`/`Save()` so a bad invocation never half-writes a config (the two `os.IsNotExist` test assertions pin this).
- `fmt.Printf` became `fmt.Fprintf(cmd.OutOrStdout(), ...)`: behavior-identical for users (cobra's default out is os.Stdout) but lets tests capture output.
- Per the spec, re-running `init` without `--backend` rewrites `backend` to the **platform default** — it does not preserve a hand-edited value. The instance-overwrite warning is the existing guard for "init on a configured machine".

- [x] **Step 4: Run the tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./cmd/stockyard/ -v`
Expected: PASS — all `TestInit*`/`TestDefaultBackend` tests plus the pre-existing CLI tests (attach, gc, image, resources, restart, snapshot).

- [x] **Step 5: Commit**

```bash
git add cmd/stockyard/init.go cmd/stockyard/init_test.go
git commit -m "feat(PRI-2177): rework stockyard init with backend/image flags and truthful next steps

--backend (platform-aware default, always written explicitly) and --image
(apple-container only, rejected with firecracker) replace hand-editing
config.json; next-steps output now describes the FallbackProvider secrets
reality and the real per-backend image flows.

Co-Authored-By: <YourHandle>@<first8-of-your-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: Delete `stockyard configure` and drop the survey dependency

**Files:**
- Delete: `cmd/stockyard/configure.go`
- Modify: `go.mod`, `go.sum` (via `go mod tidy`)

configure.go has no tests of its own, and it is the only consumer of `github.com/AlecAivazis/survey/v2` (Verified Facts #3). This task must run **before** Task 3: configure.go reads `cfg.Secrets.Provider` at lines 38/40/43, so the field cannot be removed while the file exists.

- [x] **Step 1: Delete the file**

```bash
git rm cmd/stockyard/configure.go
```

- [x] **Step 2: Verify nothing else references the command or the dependency**

Run: `rg -n "configureCmd|survey" --type go cmd/ pkg/`
Expected: no matches. (A match means a new consumer appeared since this plan was written — stop and reassess.)

- [x] **Step 3: Tidy the module**

Run: `go mod tidy`
Expected: `go.mod` no longer lists `github.com/AlecAivazis/survey/v2`; its indirect-only deps (`kballard/go-shellquote`, `mgutz/ansi`, `mattn/go-colorable`, and possibly others) drop from `go.mod`/`go.sum` if nothing else needs them. Do not hand-edit either file beyond what tidy produces.

- [x] **Step 4: Build and test**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./cmd/... -v`
Expected: build OK; CLI tests PASS. `./bin` is unaffected until `make build` in Task 7.

- [x] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "refactor(PRI-2177): delete stockyard configure and drop the survey dependency

init absorbed configure's job (instance + prefix); the interactive prompt
offered a secrets provider the daemon never reads, including an 'aws' option
that was never implemented. configure.go was survey's only consumer.

Co-Authored-By: <YourHandle>@<first8-of-your-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: Remove dead config fields (`secrets.provider`, `firecracker.vm_subnet`)

**Files:**
- Modify: `pkg/config/config.go:29-34` (SecretsConfig), `:49-58` (FirecrackerConfig), `:66-103` (DefaultConfig)
- Modify: `pkg/config/config_test.go:9-24`, `:26-56`, `:68-86`; add one test

No migration is needed: `LoadFromDir` uses plain `json.Unmarshal` into the struct (no `DisallowUnknownFields`), so old config.json files containing the removed keys keep loading and the keys disappear on the next `Save()`. The regression test below pins that.

- [x] **Step 1: Update the tests first**

In `pkg/config/config_test.go`, make these exact changes:

1. In `TestLoadConfig_DefaultsWhenNoFile`, replace the Provider assertion (lines 21-23):

```go
	if cfg.Secrets.Provider != "1password" {
		t.Errorf("expected default provider '1password', got %q", cfg.Secrets.Provider)
	}
```

with:

```go
	if cfg.Secrets.Vault != "Stockyard" {
		t.Errorf("expected default vault 'Stockyard', got %q", cfg.Secrets.Vault)
	}
```

2. In `TestSaveAndLoadConfig`, delete the line `Provider: "1password",` from the `SecretsConfig` literal.

3. In `TestConfig_DHCPDefaults`, delete the `VMSubnet` assertion block (lines 71-73):

```go
	if cfg.Firecracker.VMSubnet != "10.0.100.0/24" {
		t.Errorf("expected VMSubnet '10.0.100.0/24', got %q", cfg.Firecracker.VMSubnet)
	}
```

4. Append this new test at the end of the file:

```go
// TestLoadConfig_IgnoresRemovedFields proves that a config.json written
// before secrets.provider and firecracker.vm_subnet were removed still
// loads: json.Unmarshal ignores unknown keys, and the keys vanish on the
// next Save(). This is a regression guard (it passes before and after the
// field removal); it exists so nobody adds DisallowUnknownFields and
// silently breaks every pre-PRI-2177 install.
func TestLoadConfig_IgnoresRemovedFields(t *testing.T) {
	tmpDir := t.TempDir()
	legacy := `{
  "instance_id": "legacy",
  "secrets": {"provider": "1password", "vault": "Stockyard", "prefix": "legacy"},
  "firecracker": {"vm_subnet": "10.0.100.0/24", "vm_gateway": "10.0.100.9"}
}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(legacy), 0644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	cfg, err := LoadFromDir(tmpDir)
	if err != nil {
		t.Fatalf("loading a config with removed fields must succeed: %v", err)
	}
	if cfg.InstanceID != "legacy" {
		t.Errorf("instance ID: got %q, want %q", cfg.InstanceID, "legacy")
	}
	if cfg.Secrets.Prefix != "legacy" {
		t.Errorf("secrets prefix: got %q, want %q", cfg.Secrets.Prefix, "legacy")
	}
	if cfg.Firecracker.VMGateway != "10.0.100.9" {
		t.Errorf("vm_gateway: got %q, want %q", cfg.Firecracker.VMGateway, "10.0.100.9")
	}
}
```

- [x] **Step 2: Run the config tests**

Run: `CGO_ENABLED=0 go test ./pkg/config/ -v`
Expected: PASS (including the new regression test — it is valid both before and after the removal; the real "red" for a deletion is the compiler check in Step 4).

- [x] **Step 3: Remove the fields**

In `pkg/config/config.go`:

1. Replace the `SecretsConfig` struct (lines 29-34) with:

```go
type SecretsConfig struct {
	Vault  string `json:"vault"`  // 1Password vault name
	Prefix string `json:"prefix"` // 1Password item prefix (the instance ID)
	Dir    string `json:"dir"`    // Directory for the file-based fallback provider
}
```

2. In `FirecrackerConfig` (lines 49-58), delete the line:

```go
	VMSubnet       string `json:"vm_subnet"`
```

3. In `DefaultConfig()`, replace the `Secrets` literal:

```go
		Secrets: SecretsConfig{
			Provider: "1password",
			Vault:    "Stockyard",
		},
```

with:

```go
		Secrets: SecretsConfig{
			Vault: "Stockyard",
		},
```

and delete the Firecracker seed line:

```go
			VMSubnet:       "10.0.100.0/24",
```

- [x] **Step 4: Compile-prove no readers remain, then test**

Run: `CGO_ENABLED=0 go build ./... && rg -n "Secrets\.Provider|VMSubnet|vm_subnet" --type go cmd/ pkg/`
Expected: build OK; exactly three classes of rg hits remain, all benign — (1) the dashboard's `"VMSubnet"` map key at `pkg/dashboard/server.go:480` (a template key, renamed nowhere — its *value* changes in Task 5), (2) the comment line in `pkg/config/config_test.go`'s `TestLoadConfig_IgnoresRemovedFields` naming the removed fields, and (3) that test's legacy JSON literal `"vm_subnet": ...`. The test hits are the regression test added in Step 1 doing its job — do NOT "fix" them by stripping the legacy keys from the test; that would gut it.

Run: `CGO_ENABLED=0 go test ./pkg/config/ -v && CGO_ENABLED=0 go test ./pkg/... ./cmd/...`
Expected: all PASS.

- [x] **Step 5: Commit**

```bash
git add pkg/config/config.go pkg/config/config_test.go
git commit -m "refactor(PRI-2177): remove dead config fields secrets.provider and firecracker.vm_subnet

Neither was ever read: the daemon always uses the 1Password->file
FallbackProvider, and the real VM subnet is derived from vm_gateway
(NewIPPoolFromGateway's /24). Old config.json files keep loading — unknown
JSON keys are ignored — pinned by TestLoadConfig_IgnoresRemovedFields.

Co-Authored-By: <YourHandle>@<first8-of-your-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: `network.SubnetForGateway` — one derivation for pool and display

**Files:**
- Modify: `pkg/network/ip_pool.go:49-79`
- Modify: `pkg/network/ip_pool_test.go` (append one test)

- [x] **Step 1: Write the failing test**

Append to `pkg/network/ip_pool_test.go`:

```go
func TestSubnetForGateway(t *testing.T) {
	tests := []struct {
		gateway   string
		prefixLen int
		want      string
		wantErr   bool
	}{
		{"10.0.100.1", 24, "10.0.100.0/24", false},
		{"192.168.7.254", 24, "192.168.7.0/24", false},
		{"10.0.100.1", 16, "10.0.0.0/16", false},
		{"not-an-ip", 24, "", true},
		{"fe80::1", 24, "", true}, // IPv6: To4() fails, same as NewIPPoolFromGateway
	}
	for _, tt := range tests {
		got, err := SubnetForGateway(tt.gateway, tt.prefixLen)
		if tt.wantErr {
			if err == nil {
				t.Errorf("SubnetForGateway(%q, %d): expected error, got %v", tt.gateway, tt.prefixLen, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SubnetForGateway(%q, %d): unexpected error: %v", tt.gateway, tt.prefixLen, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("SubnetForGateway(%q, %d) = %q, want %q", tt.gateway, tt.prefixLen, got.String(), tt.want)
		}
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./pkg/network/ -run TestSubnetForGateway -v`
Expected: **compile failure** — `undefined: SubnetForGateway`.

- [x] **Step 3: Implement, and refactor `NewIPPoolFromGateway` onto it**

In `pkg/network/ip_pool.go`, replace `NewIPPoolFromGateway` (lines 49-79) with:

```go
// SubnetForGateway computes the IPv4 network that contains gateway, masked
// to prefixLen. This is the derivation NewIPPoolFromGateway uses to size the
// VM IP pool; anything that displays "the VM subnet" should go through it so
// the displayed value tracks the pool's actual network.
func SubnetForGateway(gateway string, prefixLen int) (*net.IPNet, error) {
	gwIP := net.ParseIP(gateway)
	if gwIP == nil {
		return nil, fmt.Errorf("invalid gateway IP: %s", gateway)
	}
	gwIP = gwIP.To4()
	if gwIP == nil {
		return nil, fmt.Errorf("gateway must be IPv4: %s", gateway)
	}
	mask := net.CIDRMask(prefixLen, 32)
	return &net.IPNet{IP: gwIP.Mask(mask), Mask: mask}, nil
}

// NewIPPoolFromGateway creates an IP pool from a gateway IP and prefix length.
// This is more robust than parsing CIDR from config strings.
func NewIPPoolFromGateway(gateway string, prefixLen int) (*IPPool, error) {
	ipNet, err := SubnetForGateway(gateway, prefixLen)
	if err != nil {
		return nil, err
	}

	pool := &IPPool{
		network:   ipNet,
		gateway:   gateway,
		allocated: make(map[string]string),
		available: make([]string, 0),
	}

	pool.generateAvailableIPs()
	return pool, nil
}
```

- [x] **Step 4: Run the package tests (refactor guard)**

Run: `CGO_ENABLED=0 go test ./pkg/network/ -v`
Expected: PASS — `TestSubnetForGateway` plus the pre-existing `TestNewIPPoolFromGateway` (253 available IPs, gateway preserved), proving the refactor changed nothing.

- [x] **Step 5: Commit**

```bash
git add pkg/network/ip_pool.go pkg/network/ip_pool_test.go
git commit -m "feat(PRI-2177): extract network.SubnetForGateway from NewIPPoolFromGateway

One derivation for gateway-masked-to-prefix, so the dashboard's subnet
display (next commit) and the IP pool can never disagree.

Co-Authored-By: <YourHandle>@<first8-of-your-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Dashboard settings shows the derived subnet

**Files:**
- Modify: `pkg/dashboard/server.go:14-26` (struct), `:470-481` (handleSettings), new setter
- Modify: `pkg/dashboard/server_test.go` (append one test)
- Modify: `pkg/daemon/daemon.go:161` (use new const), `:366` area (wire setter)

Threading rationale (Verified Facts #4): the settings handler is fully hardcoded and the `Server` has no config; the daemon owns `cfg` at the single `NewServer` call site. A setter avoids touching 38 `NewServer(...)` test call sites and keeps `pkg/dashboard` free of a `pkg/network` import. `SetVMSubnet` is called once, before `http.Server` starts serving (both happen inside the same `if d.cfg.HTTP.Enabled` block in `Daemon.Start`), so no synchronization is needed.

- [x] **Step 1: Write the failing test**

Append to `pkg/dashboard/server_test.go`:

```go
func TestServer_SettingsShowsDerivedVMSubnet(t *testing.T) {
	srv := NewServer(nil, "", "")
	srv.SetVMSubnet("10.7.7.0/24")

	req := httptest.NewRequest("GET", "/settings", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "10.7.7.0/24") {
		t.Errorf("settings page should show the subnet set by the daemon; body:\n%s", body)
	}
	if strings.Contains(body, "10.0.100.0/24") {
		t.Errorf("settings page still contains the old hardcoded literal")
	}
}
```

(Templates are embedded — Verified Facts #10 — so this renders the real settings.html, and `httptest`/`strings` are already imported by this file.)

- [x] **Step 2: Run the test to verify it fails**

Run: `CGO_ENABLED=0 go test ./pkg/dashboard/ -run TestServer_SettingsShowsDerivedVMSubnet -v`
Expected: **compile failure** — `srv.SetVMSubnet undefined`.

- [x] **Step 3: Implement the dashboard side**

In `pkg/dashboard/server.go`:

1. Add a field to the `Server` struct (after `vmUser string`, line 25):

```go
	vmUser          string
	vmSubnet        string
```

2. Add the setter immediately after `NewServer`:

```go
// SetVMSubnet sets the VM subnet string shown on the settings page. The
// daemon derives it from the configured vm_gateway (network.SubnetForGateway
// masked to the same prefix the IP pool uses) and sets it once before the
// HTTP server starts; it is display-only.
func (s *Server) SetVMSubnet(subnet string) {
	s.vmSubnet = subnet
}
```

3. In `handleSettings` (line 480), replace:

```go
		"VMSubnet":   "10.0.100.0/24",
```

with:

```go
		"VMSubnet":   s.vmSubnet,
```

(The `"VMSubnet"` map key stays — `templates/settings.html:53` renders `{{.VMSubnet}}`. When the setter was never called — e.g. other tests — the page shows an empty value instead of a fabricated one; the other hardcoded settings values are out of scope per the spec.)

- [x] **Step 4: Run the dashboard tests**

Run: `CGO_ENABLED=0 go test ./pkg/dashboard/ -v`
Expected: PASS, including the new test.

- [x] **Step 5: Wire the daemon**

In `pkg/daemon/daemon.go`:

1. Add a package-level const (after the imports, before the `Daemon` struct):

```go
// vmSubnetPrefixLen is the prefix length of the VM network. The IP pool and
// the dashboard's displayed subnet are both derived from
// firecracker.vm_gateway masked to this length — one source of truth.
const vmSubnetPrefixLen = 24
```

2. At line 161, replace the literal:

```go
		ipPool, err := network.NewIPPoolFromGateway(cfg.Firecracker.VMGateway, 24)
```

with:

```go
		ipPool, err := network.NewIPPoolFromGateway(cfg.Firecracker.VMGateway, vmSubnetPrefixLen)
```

3. In `Start`, immediately after the `dashboard.NewServer` line (366):

```go
		d.dashboardServer = dashboard.NewServer(adapter, d.cfg.VM.User, d.cfg.AppleContainer.ContainerBin)
		if subnet, err := network.SubnetForGateway(d.cfg.Firecracker.VMGateway, vmSubnetPrefixLen); err == nil {
			d.dashboardServer.SetVMSubnet(subnet.String())
		}
```

(`network` is already imported by daemon.go. On an unparseable gateway the display stays empty rather than lying; the IP-pool path at line 161 still hard-errors as before. With default config the displayed value is identical to the old literal: 10.0.100.1/24 → "10.0.100.0/24".)

- [x] **Step 6: Build and run the full package tests**

Run: `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go test ./pkg/...`
Expected: build OK, all PASS.

- [x] **Step 7: Commit**

```bash
git add pkg/dashboard/server.go pkg/dashboard/server_test.go pkg/daemon/daemon.go
git commit -m "feat(PRI-2177): derive dashboard VM subnet from the configured gateway

handleSettings showed a hardcoded 10.0.100.0/24; it now displays
SubnetForGateway(vm_gateway, /24) — the same derivation that sizes the IP
pool — set once by the daemon at dashboard construction. vmSubnetPrefixLen
names the shared /24.

Co-Authored-By: <YourHandle>@<first8-of-your-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: Docs — kill phantom targets, capture real semantics

**Files:**
- Modify: `CLAUDE.md` (Building/Deploying/Testing sections; Project Structure stays)
- Modify: `README.md:101` (backend paragraph)
- Modify: `vm-image/README.md:113-159` (ZFS/Initial Setup/Updating sections)

No code; no TDD. `docs/image-contract.md` was audited and is already correct post-PRI-2178 — leave it alone. `vm-image/macos/README.md` named in the spec **does not exist** (Verified Facts #8); the macOS story is covered by the root README and image-contract.md. Archival docs that mention `stockyard configure` (`docs/INITIAL_PROMPT.md`, `docs/plans/2026-01-16-*`) are historical artifacts — deliberately untouched.

- [x] **Step 1: Rewrite CLAUDE.md's build/deploy/test sections**

Replace everything in `CLAUDE.md` from `# Stockyard Development Guide` down to (but not including) `## Project Structure` with:

````markdown
# Stockyard Development Guide

## Building

```bash
make build          # Build all binaries to bin/
make build-guest    # Build guest binaries for VM (static Linux binaries)
```

## Testing

```bash
make test           # Package tests (./pkg/... only)
go test ./cmd/...   # CLI tests — NOT covered by make test or CI; run them
```

## Deploying VM Images

There are no root-Makefile deploy targets, and image deploys never restart
the daemon: both targets below register through `stockyard image import`,
and the daemon replaces images live (destroying only tasks on the replaced
image).

```bash
make -C vm-image deploy                              # Linux: build + register the default Firecracker image
make -C vm-image deploy-image REGISTRY_IMAGE=<name>  # Linux: register an already-built rootfs under a name
make container-image                                 # macOS: build the Apple container OCI image
                                                     #        (stockyard.local/stockyard-vm:container)
```

## Config Semantics Worth Knowing

- `firecracker.rootfs_path` — the seed for the image registry's `default`
  image (startup self-heal); also gates Firecracker backend construction.
- `apple_container.image` — the daemon's default task image on macOS,
  overridable per task with `stockyard run --image`.

````

Leave the `## Project Structure` section exactly as it is.

- [x] **Step 2: Update the README backend paragraph**

In `README.md`, replace the paragraph at line 101 ("The top-level `backend` key selects the VM backend. ...") with:

```markdown
The top-level `backend` key selects the VM backend; `stockyard init --backend` writes it explicitly. Valid values are `"firecracker"` (Linux) and `"apple-container"` (macOS); an empty value means firecracker. The apple-container backend skips the Firecracker-only setup steps — no ZFS, no kernel/rootfs install — and uses Apple's `container` CLI to manage VMs. `apple_container.image` (seeded by `stockyard init --image`) is the daemon's *default* task image, overridable per task with `stockyard run --image` (e.g. `"stockyard.local/stockyard-vm:container"`, built by `make container-image`). On the Firecracker side, `firecracker.rootfs_path` seeds the image registry's `default` image; additional images are registered with `stockyard image import`.
```

- [x] **Step 3: Fix vm-image/README.md's pre-registry drift**

In `vm-image/README.md`, replace from the line `The daemon auto-imports the base image on first startup from \`Firecracker.RootfsPath\` config.` (line 131) through the end of the `### Updating` section (line 159) with:

````markdown
The registry's `default` image is seeded from the `firecracker.rootfs_path`
config on daemon startup (self-heal); day-to-day, images are registered
explicitly through the image registry — no manual `zfs` commands and no
daemon restart.

### Initial Setup

```bash
# Build, install, and register the default image (kernel + rootfs):
make deploy
```

This copies `output/vmlinux.bin` and `output/rootfs.ext4` to
`/var/lib/stockyard/` and registers the rootfs via
`stockyard image import default`. The daemon imports it into ZFS itself.

### Updating

Re-run the same target. The daemon replaces the image atomically: it
destroys only the tasks running the replaced image, then re-imports.

```bash
make deploy                              # rebuild + replace 'default'
make deploy-image REGISTRY_IMAGE=<name>  # register an already-built rootfs under a name
```
````

(The "Quick Start"/"Individual Targets"/kernel sections above line 131 match today's Makefile — verified — and stay as they are. The manual `sudo cp` + `zfs snapshot` instructions being deleted are the pre-PRI-2150 flow.)

- [x] **Step 4: Sanity-check the docs against reality**

Run: `rg -n "deploy-daemon|make deploy " CLAUDE.md README.md vm-image/README.md; rg -n "deploy:|deploy-image:|container-image:" vm-image/Makefile Makefile`
Expected: first rg finds no phantom root-level targets (vm-image/README.md's `make deploy` lines refer to the vm-image Makefile where those targets exist); second rg confirms the referenced targets exist (`deploy`, `deploy-image` in vm-image/Makefile; `container-image` in both).

- [x] **Step 5: Commit**

```bash
git add CLAUDE.md README.md vm-image/README.md
git commit -m "docs(PRI-2177): replace phantom deploy targets with the real image flows

CLAUDE.md described make deploy-daemon/deploy-image/deploy at the root —
none exist. Document the real flows (make build/test, vm-image deploy ->
stockyard image import, container-image for macOS) and the two PRI-2150
semantics: rootfs_path seeds the registry default; apple_container.image is
the per-task-overridable default. vm-image/README.md loses its pre-registry
manual ZFS install instructions. (vm-image/macos/README.md named in the
spec does not exist at HEAD; docs/image-contract.md is already current.)

Co-Authored-By: <YourHandle>@<first8-of-your-session-id> (claude-fable-5)
Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: Verification + live CLI smoke

No file changes. Proves the build, both test suites, and the real `init` → config.json → daemon → `image ls` path. The smoke runs against an **isolated scratch instance** — never the real daemon socket. Scaffolding is hardened the same way as the PRI-2176 plan: fixed scratch path, pidfile, and a guard step, because **state does not survive between separate shell invocations** — an empty `$SCRATCH` would make `STOCKYARD_CONFIG_DIR=""` silently fall through to the real config (`pkg/config/config.go:159`).

- [x] **Step 1: Full build**

Run:

```bash
make build
```

Expected: exit 0; produces `bin/stockyard`, `bin/stockyardd`, and the guest binaries.

- [x] **Step 2: Run both test suites**

Run:

```bash
make test && CGO_ENABLED=0 go test ./cmd/... -v
```

Expected: both pass. (`make test` covers only `./pkg/...`; the explicit `./cmd/...` run is required because neither the Makefile nor CI runs CLI-package tests.)

- [x] **Step 3: Cross-compile check (linux)**

Run:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
```

Expected: exit 0. (init.go's `runtime.GOOS` branch and the daemon's linux-only files must still compile.)

- [x] **Step 4: Smoke the new init against a fresh scratch config dir**

Preconditions: Apple's container service must be running — `container image ls` should answer (if not: `container system start`). Pick a real image reference from `container image ls` and substitute it for `IMAGE_REF`.

```bash
SCRATCH=/tmp/stockyard-pri2177-smoke
rm -rf "$SCRATCH" /tmp/stockyard-pri2177-smoke-fc
mkdir -p "$SCRATCH/secrets" "$SCRATCH/data"
IMAGE_REF="stockyard.local/stockyard-vm:container"   # <- replace with a real REFERENCE from 'container image ls'

# 1. Fresh init, platform default (this machine is darwin -> apple-container)
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard init --instance smoke
# Expected: exit 0; output contains:
#   Initialized stockyard instance "smoke" (backend: apple-container)
#   a "Next steps" block mentioning 'container image ls' and 'make -C vm-image container-image'
#   a "Secrets (optional — tasks run without them)" block naming op://Stockyard/smoke/
#   and /etc/stockyard/secrets

cat "$SCRATCH/config.json"
# Expected: "instance_id": "smoke"; "backend": "apple-container";
# secrets has "prefix": "smoke" and NO "provider" key;
# firecracker has NO "vm_subnet" key.

# 2. --image + firecracker is rejected, config untouched
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard init --instance smoke --backend firecracker --image foo:latest
# Expected: exit 1; error contains '--image is only valid with --backend apple-container'
grep '"backend"' "$SCRATCH/config.json"
# Expected: still "backend": "apple-container"

# 3. Unknown backend is rejected
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard init --instance smoke --backend qemu
# Expected: exit 1; error contains 'invalid --backend "qemu"'

# 4. Explicit firecracker on a second fresh dir
SCRATCH_FC=/tmp/stockyard-pri2177-smoke-fc
STOCKYARD_CONFIG_DIR="$SCRATCH_FC" ./bin/stockyard init --instance smoke-fc --backend firecracker
# Expected: exit 0; '(backend: firecracker)'; next steps mention
# 'make -C vm-image deploy' and 'scripts/stockyardd.service'
grep '"backend"' "$SCRATCH_FC/config.json"
# Expected: "backend": "firecracker"

# 5. configure is gone
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard configure
# Expected: exit 1; output contains 'unknown command "configure" for "stockyard"'

# 6. Re-init with --image: overwrite warning + image seeded
STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard init --instance smoke --backend apple-container --image "$IMAGE_REF"
# Expected: exit 0; 'Warning: overwriting existing instance ID "smoke"';
# next steps now print 'Default task image: <IMAGE_REF>'
grep -A2 '"apple_container"' "$SCRATCH/config.json"
# Expected: "image": "<IMAGE_REF>"
```

- [x] **Step 5: Start the scratch daemon on the init-produced config**

`init` writes the **default** daemon paths (the real socket and `/var/lib/stockyard`), so point them at the scratch dir before starting (this is the documented scratch-instance recipe, and exactly why the Step 6 guard exists):

```bash
SCRATCH=/tmp/stockyard-pri2177-smoke
python3 - "$SCRATCH" <<'EOF'
import json, sys
scratch = sys.argv[1]
path = scratch + "/config.json"
with open(path) as f:
    cfg = json.load(f)
cfg["daemon"]["socket_path"] = scratch + "/stockyardd.sock"
cfg["daemon"]["data_dir"] = scratch + "/data"
cfg["secrets"]["dir"] = scratch + "/secrets"
with open(path, "w") as f:
    json.dump(cfg, f, indent=2)
EOF

STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyardd > "$SCRATCH/daemon.log" 2>&1 &
echo $! > "$SCRATCH/daemon.pid"
for i in $(seq 1 20); do [ -S "$SCRATCH/stockyardd.sock" ] && break; sleep 0.5; done
[ -S "$SCRATCH/stockyardd.sock" ] && echo "daemon up" || { echo "daemon failed"; cat "$SCRATCH/daemon.log"; }
```

Expected: `daemon up`. (Startup fail-fasts if `apple_container.image` is empty or `container system status` fails — both were satisfied in Step 4. If it fails, the log says which.)

- [x] **Step 6: Guarded daemon-path check — `image ls` answers**

```bash
SCRATCH=/tmp/stockyard-pri2177-smoke
[ -S "$SCRATCH/stockyardd.sock" ] || { echo "scratch daemon not up — STOP, redo Step 5"; exit 1; }
[ -z "${STOCKYARD_URL:-}" ] || { echo "STOCKYARD_URL is set and would bypass the scratch socket — unset it first"; exit 1; }

STOCKYARD_CONFIG_DIR="$SCRATCH" ./bin/stockyard image ls
# Expected: exit 0; a listing of the container CLI's local image store that
# includes $IMAGE_REF. Reaching the listing proves CLI -> scratch socket ->
# daemon -> apple-container ImageLister, i.e. the init-produced config drives
# a working daemon end to end.
```

- [x] **Step 7: Tear down the scratch instance**

```bash
SCRATCH=/tmp/stockyard-pri2177-smoke
kill "$(cat "$SCRATCH/daemon.pid")"
rm -rf "$SCRATCH" /tmp/stockyard-pri2177-smoke-fc
```

Expected: daemon stops; scratch dirs removed. Nothing touched the real daemon, socket, or data dir.

- [x] **Step 8: Confirm working tree and history**

```bash
git status --short && git log --oneline main..HEAD
```

Expected: empty status; the plan/spec docs commits plus the six code/docs commits from Tasks 1-6 (one each) on branch `matt/pri-2177-configcli-cleanup-pass-dead-config-fields-stale`. **Do not push; do not open a PR** — hand back for review.

---

## Notes for the Implementer

- **Spec deviations found at HEAD (none change the design):** (1) `vm-image/macos/README.md` does not exist — the audit lands in `vm-image/README.md` + root `README.md`, and `docs/image-contract.md` is already correct; (2) `server_test.go:107`'s "gateway fake" is `MockDaemon.GetVMIP` (terminal path) — the settings page never consults the daemon, hence the setter design; (3) `configure.go` has no test file to delete.
- The dashboard isn't exercised by the smoke (the scratch config leaves `http.enabled` false, and the dashboard sits behind Tailscale auth middleware); its behavior is covered by the Task 5 unit test.
- If smoke Step 6 returns a connection error (`failed to connect`/`no such file`), the CLI is not finding the scratch config — confirm `STOCKYARD_CONFIG_DIR` is set on the CLI invocation itself, not just the daemon.
- The two `Co-Authored-By` trailer lines in every commit template are mandatory; replace `<YourHandle>@<first8-of-your-session-id>` with your own handle.

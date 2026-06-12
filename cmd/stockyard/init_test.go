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

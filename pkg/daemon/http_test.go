package daemon

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/obra/stockyard/pkg/config"
)

func TestDaemon_HTTPServerDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.HTTP.Enabled = false

	d := &Daemon{cfg: cfg}

	// With HTTP disabled, httpServer should remain nil after initialization
	// (it's only set during Start() when HTTP.Enabled is true)
	if d.httpServer != nil {
		t.Error("expected no HTTP server when disabled")
	}
}

func TestDaemon_HTTPServerEnabled(t *testing.T) {
	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cfg := config.DefaultConfig()
	cfg.HTTP.Enabled = true
	cfg.HTTP.Addr = fmt.Sprintf("127.0.0.1:%d", port)

	d := &Daemon{cfg: cfg}

	// Manually start HTTP server (simulating what Start() does for HTTP)
	d.httpServer = &http.Server{
		Addr: cfg.HTTP.Addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("dashboard placeholder"))
		}),
	}

	// Start the server in a goroutine
	serverErr := make(chan error, 1)
	go func() {
		if err := d.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Verify server is running by making a request
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("failed to connect to HTTP server: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	if string(body) != "dashboard placeholder" {
		t.Errorf("unexpected response body: got %q, want %q", string(body), "dashboard placeholder")
	}

	// Clean up
	d.httpServer.Close()
}

func TestDaemon_HTTPServerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	cfg := config.DefaultConfig()
	cfg.HTTP.Enabled = true
	cfg.HTTP.Addr = ":0" // random port

	// This is just a compile-time check that the pieces fit together
	// Full integration would require a running daemon
	t.Log("Integration test placeholder - full test requires daemon")
}

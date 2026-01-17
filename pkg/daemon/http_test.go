package daemon

import (
	"testing"

	"github.com/obra/stockyard/pkg/config"
)

func TestDaemon_HTTPServerDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.HTTP.Enabled = false

	d := &Daemon{cfg: cfg}

	if d.httpServer != nil {
		t.Error("expected no HTTP server when disabled")
	}
}

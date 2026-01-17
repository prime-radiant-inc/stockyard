package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/secrets"
	"github.com/obra/stockyard/pkg/zfs"
)

// Daemon is the core daemon process that manages workspaces and tasks.
type Daemon struct {
	cfg      *config.Config
	secrets  secrets.Provider
	zfs      *zfs.Manager
	state    *State

	listener net.Listener
	mu       sync.Mutex
	running  bool
}

// New creates a new Daemon instance with the given configuration and secrets provider.
func New(cfg *config.Config, secretsProvider secrets.Provider) (*Daemon, error) {
	zfsMgr := zfs.NewManager(cfg.ZFS.Pool, cfg.ZFS.BasePath)

	state, err := NewState()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state: %w", err)
	}

	return &Daemon{
		cfg:     cfg,
		secrets: secretsProvider,
		zfs:     zfsMgr,
		state:   state,
	}, nil
}

// Start begins the daemon, listening on the configured Unix socket.
// It blocks until the context is cancelled or an error occurs.
func (d *Daemon) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("daemon already running")
	}
	d.running = true
	d.mu.Unlock()

	socketDir := filepath.Dir(d.cfg.Daemon.SocketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	// Remove any existing socket file
	os.Remove(d.cfg.Daemon.SocketPath)

	listener, err := net.Listen("unix", d.cfg.Daemon.SocketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on socket: %w", err)
	}
	d.listener = listener

	fmt.Printf("Daemon listening on %s\n", d.cfg.Daemon.SocketPath)

	// TODO: Start gRPC server here

	<-ctx.Done()
	return d.Stop()
}

// Stop gracefully shuts down the daemon.
func (d *Daemon) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running {
		return nil
	}

	d.running = false

	if d.listener != nil {
		d.listener.Close()
	}

	if d.state != nil {
		d.state.Close()
	}

	return nil
}

// State returns the daemon's state manager.
func (d *Daemon) State() *State {
	return d.state
}

// ZFS returns the daemon's ZFS manager.
func (d *Daemon) ZFS() *zfs.Manager {
	return d.zfs
}

// Secrets returns the daemon's secrets provider.
func (d *Daemon) Secrets() secrets.Provider {
	return d.secrets
}

// Config returns the daemon's configuration.
func (d *Daemon) Config() *config.Config {
	return d.cfg
}

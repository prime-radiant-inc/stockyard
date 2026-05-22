//go:build darwin

package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createAppleContainerBackend(cfg *config.Config) (vmbackend.Backend, error) {
	bin := cfg.AppleContainer.ContainerBin
	if bin == "" {
		bin = "container"
	}

	// Fail-fast probe: confirm the `container` service is reachable.
	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(probeCtx, bin, "system", "status").Run(); err != nil {
		return nil, fmt.Errorf(
			"apple-container backend: `%s system status` failed (%w); "+
				"is the container service running? try `container system start`", bin, err)
	}

	acCfg := vmbackend.AppleContainerConfig{
		ContainerBin: bin,
		Image:        cfg.AppleContainer.Image,
		StateDir:     cfg.Daemon.DataDir + "/vms/stockyard",
	}
	return vmbackend.NewAppleContainerBackend(acCfg), nil
}

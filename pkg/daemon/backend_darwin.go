//go:build darwin

package daemon

import (
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createVfkitBackend(cfg *config.Config) (vmbackend.Backend, error) {
	vfkitCfg := vmbackend.VfkitConfig{
		VfkitBin:   cfg.Vfkit.VfkitBin,
		KernelPath: cfg.Vfkit.KernelPath,
		StateDir:   cfg.Daemon.DataDir + "/vms/stockyard",
	}
	return vmbackend.NewVfkitBackend(vfkitCfg), nil
}

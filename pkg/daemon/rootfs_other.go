//go:build !darwin

package daemon

import (
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/rootfs"
)

func createRootfsProvisioner(cfg *config.Config) rootfs.Provisioner {
	if cfg.Rootfs.Provider == "copy" && cfg.Rootfs.BaseImage != "" {
		return rootfs.NewCopyProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
	}
	return nil
}

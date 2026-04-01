//go:build darwin

package daemon

import (
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/rootfs"
)

func createRootfsProvisioner(cfg *config.Config) rootfs.Provisioner {
	switch cfg.Rootfs.Provider {
	case "apfs":
		return rootfs.NewAPFSProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
	case "copy":
		return rootfs.NewCopyProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
	default:
		if cfg.Rootfs.BaseImage != "" {
			return rootfs.NewAPFSProvisioner(cfg.Rootfs.BaseImage, cfg.Rootfs.VMsDir)
		}
		return nil
	}
}

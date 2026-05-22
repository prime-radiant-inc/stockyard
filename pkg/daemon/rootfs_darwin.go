//go:build darwin

package daemon

import (
	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/rootfs"
)

func createRootfsProvisioner(cfg *config.Config) rootfs.Provisioner {
	// apple-container owns its own rootfs; never provision one.
	if cfg.Backend == "apple-container" {
		return nil
	}
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

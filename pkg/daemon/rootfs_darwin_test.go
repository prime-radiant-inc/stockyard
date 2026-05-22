//go:build darwin

package daemon

import (
	"testing"

	"github.com/obra/stockyard/pkg/config"
)

func TestCreateRootfsProvisioner_AppleContainerReturnsNil(t *testing.T) {
	cfg := &config.Config{Backend: "apple-container"}
	cfg.Rootfs.BaseImage = "/some/stray/base.img" // would otherwise trigger APFS
	if p := createRootfsProvisioner(cfg); p != nil {
		t.Errorf("apple-container backend must yield a nil rootfs provisioner, got %T", p)
	}
}

func TestCreateRootfsProvisioner_VfkitStillProvisions(t *testing.T) {
	cfg := &config.Config{Backend: "vfkit"}
	cfg.Rootfs.BaseImage = "/some/base.img"
	if p := createRootfsProvisioner(cfg); p == nil {
		t.Error("vfkit with a base image must still yield an APFS provisioner")
	}
}

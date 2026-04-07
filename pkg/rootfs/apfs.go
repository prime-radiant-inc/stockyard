//go:build darwin

package rootfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type APFSProvisioner struct {
	baseImage string
	vmsDir    string
}

func NewAPFSProvisioner(baseImage, vmsDir string) Provisioner {
	return &APFSProvisioner{
		baseImage: baseImage,
		vmsDir:    vmsDir,
	}
}

func (p *APFSProvisioner) Clone(_ context.Context, vmID string) (string, error) {
	vmDir := filepath.Join(p.vmsDir, vmID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return "", fmt.Errorf("create VM directory: %w", err)
	}

	dst := filepath.Join(vmDir, "rootfs.img")
	if err := unix.Clonefile(p.baseImage, dst, 0); err != nil {
		os.RemoveAll(vmDir)
		return "", fmt.Errorf("clonefile: %w", err)
	}
	return dst, nil
}

func (p *APFSProvisioner) Destroy(_ context.Context, vmID string) error {
	vmDir := filepath.Join(p.vmsDir, vmID)
	return os.RemoveAll(vmDir)
}

func (p *APFSProvisioner) EnsureBase(_ context.Context) error {
	if _, err := os.Stat(p.baseImage); err != nil {
		return fmt.Errorf("base image not found: %w", err)
	}
	return nil
}

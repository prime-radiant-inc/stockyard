package rootfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type CopyProvisioner struct {
	baseImage string
	vmsDir    string
}

func NewCopyProvisioner(baseImage, vmsDir string) Provisioner {
	return &CopyProvisioner{
		baseImage: baseImage,
		vmsDir:    vmsDir,
	}
}

func (p *CopyProvisioner) Clone(_ context.Context, vmID string) (string, error) {
	vmDir := filepath.Join(p.vmsDir, vmID)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return "", fmt.Errorf("create VM directory: %w", err)
	}

	dst := filepath.Join(vmDir, "rootfs.img")
	if err := copyFile(p.baseImage, dst); err != nil {
		os.RemoveAll(vmDir)
		return "", fmt.Errorf("copy rootfs: %w", err)
	}
	return dst, nil
}

func (p *CopyProvisioner) Destroy(_ context.Context, vmID string) error {
	vmDir := filepath.Join(p.vmsDir, vmID)
	return os.RemoveAll(vmDir)
}

func (p *CopyProvisioner) EnsureBase(_ context.Context) error {
	if _, err := os.Stat(p.baseImage); err != nil {
		return fmt.Errorf("base image not found: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	success := false
	defer func() {
		out.Close()
		if !success {
			os.Remove(dst)
		}
	}()

	buf := make([]byte, 4*1024*1024)
	if _, err := io.CopyBuffer(out, in, buf); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}

	success = true
	return nil
}

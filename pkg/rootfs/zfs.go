package rootfs

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/obra/stockyard/pkg/zfs"
)

type ZFSProvisioner struct {
	zfsMgr     *zfs.Manager
	pool       string
	imagesPath string
	vmsPath    string
}

func NewZFSProvisioner(zfsMgr *zfs.Manager, pool, imagesPath, vmsPath string) Provisioner {
	return &ZFSProvisioner{
		zfsMgr:     zfsMgr,
		pool:       pool,
		imagesPath: imagesPath,
		vmsPath:    vmsPath,
	}
}

func (p *ZFSProvisioner) Clone(ctx context.Context, vmID string) (string, error) {
	if p.zfsMgr == nil {
		return "", fmt.Errorf("ZFS manager not available")
	}

	snapshotPath := fmt.Sprintf("%s/%s/rootfs@base", p.pool, p.imagesPath)
	vmDatasetPath := fmt.Sprintf("%s/%s/%s", p.pool, p.vmsPath, vmID)

	parentDataset := fmt.Sprintf("%s/%s", p.pool, p.vmsPath)
	exec.CommandContext(ctx, "zfs", "create", "-p", parentDataset).Run()

	cmd := exec.CommandContext(ctx, "zfs", "clone", snapshotPath, vmDatasetPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("zfs clone failed: %w: %s", err, string(output))
	}

	cmd = exec.CommandContext(ctx, "zfs", "get", "-H", "-o", "value", "mountpoint", vmDatasetPath)
	output, err := cmd.Output()
	if err != nil {
		exec.CommandContext(ctx, "zfs", "destroy", "-r", vmDatasetPath).Run()
		return "", fmt.Errorf("zfs get mountpoint failed: %w", err)
	}

	mountpoint := strings.TrimSpace(string(output))
	return filepath.Join(mountpoint, "rootfs.ext4"), nil
}

func (p *ZFSProvisioner) Destroy(ctx context.Context, vmID string) error {
	if p.zfsMgr == nil {
		return nil
	}
	vmDatasetPath := fmt.Sprintf("%s/%s/%s", p.pool, p.vmsPath, vmID)
	cmd := exec.CommandContext(ctx, "zfs", "destroy", "-r", vmDatasetPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zfs destroy failed: %w: %s", err, string(output))
	}
	return nil
}

func (p *ZFSProvisioner) EnsureBase(ctx context.Context) error {
	snapshotPath := fmt.Sprintf("%s/%s/rootfs@base", p.pool, p.imagesPath)
	cmd := exec.CommandContext(ctx, "zfs", "list", "-t", "snapshot", snapshotPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("base snapshot %s not found: %w", snapshotPath, err)
	}
	return nil
}

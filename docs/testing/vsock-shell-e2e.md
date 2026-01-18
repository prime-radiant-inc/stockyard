# vsock Shell End-to-End Testing

## Prerequisites

1. Working stockyard deployment
2. Rebuilt VM image with stockyard-shell

## Build and Deploy VM Image

```bash
# Build stockyard-shell
make build-shell

# Build VM image
cd vm-image
make

# Install to ZFS (adjust paths as needed)
sudo zfs destroy tank/stockyard/images/rootfs@base 2>/dev/null || true
sudo cp output/rootfs.ext4 /tank/stockyard/images/rootfs/rootfs.ext4
sudo zfs snapshot tank/stockyard/images/rootfs@base
sudo cp output/vmlinux.bin /var/lib/stockyard/vmlinux.bin
```

## Test Shell Service in VM

1. Create a new VM:
   ```bash
   stockyard run github.com/your/repo --name test-shell
   ```

2. Check service is running (SSH or console):
   ```bash
   systemctl status stockyard-shell
   journalctl -u stockyard-shell -f
   ```

3. Test from host (requires vsock tools or dashboard):
   - Open dashboard at http://localhost:65432
   - Navigate to VM detail page
   - Click "Open Terminal"
   - Verify shell prompt appears
   - Test typing, output, resize

## Troubleshooting

- **Service won't start**: Check `journalctl -u stockyard-shell`
- **Connection refused**: Verify vsock is enabled in Firecracker config
- **Permission denied**: Ensure service runs as root
- **No TERM**: Check Open message includes term field

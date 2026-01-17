# Firecracker API Mode Refactor

**Status: IMPLEMENTED** (2026-01-17)

## Overview

Refactor the stockyard firecracker client from `--no-api` mode to full API mode, enabling MMDS (Microvm Metadata Service) for cloud-init data delivery.

## Implementation Summary

The refactor was completed with the following changes:

- **`pkg/firecracker/api.go`** - New APIClient for HTTP communication over Unix socket
- **`pkg/firecracker/client.go`** - CreateVM now uses API mode; StopVM uses graceful shutdown
- **`pkg/firecracker/cloudinit.go`** - Added BuildMMDSData helper for MMDS format
- **`pkg/firecracker/types.go`** - Added VMInfo struct with APISocketPath
- **`pkg/firecracker/config.go`** - Removed (config file generation no longer needed)

See implementation plan: `docs/plans/2026-01-17-firecracker-api-mode.md`

### Integration Test Results

The API mode refactor works correctly:
- VM creation via Firecracker API socket ✓
- MMDS config sent to `/mmds/config` ✓
- MMDS data sent to `/mmds` ✓
- VM boots successfully ✓
- Graceful shutdown via `SendCtrlAltDel` ✓

### Remaining Work (VM Image)

The VM image needs to be updated to configure cloud-init for IMDS datasource.
Currently cloud-init only uses NoCloud. Add to `vm-image/Dockerfile`:

```dockerfile
# Configure cloud-init to use IMDS for Firecracker MMDS
RUN mkdir -p /etc/cloud/cloud.cfg.d && \
    echo 'datasource_list: [ IMDS, NoCloud, None ]' > /etc/cloud/cloud.cfg.d/99-datasource.cfg && \
    echo 'datasource:' >> /etc/cloud/cloud.cfg.d/99-datasource.cfg && \
    echo '  IMDS:' >> /etc/cloud/cloud.cfg.d/99-datasource.cfg && \
    echo '    metadata_urls: ["http://169.254.169.254"]' >> /etc/cloud/cloud.cfg.d/99-datasource.cfg
```

This is tracked separately from the API mode refactor.

## Original State

- `pkg/firecracker/client.go` started firecracker with `--no-api --config-file`
- Cloud-init data was generated but never delivered to the VM
- VMs booted but didn't receive hostname, tailscale auth key, or environment variables

## Target State

- Firecracker starts with `--api-sock` pointing to a Unix socket
- Client configures VM via HTTP API calls to the socket
- MMDS delivers cloud-init compatible metadata to the guest
- Cloud-init in guest retrieves data via IMDS datasource (169.254.169.254)

## Firecracker API Workflow

### 1. Start Firecracker Process
```bash
firecracker --api-sock /var/lib/stockyard/vms/{ns}/{id}/api.sock
```

Process starts and waits for configuration via API.

### 2. Configure VM via API

Each call is an HTTP request to the Unix socket:

```
PUT /boot-source
{
  "kernel_image_path": "/tmp/vmlinux.bin",
  "boot_args": "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw"
}

PUT /drives/rootfs
{
  "drive_id": "rootfs",
  "path_on_host": "/path/to/rootfs.ext4",
  "is_root_device": true,
  "is_read_only": false
}

PUT /network-interfaces/eth0
{
  "iface_id": "eth0",
  "guest_mac": "02:xx:xx:xx:xx:xx",
  "host_dev_name": "tap-{vmid}"
}

PUT /machine-config
{
  "vcpu_count": 2,
  "mem_size_mib": 1024,
  "smt": false
}
```

### 3. Configure MMDS

```
PUT /mmds/config
{
  "network_interfaces": ["eth0"],
  "version": "V2"
}

PUT /mmds
{
  "latest": {
    "meta-data": {
      "instance-id": "{vmid}",
      "local-hostname": "stockyard-{vmid}"
    },
    "user-data": "<base64-encoded cloud-init yaml>"
  }
}
```

### 4. Start the VM

```
PUT /actions
{
  "action_type": "InstanceStart"
}
```

## Guest Configuration

Cloud-init must be configured to use IMDS datasource. The VM image already has cloud-init; we need to ensure it checks the metadata service.

Create `/etc/cloud/cloud.cfg.d/99-datasource.cfg`:
```yaml
datasource_list: [ NoCloud, IMDS, None ]
datasource:
  IMDS:
    metadata_urls: ["http://169.254.169.254"]
```

Or use the NoCloud-compatible MMDS format that cloud-init recognizes.

## API Client Design

### New Files

- `pkg/firecracker/api.go` - HTTP client for Firecracker API over Unix socket

### Modified Files

- `pkg/firecracker/client.go` - Replace config-file approach with API calls
- `pkg/firecracker/config.go` - May need adjustments for API payloads

### Key Types

```go
// APIClient handles HTTP communication with Firecracker API socket
type APIClient struct {
    socketPath string
    httpClient *http.Client
}

func (a *APIClient) SetBootSource(kernel, bootArgs string) error
func (a *APIClient) SetDrive(id, path string, isRoot, isReadOnly bool) error
func (a *APIClient) SetNetworkInterface(id, mac, hostDev string) error
func (a *APIClient) SetMachineConfig(vcpus int32, memMB int32) error
func (a *APIClient) SetMMDSConfig(ifaces []string) error
func (a *APIClient) SetMMDSData(data map[string]interface{}) error
func (a *APIClient) StartInstance() error
```

## MMDS Data Format

Cloud-init expects data in a specific format. For IMDS datasource:

```json
{
  "latest": {
    "meta-data": {
      "instance-id": "i-9193eb30",
      "local-hostname": "stockyard-9193eb30",
      "local-ipv4": ""
    },
    "user-data": "#cloud-config\nhostname: stockyard-9193eb30\n..."
  }
}
```

The user-data should NOT be base64-encoded for MMDS - cloud-init fetches it as-is.

## VM Lifecycle Changes

### CreateVM
1. Create state directory
2. Create TAP device
3. Copy rootfs
4. Start firecracker process with `--api-sock`
5. Wait for API socket to be ready
6. Configure via API (boot source, drives, network, machine config)
7. Configure MMDS with cloud-init data
8. Start instance via API
9. Return VM info

### StopVM
- Can now use `PUT /actions {"action_type": "SendCtrlAltDel"}` for graceful shutdown
- Fall back to SIGTERM/SIGKILL

### GetVM
- Can query `GET /` or `GET /machine-config` for VM state
- More accurate status than just checking PID

## Error Handling

- API socket may take a moment to be ready after process start
- Implement retry with backoff for initial connection
- All API calls should have timeouts
- On any configuration error, kill process and clean up

## Testing

1. Unit tests for API client with mock HTTP responses
2. Integration test that boots a VM and verifies MMDS data is accessible
3. Verify cloud-init runs successfully with tailscale auth key

## Migration

- Existing VMs created with old method won't have API sockets
- GetVM/DeleteVM should handle both cases during transition
- Can detect by presence of `api.sock` file

## Dependencies

- No new Go dependencies (stdlib `net/http` supports Unix sockets)

## Risks

- API socket readiness timing
- MMDS data format compatibility with cloud-init
- Firecracker version compatibility (MMDS V2 requires recent versions)

## Success Criteria

1. VM boots and receives cloud-init data via MMDS
2. Tailscale connects automatically using auth key from metadata
3. Hostname is set correctly
4. Environment variables are available

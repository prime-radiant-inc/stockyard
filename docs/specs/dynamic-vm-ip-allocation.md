# Dynamic VM IP Allocation via DHCP

## Status
Implemented

## Problem

All Stockyard VMs are configured with the same static IP address (`192.168.100.2/24`) baked into the rootfs image. When multiple VMs run simultaneously, they create IP conflicts on the bridge network, causing:

- ARP table confusion on the bridge
- Intermittent connectivity
- SSH connections that work briefly then freeze
- Unpredictable packet routing

## Solution

Run dnsmasq as a managed child process of the stockyard daemon. VMs use standard DHCP to obtain unique IPs at boot time.

---

## Network Design

### Subnet

**CIDR:** `192.168.64.0/18`

| Range | Count |
|-------|-------|
| Network | 192.168.64.0 |
| Usable | 192.168.64.1 - 192.168.127.254 |
| Broadcast | 192.168.127.255 |
| Total hosts | 16,382 |

### Address Allocation

| Address | Purpose |
|---------|---------|
| 192.168.64.1 | Bridge gateway (flbr0) |
| 192.168.64.2 - 192.168.127.254 | DHCP pool (16,381 addresses) |

### DNS

VMs use `8.8.8.8` (unchanged from current config).

---

## Architecture

### Why dnsmasq?

- Battle-tested DHCP implementation
- Handles all edge cases (lease management, expiry, reuse)
- Zero custom allocation code
- Lease expiry automatically reclaims IPs from dead VMs
- Simple config, complex protocol handled for us

### Why Managed Child Process?

- No separate systemd service to configure
- Lifecycle tied to stockyard daemon
- Config generated from stockyard settings
- Single source of truth for network configuration

---

## Component Changes

### 1. Configuration (`pkg/config/config.go`)

Add to `FirecrackerConfig`:

```go
type FirecrackerConfig struct {
    KernelPath   string `json:"kernel_path"`
    RootfsPath   string `json:"rootfs_path"`
    BridgeName   string `json:"bridge_name"`
    VMSubnet     string `json:"vm_subnet"`     // Default: "192.168.64.0/18"
    VMGateway    string `json:"vm_gateway"`    // Default: "192.168.64.1"
    DHCPRangeStart string `json:"dhcp_range_start"` // Default: "192.168.64.2"
    DHCPRangeEnd   string `json:"dhcp_range_end"`   // Default: "192.168.127.254"
    DHCPLeaseTime  string `json:"dhcp_lease_time"`  // Default: "12h"
}
```

### 2. DHCP Server (`pkg/network/dhcp.go`)

New file managing dnsmasq:

```go
type DHCPServer struct {
    cmd        *exec.Cmd
    configPath string
    leasePath  string
    bridge     string
    gateway    string
    rangeStart string
    rangeEnd   string
    netmask    string
    leaseTime  string
}

func NewDHCPServer(cfg *config.FirecrackerConfig, dataDir string) (*DHCPServer, error)
func (d *DHCPServer) Start() error
func (d *DHCPServer) Stop() error
func (d *DHCPServer) GetIPForMAC(mac string) (string, bool)
func (d *DHCPServer) writeConfig() error
```

**Generated config** (`/var/lib/stockyard/dnsmasq.conf`):

```
interface=flbr0
bind-interfaces
dhcp-range=192.168.64.2,192.168.127.254,255.255.192.0,12h
dhcp-option=option:router,192.168.64.1
dhcp-option=option:dns-server,8.8.8.8
dhcp-authoritative
log-dhcp
```

**Lease file:** `/var/lib/stockyard/dnsmasq.leases`

Format: `<expiry> <MAC> <IP> <hostname> <client-id>`

### 3. Daemon Integration (`cmd/stockyardd/main.go`)

```go
// Start DHCP server before accepting VM requests
dhcpServer, err := network.NewDHCPServer(cfg.Firecracker, dataDir)
if err != nil {
    log.Fatal("Failed to create DHCP server:", err)
}
if err := dhcpServer.Start(); err != nil {
    log.Fatal("Failed to start DHCP server:", err)
}
defer dhcpServer.Stop()
```

### 4. VM IP Lookup (`pkg/firecracker/client.go`)

To find a VM's IP:

```go
func (c *Client) GetVMIP(vmID string) (string, error) {
    // Get MAC from VM's mac_addr file
    mac, err := c.getVMMAC(vmID)
    if err != nil {
        return "", err
    }

    // Look up in DHCP leases
    ip, found := c.dhcpServer.GetIPForMAC(mac)
    if !found {
        return "", fmt.Errorf("no lease found for VM %s (MAC %s)", vmID, mac)
    }
    return ip, nil
}
```

### 5. VM Image Changes

#### Dockerfile

Remove static network config entirely:

```dockerfile
# REMOVED: Static IP configuration
# Network is configured via DHCP at boot
```

#### systemd-networkd config (`/etc/systemd/network/10-eth0.network`)

```ini
[Match]
Name=eth0

[Network]
DHCP=yes
DNS=8.8.8.8

[DHCP]
UseDNS=false
UseRoutes=true
```

#### Init Script (`vm-image/init/stockyard-init.sh`)

Remove manual network configuration. Add MMDS route only:

```bash
# Ensure MMDS route exists (DHCP handles the rest)
# Wait for DHCP to configure eth0
for i in $(seq 1 30); do
    if ip addr show eth0 | grep -q "inet "; then
        break
    fi
    sleep 0.5
done

# Add MMDS route (link-local, doesn't conflict with DHCP)
ip route add 169.254.169.254/32 dev eth0 scope link 2>/dev/null || true
```

### 6. Host Bridge Setup

Update bridge configuration (one-time or via setup script):

**Current:**
```bash
ip addr add 192.168.100.1/24 dev flbr0
iptables -t nat -A POSTROUTING -s 192.168.100.0/24 ! -o flbr0 -j MASQUERADE
```

**New:**
```bash
ip addr add 192.168.64.1/18 dev flbr0
iptables -t nat -A POSTROUTING -s 192.168.64.0/18 ! -o flbr0 -j MASQUERADE
```

---

## Failure Handling

### dnsmasq Fails to Start
- Check if binary exists at startup, clear error message if missing
- Check if bridge exists, fail fast if not
- Log dnsmasq stderr for debugging

### dnsmasq Crashes During Operation
- Monitor child process
- Restart automatically with backoff
- Existing VMs keep their IPs (leases persisted to file)

### VM Doesn't Get IP
- DHCP lease file is authoritative
- Check if VM's MAC appears in leases
- Check dnsmasq logs for DHCP requests

### Daemon Crashes
- On restart, dnsmasq restarts with same lease file
- VMs may need to renew leases but IPs typically stay stable

### Pool Exhaustion
- dnsmasq returns no lease
- VM boots without network (DHCP timeout)
- Clear error in dnsmasq logs

---

## Migration

### Phase 1: Deploy Code
1. Add DHCPServer component
2. Add config fields with new subnet defaults
3. Daemon starts dnsmasq on startup

### Phase 2: Update Host Network
1. Add new bridge IP: `ip addr add 192.168.64.1/18 dev flbr0`
2. Add new NAT rule: `iptables -t nat -A POSTROUTING -s 192.168.64.0/18 ! -o flbr0 -j MASQUERADE`
3. Keep old config temporarily for existing VMs

### Phase 3: Rebuild VM Image
1. Remove static IP from Dockerfile
2. Add systemd-networkd DHCP config
3. Update init script for MMDS route only
4. Rebuild and import new rootfs

### Phase 4: Cleanup
1. Destroy old VMs using static IPs
2. Remove old bridge IP and NAT rule

---

## Testing

### Unit Tests
- Config generation for dnsmasq
- Lease file parsing
- MAC to IP lookup

### Integration Tests
- dnsmasq starts and stops with daemon
- VM gets IP via DHCP
- Multiple VMs get different IPs
- IP lookup by VM ID works
- Lease persists across dnsmasq restart

### Manual Verification
1. Start daemon, verify dnsmasq running
2. Start 2+ VMs
3. Verify each has unique IP: `ip addr show eth0`
4. Verify connectivity: `ping 8.8.8.8`
5. Verify Tailscale works
6. Verify `stockyard vm ip <id>` returns correct IP
7. Destroy VM, start new VM, verify IP reuse works

---

## Dependencies

- `dnsmasq` binary must be installed on host
- Bridge interface must exist before daemon starts

---

## Rollback

If issues arise:
1. Stop daemon (stops dnsmasq)
2. Revert to static IP in Dockerfile
3. Rebuild VM image
4. Remove DHCPServer from daemon startup
5. VMs return to shared IP (with original bug)

---

## Future Considerations

- **IPv6 support**: Add DHCPv6 or SLAAC via dnsmasq
- **Static reservations**: Pre-assign specific IPs to specific VMs via dnsmasq `dhcp-host`
- **Multiple bridges**: Run separate dnsmasq per bridge/tenant

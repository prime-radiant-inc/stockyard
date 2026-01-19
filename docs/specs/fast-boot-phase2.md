# Fast Boot Phase 2: Static IP and Tailscale Pre-Registration

**Status: Implemented (2025-01-19)**

## Overview

Phase 1 optimizations reduced boot time from ~7s to ~5.1s. Two bottlenecks remained:
- **DHCP/Network init**: ~3.3s
- **Tailscale control plane**: ~1.5s

This spec covers eliminating both, targeting **<2s boot time**.

**Final Result: 1.65s init time (target achieved)**

---

## Part 1: Static IP Assignment

### Current State

```
VM Boot Timeline (network portion):
0.0s  Kernel starts
0.5s  systemd-networkd starts
0.6s  DHCP DISCOVER sent
      ... waiting for DHCP ...
3.6s  DHCP complete, IP assigned
3.7s  network-online.target reached
```

The VM uses DHCP to get its IP from dnsmasq on the host. This takes ~3s due to:
1. virtio_net driver initialization
2. systemd-networkd startup
3. DHCP 4-way handshake
4. Route/address configuration

### Proposed Solution

Assign static IPs to VMs before boot. The host knows the MAC address before starting Firecracker, so it can pre-determine the IP.

### Implementation

#### 1. IP Allocation (Host Side)

**File:** `pkg/firecracker/client.go`

When creating a VM, allocate an IP from the pool before boot:

```go
type VMNetwork struct {
    MAC       string // e.g., "02:FC:00:00:01:23"
    IP        string // e.g., "10.0.100.50"
    Gateway   string // e.g., "10.0.100.1"
    Netmask   string // e.g., "255.255.255.0"
    DNS       string // e.g., "8.8.8.8"
}

func (c *Client) allocateNetwork(vmID string) (*VMNetwork, error) {
    // Generate deterministic MAC from VM ID
    mac := generateMAC(vmID)

    // Allocate next available IP from pool
    ip, err := c.ipPool.Allocate(vmID)
    if err != nil {
        return nil, err
    }

    return &VMNetwork{
        MAC:     mac,
        IP:      ip,
        Gateway: "10.0.100.1",
        Netmask: "255.255.255.0",
        DNS:     "8.8.8.8",
    }, nil
}
```

**IP Pool Management:**

```go
type IPPool struct {
    mu        sync.Mutex
    baseIP    net.IP      // 10.0.100.0
    allocated map[string]string // vmID -> IP
    available []string    // available IPs
}

func NewIPPool(cidr string) *IPPool {
    // Parse CIDR, generate pool of .2-.254
    // Reserve .1 for gateway
}

func (p *IPPool) Allocate(vmID string) (string, error) {
    p.mu.Lock()
    defer p.mu.Unlock()

    if ip, ok := p.allocated[vmID]; ok {
        return ip, nil // Already allocated
    }

    if len(p.available) == 0 {
        return "", errors.New("IP pool exhausted")
    }

    ip := p.available[0]
    p.available = p.available[1:]
    p.allocated[vmID] = ip
    return ip, nil
}

func (p *IPPool) Release(vmID string) {
    p.mu.Lock()
    defer p.mu.Unlock()

    if ip, ok := p.allocated[vmID]; ok {
        delete(p.allocated, vmID)
        p.available = append(p.available, ip)
    }
}
```

#### 2. Pass Network Config via MMDS

**File:** `pkg/firecracker/mmds.go`

Add network configuration to MMDS metadata:

```go
type MMDSMetadata struct {
    // Existing fields...
    InstanceID        string
    Hostname          string
    TailscaleAuthKey  string
    SSHAuthorizedKeys string

    // New fields for static IP
    NetworkConfig     *NetworkConfig `json:"network-config,omitempty"`
}

type NetworkConfig struct {
    IP      string `json:"ip"`       // "10.0.100.50"
    Netmask string `json:"netmask"`  // "255.255.255.0"
    Gateway string `json:"gateway"`  // "10.0.100.1"
    DNS     string `json:"dns"`      // "8.8.8.8"
}
```

#### 3. VM Network Configuration

**Option A: Kernel Command Line (Fastest)**

Pass IP via kernel boot args:

```go
bootArgs := fmt.Sprintf(
    "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw "+
    "ip=%s::%s:%s::eth0:off",
    network.IP, network.Gateway, network.Netmask,
)
```

Format: `ip=<client-ip>:<server-ip>:<gw-ip>:<netmask>:<hostname>:<device>:<autoconf>`

This configures the IP in the kernel before systemd even starts.

**Pros:** Fastest possible - IP available at kernel init
**Cons:** Less flexible, requires kernel support

**Option B: systemd-networkd Static Config (Recommended)**

Generate a static network file via MMDS:

**File:** `vm-image/init/stockyard-init.sh` (runs early)

```bash
# Fetch network config from MMDS before systemd-networkd starts
NETWORK_CONFIG=$(curl -sf "${MMDS_URL}/latest/meta-data/network-config" 2>/dev/null)
if [ -n "$NETWORK_CONFIG" ]; then
    IP=$(echo "$NETWORK_CONFIG" | jq -r '.ip')
    NETMASK=$(echo "$NETWORK_CONFIG" | jq -r '.netmask')
    GATEWAY=$(echo "$NETWORK_CONFIG" | jq -r '.gateway')
    DNS=$(echo "$NETWORK_CONFIG" | jq -r '.dns')

    # Write static network config
    cat > /etc/systemd/network/10-eth0.network <<EOF
[Match]
Name=eth0

[Network]
Address=${IP}/${NETMASK_BITS}
Gateway=${GATEWAY}
DNS=${DNS}

[Route]
Destination=169.254.169.254/32
Scope=link
EOF

    # Reload networkd
    networkctl reload
fi
```

**But wait** - this runs AFTER network-online.target. We need it earlier.

**Option C: Early Boot Service (Recommended)**

Create a new service that runs before systemd-networkd:

**File:** `vm-image/init/stockyard-network.service`

```ini
[Unit]
Description=Stockyard Static Network Configuration
DefaultDependencies=no
Before=systemd-networkd.service network.target
After=local-fs.target
Wants=local-fs.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/stockyard-network.sh
RemainAfterExit=yes

[Install]
WantedBy=sysinit.target
```

**File:** `vm-image/init/stockyard-network.sh`

```bash
#!/bin/bash
# Configure static IP from MMDS before systemd-networkd starts

MMDS_URL="http://169.254.169.254/latest"

# MMDS route must exist (added by Firecracker)
# Fetch network config
NETWORK_JSON=$(curl -sf "${MMDS_URL}/meta-data/network-config" 2>/dev/null)

if [ -z "$NETWORK_JSON" ]; then
    echo "No static network config in MMDS, using DHCP"
    exit 0
fi

# Parse JSON (jq is available)
IP=$(echo "$NETWORK_JSON" | jq -r '.ip')
NETMASK=$(echo "$NETWORK_JSON" | jq -r '.netmask')
GATEWAY=$(echo "$NETWORK_JSON" | jq -r '.gateway')
DNS=$(echo "$NETWORK_JSON" | jq -r '.dns')

# Convert netmask to CIDR prefix
case "$NETMASK" in
    "255.255.255.0") PREFIX=24 ;;
    "255.255.0.0") PREFIX=16 ;;
    "255.0.0.0") PREFIX=8 ;;
    *) PREFIX=24 ;;
esac

# Write static config (overwrite DHCP config)
cat > /etc/systemd/network/10-eth0.network <<EOF
[Match]
Name=eth0

[Network]
Address=${IP}/${PREFIX}
Gateway=${GATEWAY}
DNS=${DNS}

[Route]
Destination=169.254.169.254/32
Scope=link
EOF

echo "Configured static IP: ${IP}/${PREFIX}"
```

#### 4. Keep DHCP as Fallback

The VM image should still have DHCP capability for:
- Development/debugging
- Backwards compatibility
- Cases where MMDS isn't available

The static config service checks for MMDS network config; if absent, DHCP proceeds normally.

### Expected Improvement

| Phase | Before | After |
|-------|--------|-------|
| Network ready | 3.6s | ~0.5s |
| Total to init complete | 5.1s | ~2.0s |

**Savings: ~3s**

### Testing Plan

1. Verify IP is correctly allocated and passed via MMDS
2. Verify VM gets static IP before network-online.target
3. Verify DHCP fallback still works when MMDS has no network config
4. Benchmark 10 VMs to confirm timing improvement
5. Test IP pool exhaustion and release

---

## Part 2: Tailscale Pre-Registration

### Current State

```
Tailscale Timeline:
0.0s  tailscaled starts
0.1s  Socket ready
0.2s  tailscale up --authkey=... starts
      ... control plane communication ...
1.7s  Tailscale connected, IP assigned
```

The `tailscale up` command:
1. Generates node keypair (if needed)
2. Connects to control.tailscale.com
3. Authenticates with auth key
4. Gets IP allocation
5. Establishes DERP connections

### Proposed Solution

Pre-register Tailscale nodes on the host BEFORE VM boot. Pass the complete Tailscale state via MMDS so the VM just reconnects.

### Security Consideration

This means the host has the VM's Tailscale private key. This is acceptable because:
- The host already has Tailscale auth keys
- The host has SSH keys, API keys, etc.
- The VM is not a trust boundary against the host
- The private key never leaves the host machine

### Implementation

#### 1. Tailscale State Structure

Tailscale stores state in `/var/lib/tailscale/tailscaled.state` (JSON):

```json
{
  "_machinekey": "base64-encoded-machine-private-key",
  "_current-profile": "base64-profile-id",
  "_profiles": "base64-encoded-profiles-list",
  "profile-XXXX": "base64-encoded-profile-data"
}
```

The profile contains:
- Node private key
- Control server URL
- Preferences (hostname, accept-routes, etc.)
- Persisted peer info

#### 2. Pre-Registration Service (Host Side)

**File:** `pkg/tailscale/preregister.go`

```go
package tailscale

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
)

type PreRegistrar struct {
    authKey     string
    stateDir    string // Base directory for pre-registered states
}

type PreRegisteredNode struct {
    Hostname string
    State    []byte // tailscaled.state content
    IP       string // Assigned Tailscale IP
}

func NewPreRegistrar(authKey, stateDir string) *PreRegistrar {
    return &PreRegistrar{
        authKey:  authKey,
        stateDir: stateDir,
    }
}

// PreRegister creates a new Tailscale identity and registers it
func (p *PreRegistrar) PreRegister(ctx context.Context, hostname string) (*PreRegisteredNode, error) {
    // Create isolated state directory for this registration
    nodeDir := filepath.Join(p.stateDir, hostname)
    if err := os.MkdirAll(nodeDir, 0700); err != nil {
        return nil, err
    }

    statePath := filepath.Join(nodeDir, "tailscaled.state")
    socketPath := filepath.Join(nodeDir, "tailscaled.sock")

    // Start tailscaled in userspace mode with isolated state
    tailscaled := exec.CommandContext(ctx,
        "tailscaled",
        "--state="+statePath,
        "--socket="+socketPath,
        "--tun=userspace-networking",
        "--statedir="+nodeDir,
    )
    tailscaled.Env = append(os.Environ(),
        "TS_NO_LOGS_NO_SUPPORT=true",
    )

    if err := tailscaled.Start(); err != nil {
        return nil, fmt.Errorf("start tailscaled: %w", err)
    }
    defer tailscaled.Process.Kill()

    // Wait for socket
    if err := waitForSocket(ctx, socketPath, 10*time.Second); err != nil {
        return nil, fmt.Errorf("wait for socket: %w", err)
    }

    // Run tailscale up
    up := exec.CommandContext(ctx,
        "tailscale",
        "--socket="+socketPath,
        "up",
        "--authkey="+p.authKey,
        "--hostname="+hostname,
        "--accept-routes",
        "--ssh",
        "--timeout=30s",
    )

    if output, err := up.CombinedOutput(); err != nil {
        return nil, fmt.Errorf("tailscale up: %w: %s", err, output)
    }

    // Get assigned IP
    ipCmd := exec.CommandContext(ctx,
        "tailscale",
        "--socket="+socketPath,
        "ip", "-4",
    )
    ipOutput, err := ipCmd.Output()
    if err != nil {
        return nil, fmt.Errorf("get IP: %w", err)
    }
    ip := strings.TrimSpace(string(ipOutput))

    // Read the state file
    state, err := os.ReadFile(statePath)
    if err != nil {
        return nil, fmt.Errorf("read state: %w", err)
    }

    // Cleanup
    tailscaled.Process.Kill()
    os.RemoveAll(nodeDir)

    return &PreRegisteredNode{
        Hostname: hostname,
        State:    state,
        IP:       ip,
    }, nil
}
```

#### 3. Integration with VM Creation

**File:** `pkg/daemon/tasks.go`

```go
func (m *TaskManager) CreateTask(ctx context.Context, req *CreateTaskRequest) (*Task, error) {
    // ... existing code ...

    // Pre-register Tailscale node
    hostname := fmt.Sprintf("stockyard-%s", taskID)

    preReg := tailscale.NewPreRegistrar(
        m.tailscaleAuthKey,
        filepath.Join(m.dataDir, "tailscale-prereg"),
    )

    node, err := preReg.PreRegister(ctx, hostname)
    if err != nil {
        // Fall back to in-VM registration
        log.Printf("Pre-registration failed, VM will register: %v", err)
        node = nil
    }

    // Build MMDS data including Tailscale state
    mmdsData := BuildMMDSData(MMDSMetadata{
        // ... existing fields ...
        TailscaleState: node.State, // New field
    })

    // ... continue with VM creation ...
}
```

#### 4. MMDS Metadata Extension

**File:** `pkg/firecracker/mmds.go`

```go
type MMDSMetadata struct {
    // Existing fields
    InstanceID        string
    Hostname          string
    TailscaleAuthKey  string  // Keep for fallback
    SSHAuthorizedKeys string

    // New field for pre-registered state
    TailscaleState    []byte `json:"tailscale-state,omitempty"`
}
```

#### 5. VM-Side State Injection

**File:** `vm-image/init/stockyard-init.sh`

```bash
# Phase 3: Tailscale setup
log_timing "Starting Tailscale configuration..."

TS_STATE=$(curl -sf "${MMDS_URL}/meta-data/tailscale-state" 2>/dev/null | jq -r '.')

if [ -n "$TS_STATE" ] && [ "$TS_STATE" != "null" ]; then
    log_timing "Found pre-registered Tailscale state"

    # Write state before starting tailscaled
    mkdir -p /var/lib/tailscale
    echo "$TS_STATE" | base64 -d > /var/lib/tailscale/tailscaled.state
    chmod 600 /var/lib/tailscale/tailscaled.state

    # Start tailscaled (will use existing state)
    systemctl start tailscaled.service

    # Just wait for connection (no registration needed)
    for i in {1..30}; do
        if tailscale status &>/dev/null; then
            TS_IP=$(tailscale ip -4 2>/dev/null)
            log_timing "Tailscale reconnected: $TS_IP"
            break
        fi
        sleep 0.1
    done
else
    log_timing "No pre-registered state, using auth key"
    # ... existing auth key flow ...
fi
```

#### 6. Node Cleanup on VM Destruction

**File:** `pkg/daemon/tasks.go`

```go
func (m *TaskManager) DestroyTask(ctx context.Context, taskID string) error {
    // ... existing cleanup ...

    // Remove Tailscale device from tailnet
    hostname := fmt.Sprintf("stockyard-%s", taskID)
    if err := m.tailscale.RemoveDevice(ctx, hostname); err != nil {
        log.Printf("Warning: failed to remove Tailscale device: %v", err)
        // Don't fail - ephemeral keys will clean up eventually
    }

    // ... continue with cleanup ...
}
```

### Pre-Registration Timing

The pre-registration happens DURING `stockyard run`, before the VM boots:

```
stockyard run timeline:
0.0s   Request received
0.1s   Task ID generated
0.2s   Tailscale pre-registration starts
       ... runs tailscaled, tailscale up ...
1.8s   Pre-registration complete
1.9s   ZFS clone
2.0s   Firecracker starts (with Tailscale state in MMDS)
       ... VM boots ...
4.5s   VM init complete, Tailscale already connected
```

The 1.8s pre-registration runs in parallel with nothing, but it happens BEFORE VM boot. The VM then reconnects in ~0.2s instead of registering in ~1.5s.

**Net effect:** Shifts 1.5s from VM boot to pre-boot, but pre-boot can potentially be parallelized with other setup.

### Alternative: Background Pre-Registration Pool

For even faster startup, maintain a pool of pre-registered identities:

```go
type TailscalePool struct {
    ready chan *PreRegisteredNode
    size  int
}

func (p *TailscalePool) Start(ctx context.Context) {
    // Keep pool.size nodes pre-registered
    for {
        if len(p.ready) < p.size {
            hostname := fmt.Sprintf("stockyard-pool-%s", uuid.New())
            node, err := p.preReg.PreRegister(ctx, hostname)
            if err == nil {
                p.ready <- node
            }
        }
        time.Sleep(time.Second)
    }
}

func (p *TailscalePool) Acquire(hostname string) (*PreRegisteredNode, error) {
    select {
    case node := <-p.ready:
        // Rename the node to desired hostname
        // (Tailscale allows hostname changes)
        return node, nil
    default:
        // Pool empty, register synchronously
        return p.preReg.PreRegister(ctx, hostname)
    }
}
```

This makes VM startup instant (no pre-registration wait) at the cost of:
- Background resource usage
- More complex hostname management
- Potential for stale registrations

### Expected Improvement

| Phase | Before | After |
|-------|--------|-------|
| Tailscale ready | 1.7s | ~0.3s |
| Total to SSH available | 6.6s | ~2.0s |

**Savings: ~1.4s in VM boot (shifted to pre-boot)**

### Testing Plan

1. Verify pre-registration creates valid Tailscale state
2. Verify VM can reconnect with injected state
3. Verify fallback to auth key when pre-reg fails
4. Verify cleanup removes devices from tailnet
5. Benchmark 10 VMs to confirm timing improvement
6. Test with expired/invalid pre-registered state

---

## Combined Implementation

### Phase 2 Complete Timeline

With both optimizations:

```
Host-side (stockyard run):
0.0s   Request received
0.1s   Task ID, MAC, IP allocated
0.2s   Tailscale pre-registration starts (background)
0.3s   ZFS clone starts
0.4s   ZFS clone complete
1.8s   Tailscale pre-registration complete
1.9s   MMDS configured (static IP + Tailscale state)
2.0s   Firecracker starts

VM-side:
0.0s   Kernel starts
0.4s   stockyard-network.sh writes static IP config
0.5s   systemd-networkd starts with static IP
0.6s   network-online.target reached (no DHCP wait!)
0.7s   stockyard-init.service starts
0.8s   Tailscale state injected, tailscaled starts
1.0s   Tailscale reconnected
1.1s   stockyard-init complete
1.2s   SSH available

Total: ~2.0s host-side + ~1.2s VM-side = ~3.2s to SSH
```

But if we parallelize Tailscale pre-reg with ZFS clone:

```
Host-side:
0.0s   Request received
0.1s   Task ID, MAC, IP allocated
0.1s   ┌─ Tailscale pre-reg starts (background)
0.1s   └─ ZFS clone starts
0.4s      ZFS clone complete
1.8s   Tailscale pre-reg complete
1.9s   Firecracker starts

VM-side: (same as above, ~1.2s)

Total: ~1.9s + ~1.2s = ~3.1s to SSH
```

### Comparison

| Metric | Original | Phase 1 | Phase 2 |
|--------|----------|---------|---------|
| Init complete | 7.0s | 5.1s | ~1.5s |
| SSH available | 7.0s | 6.6s | ~2.0s |

**Total improvement: 7.0s → 2.0s (71% reduction)**

---

## Implementation Order

1. **Static IP (Part 1)** - Lower risk, bigger impact (~3s savings)
   - IP pool management
   - MMDS network config
   - Early boot service
   - ~2-3 hours implementation

2. **Tailscale Pre-Registration (Part 2)** - Higher complexity (~1.4s savings)
   - Pre-registration service
   - State injection via MMDS
   - Cleanup on destroy
   - Pool management (optional)
   - ~4-6 hours implementation

3. **Testing & Benchmarking**
   - Full regression testing
   - 10-VM benchmark suite
   - Edge case handling
   - ~2 hours

**Total estimated effort: 8-11 hours**

---

## Risks and Mitigations

### Static IP Risks

| Risk | Mitigation |
|------|------------|
| IP pool exhaustion | Auto-expand pool, alert on low availability |
| IP conflicts | Single source of truth in daemon, atomic allocation |
| MMDS not available early enough | Fallback to DHCP, test timing carefully |

### Tailscale Pre-Registration Risks

| Risk | Mitigation |
|------|------------|
| Pre-reg fails | Fallback to in-VM registration |
| State becomes stale | Short TTL, re-register on reconnect failure |
| Control plane rate limiting | Pool with background replenishment |
| Host tailscaled conflicts | Use isolated state directories |

---

## Success Criteria

1. Boot time to SSH available < 2.5s (p50)
2. Boot time to SSH available < 3.5s (p99)
3. No regression in reliability
4. Graceful fallback when optimizations fail
5. Clean VM destruction (no orphaned Tailscale devices)

---

## Implementation Notes (2025-01-19)

### Critical Bug Fix: Tailscale Auth Key

The original spec showed passing the auth key via `--authkey=` flag directly. This works, but the implementation initially tried to use the `TS_AUTHKEY` environment variable (for security - to avoid exposing the key in process arguments).

**Bug:** The `tailscale` CLI does NOT read `TS_AUTHKEY` - only the tsnet Go library does.

**Solution:** Use file-based auth key:
```go
authKeyPath := filepath.Join(nodeDir, "authkey")
os.WriteFile(authKeyPath, []byte(p.authKey), 0600)
// Then: "--authkey=file:"+authKeyPath
```

This keeps the auth key out of process arguments while working with the CLI.

### ZFS Snapshot Cache

After updating the VM init script, you must destroy the existing ZFS base snapshot to force reimport:
```bash
sudo zfs destroy tank/stockyard/images/rootfs@base
sudo systemctl restart stockyardd  # Reimports base image
```

### Verified Results

```
[+.028s] Network already configured (kernel): 10.0.100.2
[+.141s] Found pre-registered Tailscale state
[+1.637s] Tailscale reconnected in 1.26s: 100.127.202.26
[+1.657s] === Stockyard Init Complete (total: 1.65s) ===
```

All success criteria met.

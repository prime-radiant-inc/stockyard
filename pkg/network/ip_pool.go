package network

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// IPPool manages a pool of IP addresses for VM allocation.
type IPPool struct {
	mu          sync.Mutex
	network     *net.IPNet
	gateway     string
	allocated   map[string]string // vmID -> IP
	available   []string          // available IPs
	persistPath string            // path to persist state (optional)
}

// persistedState is the JSON format for saving pool state to disk.
type persistedState struct {
	Allocated map[string]string `json:"allocated"`
}

// NewIPPool creates an IP pool from a CIDR and gateway.
// The gateway IP is excluded from the pool.
func NewIPPool(cidr, gateway string) (*IPPool, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR: %w", err)
	}

	pool := &IPPool{
		network:   ipNet,
		gateway:   gateway,
		allocated: make(map[string]string),
		available: make([]string, 0),
	}

	// Generate available IPs (skip network address, gateway, and broadcast)
	pool.generateAvailableIPs()

	return pool, nil
}

// NewIPPoolFromGateway creates an IP pool from a gateway IP and prefix length.
// This is more robust than parsing CIDR from config strings.
func NewIPPoolFromGateway(gateway string, prefixLen int) (*IPPool, error) {
	gwIP := net.ParseIP(gateway)
	if gwIP == nil {
		return nil, fmt.Errorf("invalid gateway IP: %s", gateway)
	}
	gwIP = gwIP.To4()
	if gwIP == nil {
		return nil, fmt.Errorf("gateway must be IPv4: %s", gateway)
	}

	// Calculate network address from gateway
	mask := net.CIDRMask(prefixLen, 32)
	networkIP := gwIP.Mask(mask)

	ipNet := &net.IPNet{
		IP:   networkIP,
		Mask: mask,
	}

	pool := &IPPool{
		network:   ipNet,
		gateway:   gateway,
		allocated: make(map[string]string),
		available: make([]string, 0),
	}

	pool.generateAvailableIPs()
	return pool, nil
}

// generateAvailableIPs populates the available IP list.
func (p *IPPool) generateAvailableIPs() {
	ip := make(net.IP, 4)
	copy(ip, p.network.IP.To4())

	ones, bits := p.network.Mask.Size()
	hostBits := bits - ones
	maxHosts := (1 << hostBits) - 1

	for {
		// Increment IP
		for i := 3; i >= 0; i-- {
			ip[i]++
			if ip[i] != 0 {
				break
			}
		}

		// Check if still in network
		if !p.network.Contains(ip) {
			break
		}

		// Skip broadcast (last IP in range)
		ipInt := binary.BigEndian.Uint32(ip)
		baseInt := binary.BigEndian.Uint32(p.network.IP.To4())
		if ipInt-baseInt >= uint32(maxHosts) {
			break
		}

		ipStr := ip.String()
		if ipStr != p.gateway {
			p.available = append(p.available, ipStr)
		}
	}
}

// Available returns the number of available IPs.
func (p *IPPool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.available)
}

// Gateway returns the gateway IP.
func (p *IPPool) Gateway() string {
	return p.gateway
}

// Netmask returns the netmask in dotted-decimal notation.
func (p *IPPool) Netmask() string {
	mask := p.network.Mask
	if len(mask) == 4 {
		return fmt.Sprintf("%d.%d.%d.%d", mask[0], mask[1], mask[2], mask[3])
	}
	return mask.String()
}

// PrefixLen returns the CIDR prefix length.
func (p *IPPool) PrefixLen() int {
	ones, _ := p.network.Mask.Size()
	return ones
}

// SetPersistPath sets the file path for persisting allocation state.
func (p *IPPool) SetPersistPath(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.persistPath = path
}

// Allocate assigns an IP address to a VM. If the VM already has an
// allocation, returns the same IP (idempotent). Returns error if pool
// is exhausted.
func (p *IPPool) Allocate(vmID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check for existing allocation
	if ip, ok := p.allocated[vmID]; ok {
		return ip, nil
	}

	// Allocate new IP from available pool
	if len(p.available) == 0 {
		return "", fmt.Errorf("IP pool exhausted")
	}

	ip := p.available[0]
	p.available = p.available[1:]
	p.allocated[vmID] = ip

	p.persistLocked()
	return ip, nil
}

// Release returns a VM's IP address to the pool.
func (p *IPPool) Release(vmID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ip, ok := p.allocated[vmID]
	if !ok {
		return
	}

	delete(p.allocated, vmID)
	p.available = append(p.available, ip)

	p.persistLocked()
}

// GetAllocation returns the IP allocated to a VM, or empty string if none.
func (p *IPPool) GetAllocation(vmID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.allocated[vmID]
}

// persistLocked saves allocation state to disk. Caller must hold the lock.
func (p *IPPool) persistLocked() {
	if p.persistPath == "" {
		return
	}

	state := persistedState{
		Allocated: p.allocated,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return // silently fail - persistence is best-effort
	}

	// Ensure directory exists
	dir := filepath.Dir(p.persistPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	// Write atomically via temp file
	tmpPath := p.persistPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	os.Rename(tmpPath, p.persistPath)
}

// LoadState restores allocation state from disk.
func (p *IPPool) LoadState() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.persistPath == "" {
		return nil
	}

	data, err := os.ReadFile(p.persistPath)
	if os.IsNotExist(err) {
		return nil // no state to restore
	}
	if err != nil {
		return fmt.Errorf("reading state file: %w", err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parsing state file: %w", err)
	}

	// Restore allocations - remove allocated IPs from available pool
	for vmID, ip := range state.Allocated {
		p.allocated[vmID] = ip
		// Remove from available
		for i, avail := range p.available {
			if avail == ip {
				p.available = append(p.available[:i], p.available[i+1:]...)
				break
			}
		}
	}

	return nil
}

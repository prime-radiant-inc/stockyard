package network

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// IPPool manages a pool of IP addresses for VM allocation.
type IPPool struct {
	mu        sync.Mutex
	network   *net.IPNet
	gateway   string
	allocated map[string]string // vmID -> IP
	available []string          // available IPs
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

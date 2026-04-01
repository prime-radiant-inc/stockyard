package vmbackend

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// FindIPByMAC parses a macOS /var/db/dhcpd_leases file and returns the IP
// address associated with the given MAC address. MAC comparison is case-insensitive.
func FindIPByMAC(leasePath, mac string) (string, error) {
	f, err := os.Open(leasePath)
	if err != nil {
		return "", fmt.Errorf("open lease file: %w", err)
	}
	defer f.Close()

	searchMAC := strings.ToLower(mac)

	var currentIP string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "ip_address=") {
			currentIP = strings.TrimPrefix(line, "ip_address=")
		}

		if strings.HasPrefix(line, "hw_address=") {
			hwAddr := strings.TrimPrefix(line, "hw_address=")
			if idx := strings.Index(hwAddr, ","); idx >= 0 {
				hwAddr = hwAddr[idx+1:]
			}
			if strings.ToLower(hwAddr) == searchMAC && currentIP != "" {
				return currentIP, nil
			}
		}

		if line == "}" {
			currentIP = ""
		}
	}

	return "", fmt.Errorf("no lease found for MAC %s", mac)
}

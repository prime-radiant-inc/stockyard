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
	return findIPInLeases(leasePath, "hw_address", mac)
}

// FindIPByName parses a macOS /var/db/dhcpd_leases file and returns the IP
// address associated with the given hostname.
func FindIPByName(leasePath, name string) (string, error) {
	return findIPInLeases(leasePath, "name", name)
}

func findIPInLeases(leasePath, matchField, matchValue string) (string, error) {
	f, err := os.Open(leasePath)
	if err != nil {
		return "", fmt.Errorf("open lease file: %w", err)
	}
	defer f.Close()

	searchValue := strings.ToLower(matchValue)

	var currentIP string
	var matched bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "ip_address=") {
			currentIP = strings.TrimPrefix(line, "ip_address=")
		}

		if strings.HasPrefix(line, matchField+"=") {
			value := strings.TrimPrefix(line, matchField+"=")
			if matchField == "hw_address" {
				if idx := strings.Index(value, ","); idx >= 0 {
					value = value[idx+1:]
				}
			}
			if strings.ToLower(value) == searchValue {
				matched = true
			}
		}

		if line == "}" {
			if matched && currentIP != "" {
				return currentIP, nil
			}
			currentIP = ""
			matched = false
		}
	}

	return "", fmt.Errorf("no lease found for %s=%s", matchField, matchValue)
}

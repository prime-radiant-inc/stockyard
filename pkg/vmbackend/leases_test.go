package vmbackend

import (
	"os"
	"path/filepath"
	"testing"
)

const testLeaseData = `{
	name=vm-one
	ip_address=192.168.64.2
	hw_address=1,02:aa:bb:cc:dd:01
	identifier=1,02:aa:bb:cc:dd:01
	lease=0x67890001
}
{
	name=vm-two
	ip_address=192.168.64.3
	hw_address=1,02:aa:bb:cc:dd:02
	identifier=1,02:aa:bb:cc:dd:02
	lease=0x67890002
}
`

func TestParseLeaseFile_FindByMAC(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	ip, err := FindIPByMAC(leasePath, "02:aa:bb:cc:dd:02")
	if err != nil {
		t.Fatalf("FindIPByMAC failed: %v", err)
	}
	if ip != "192.168.64.3" {
		t.Errorf("expected 192.168.64.3, got %s", ip)
	}
}

func TestParseLeaseFile_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	_, err := FindIPByMAC(leasePath, "02:ff:ff:ff:ff:ff")
	if err == nil {
		t.Fatal("expected error for unknown MAC")
	}
}

func TestParseLeaseFile_CaseInsensitive(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	ip, err := FindIPByMAC(leasePath, "02:AA:BB:CC:DD:01")
	if err != nil {
		t.Fatalf("FindIPByMAC failed: %v", err)
	}
	if ip != "192.168.64.2" {
		t.Errorf("expected 192.168.64.2, got %s", ip)
	}
}

func TestParseLeaseFile_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(""), 0644)

	_, err := FindIPByMAC(leasePath, "02:aa:bb:cc:dd:01")
	if err == nil {
		t.Fatal("expected error for empty file")
	}
}

func TestFindIPByName(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	ip, err := FindIPByName(leasePath, "vm-two")
	if err != nil {
		t.Fatalf("FindIPByName failed: %v", err)
	}
	if ip != "192.168.64.3" {
		t.Errorf("expected 192.168.64.3, got %s", ip)
	}
}

func TestFindIPByName_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	leasePath := filepath.Join(tmpDir, "dhcpd_leases")
	os.WriteFile(leasePath, []byte(testLeaseData), 0644)

	_, err := FindIPByName(leasePath, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown name")
	}
}

func TestParseLeaseFile_MissingFile(t *testing.T) {
	_, err := FindIPByMAC("/nonexistent/path", "02:aa:bb:cc:dd:01")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

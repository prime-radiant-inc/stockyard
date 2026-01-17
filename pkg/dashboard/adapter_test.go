package dashboard

import (
	"testing"
)

func TestDaemonAdapter_ImplementsInterface(t *testing.T) {
	// This is a compile-time check
	var _ DaemonAPI = (*DaemonAdapter)(nil)
}

//go:build !darwin

package daemon

import (
	"fmt"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createVfkitBackend(cfg *config.Config) (vmbackend.Backend, error) {
	return nil, fmt.Errorf("vfkit backend is only available on macOS")
}

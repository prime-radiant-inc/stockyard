//go:build !darwin

package daemon

import (
	"fmt"

	"github.com/obra/stockyard/pkg/config"
	"github.com/obra/stockyard/pkg/vmbackend"
)

func createAppleContainerBackend(cfg *config.Config) (vmbackend.Backend, error) {
	return nil, fmt.Errorf("apple-container backend is only available on macOS")
}

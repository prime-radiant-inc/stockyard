//go:build !linux

package firecracker

import (
	"context"
	"errors"
)

var errStableProcessHandlesUnsupported = errors.New("stable process handles are unsupported on this platform")

type unsupportedStableProcessProvider struct{}

func newStableProcessProvider() stableProcessProvider {
	return unsupportedStableProcessProvider{}
}

func (unsupportedStableProcessProvider) candidatePIDs(context.Context, string, string) ([]int, error) {
	return nil, errStableProcessHandlesUnsupported
}

func (unsupportedStableProcessProvider) open(int) (stableProcessHandle, error) {
	return nil, errStableProcessHandlesUnsupported
}

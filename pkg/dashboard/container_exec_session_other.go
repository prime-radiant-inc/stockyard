//go:build !darwin

package dashboard

import "fmt"

// ContainerExecSession is unavailable off macOS; the apple-container backend
// only runs on macOS, so this is never constructed there.
type ContainerExecSession struct {
	ID     string
	TaskID string
	User   string
}

func newContainerExecSession(containerBin, vmID, shell string, cols, rows int) (*ContainerExecSession, error) {
	return nil, fmt.Errorf("apple-container terminal is only available on macOS")
}

func (s *ContainerExecSession) Read(p []byte) (int, error)  { return 0, fmt.Errorf("unsupported") }
func (s *ContainerExecSession) Write(p []byte) (int, error) { return 0, fmt.Errorf("unsupported") }
func (s *ContainerExecSession) Resize(cols, rows int) error { return fmt.Errorf("unsupported") }
func (s *ContainerExecSession) Close() error                { return nil }

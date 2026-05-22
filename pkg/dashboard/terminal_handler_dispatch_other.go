//go:build !darwin

package dashboard

import "net/http"

// serveContainerExec is a stub off macOS — apple-container is macOS-only.
func (h *TerminalHandler) serveContainerExec(w http.ResponseWriter, r *http.Request, task *Task, user string, cols, rows int) {
	http.Error(w, "apple-container terminal is only available on macOS", http.StatusServiceUnavailable)
}

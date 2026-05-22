//go:build darwin

package dashboard

import (
	"log"
	"net/http"

	"github.com/google/uuid"
)

// serveContainerExec upgrades the websocket and bridges it to a
// `container exec` PTY session for an apple-container task.
func (h *TerminalHandler) serveContainerExec(w http.ResponseWriter, r *http.Request, task *Task, user string, cols, rows int) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("terminal: websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	session, err := newContainerExecSession(h.containerBin, task.VMID, user, "/bin/bash", cols, rows)
	if err != nil {
		h.sendError(conn, "Failed to start container exec: "+err.Error())
		return
	}
	session.ID = uuid.New().String()
	session.TaskID = task.ID
	session.User = user
	defer session.Close()

	log.Printf("terminal: container exec session started for task %s (%dx%d)", task.ID, cols, rows)
	h.bridgeContainerSession(conn, session)
	log.Printf("terminal: container exec session ended for task %s", task.ID)
}

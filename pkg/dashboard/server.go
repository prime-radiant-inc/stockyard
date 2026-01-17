package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// Server is the HTTP server for the web dashboard.
type Server struct {
	mux       *http.ServeMux
	daemon    DaemonAPI
	templates *template.Template
}

// NewServer creates a new dashboard HTTP server.
// The daemon parameter will be used for API calls (nil allowed for testing).
func NewServer(daemon DaemonAPI) *Server {
	s := &Server{
		mux:    http.NewServeMux(),
		daemon: daemon,
	}
	// Load templates, but don't fail if they're not available (for testing)
	s.templates, _ = LoadTemplates()
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/vm/", s.handleVMDetail)
	s.mux.HandleFunc("/", s.handleFleet)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	// Only handle exact "/" path
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Get tasks from daemon
	var tasks []Task
	if s.daemon != nil {
		var err error
		tasks, err = s.daemon.ListTasks(context.Background())
		if err != nil {
			http.Error(w, "Failed to list tasks", http.StatusInternalServerError)
			return
		}
	}

	// Group tasks by RepoURL
	grouped := make(map[string][]Task)
	for _, task := range tasks {
		grouped[task.RepoURL] = append(grouped[task.RepoURL], task)
	}

	// If templates are not available or fleet.html doesn't exist (testing), output plain text
	if s.templates == nil || s.templates.Lookup("fleet.html") == nil {
		w.Header().Set("Content-Type", "text/plain")
		for _, task := range tasks {
			fmt.Fprintf(w, "%s %s\n", task.ID, task.Status)
		}
		return
	}

	// Render template
	data := map[string]interface{}{
		"Title":        "Fleet",
		"User":         "user", // TODO: get from auth
		"ActiveNav":    "fleet",
		"Tasks":        tasks,
		"GroupedTasks": grouped,
	}
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "fleet.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	buf.WriteTo(w)
}

func (s *Server) handleVMDetail(w http.ResponseWriter, r *http.Request) {
	// Extract VM ID from path: /vm/{id}
	id := strings.TrimPrefix(r.URL.Path, "/vm/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	task, err := s.daemon.GetTask(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.NotFound(w, r)
		return
	}

	snapshots, _ := s.daemon.ListSnapshots(ctx, id)

	data := map[string]interface{}{
		"Title":     task.ID,
		"User":      "user", // TODO: get from auth
		"ActiveNav": "fleet",
		"Task":      task,
		"Snapshots": snapshots,
	}

	// Check if template exists, fallback for testing
	if s.templates == nil || s.templates.Lookup("vm_detail.html") == nil {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(task.ID + " " + task.Status))
		return
	}

	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "vm_detail.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	buf.WriteTo(w)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

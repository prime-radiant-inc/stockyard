package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
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
	w.Header().Set("Content-Type", "text/html")
	if err := s.templates.ExecuteTemplate(w, "fleet.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
)

// Server is the HTTP server for the web dashboard.
type Server struct {
	mux        *http.ServeMux
	daemon     DaemonAPI
	templates  *template.Template
	hub        *Hub
	logHistory *LogHistory
}

// NewServer creates a new dashboard HTTP server.
// The daemon parameter will be used for API calls (nil allowed for testing).
func NewServer(daemon DaemonAPI) *Server {
	hub := NewHub()
	go hub.Run()

	s := &Server{
		mux:        http.NewServeMux(),
		daemon:     daemon,
		hub:        hub,
		logHistory: NewLogHistory(10000),
	}
	// Load templates, but don't fail if they're not available (for testing)
	s.templates, _ = LoadTemplates()
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/ws", s.handleWebSocket)
	s.mux.HandleFunc("/api/vm-logs/", s.handleLogSearch)
	s.mux.HandleFunc("/api/vm/", s.handleAPIVM)
	s.mux.HandleFunc("/preview/vm/", s.handleVMPreview)
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
	groupedByRepo := make(map[string][]Task)
	for _, task := range tasks {
		repo := task.RepoURL
		if repo == "" {
			repo = "(none)"
		}
		groupedByRepo[repo] = append(groupedByRepo[repo], task)
	}

	// Group tasks by Owner (placeholder - Owner field not yet in Task struct)
	groupedByOwner := make(map[string][]Task)
	for _, task := range tasks {
		owner := "(unknown)" // TODO: use task.Owner when available
		groupedByOwner[owner] = append(groupedByOwner[owner], task)
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
		"Title":          "Fleet",
		"User":           GetUser(r.Context()),
		"ActiveNav":      "fleet",
		"Tasks":          tasks,
		"GroupedTasks":   groupedByRepo, // Keep for backward compatibility
		"GroupedByRepo":  groupedByRepo,
		"GroupedByOwner": groupedByOwner,
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

	if s.daemon == nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	task, err := s.daemon.GetTask(ctx, id)
	if err != nil {
		http.Error(w, "Failed to get task", http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.NotFound(w, r)
		return
	}

	snapshots, _ := s.daemon.ListSnapshots(ctx, id)

	data := map[string]interface{}{
		"Title":     task.ID,
		"User":      GetUser(r.Context()),
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

func (s *Server) handleVMPreview(w http.ResponseWriter, r *http.Request) {
	// Extract VM ID from path: /preview/vm/{id}
	id := strings.TrimPrefix(r.URL.Path, "/preview/vm/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	if s.daemon == nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	task, err := s.daemon.GetTask(ctx, id)
	if err != nil {
		http.Error(w, "Failed to get task", http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.NotFound(w, r)
		return
	}

	snapshots, _ := s.daemon.ListSnapshots(ctx, id)

	data := map[string]interface{}{
		"Task":      task,
		"Snapshots": snapshots,
	}

	// Check if template exists, fallback for testing
	if s.templates == nil || s.templates.Lookup("vm_preview.html") == nil {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(task.ID + " " + task.Status))
		return
	}

	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "vm_preview.html", data); err != nil {
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

func (s *Server) handleAPIVM(w http.ResponseWriter, r *http.Request) {
	if s.daemon == nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Parse path: /api/vm/{id} or /api/vm/{id}/action
	path := strings.TrimPrefix(r.URL.Path, "/api/vm/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	ctx := r.Context()

	switch {
	case r.Method == "POST" && action == "stop":
		if err := s.daemon.StopTask(ctx, id); err != nil {
			http.Error(w, "Failed to stop task", http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)

	case r.Method == "DELETE" && action == "":
		if err := s.daemon.DestroyTask(ctx, id); err != nil {
			http.Error(w, "Failed to destroy task", http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusOK)

	case r.Method == "POST" && action == "snapshots":
		label := r.FormValue("label")
		if _, err := s.daemon.CreateSnapshot(ctx, id, label); err != nil {
			http.Error(w, "Failed to create snapshot", http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusOK)

	case r.Method == "POST" && len(parts) >= 3 && parts[1] == "snapshots" && parts[len(parts)-1] == "restore":
		// /api/vm/{id}/snapshots/{name}/restore
		snapshotName := parts[2]
		if err := s.daemon.RestoreSnapshot(ctx, id, snapshotName); err != nil {
			http.Error(w, "Failed to restore snapshot: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusOK)

	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	handler := NewWebSocketHandler(s.hub)
	handler.ServeHTTP(w, r)
}

// Hub returns the WebSocket hub for broadcasting messages.
func (s *Server) Hub() *Hub {
	return s.hub
}

// Close shuts down the server and its resources.
func (s *Server) Close() {
	if s.hub != nil {
		s.hub.Stop()
	}
}

// LogHistory returns the log history store for adding/searching logs.
func (s *Server) LogHistory() *LogHistory {
	return s.logHistory
}

func (s *Server) handleLogSearch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/vm-logs/")
	if id == "" {
		http.Error(w, "missing task ID", http.StatusBadRequest)
		return
	}

	query := r.URL.Query().Get("q")
	stream := r.URL.Query().Get("stream")

	var lines []LogLine
	if stream != "" && stream != "all" {
		lines = s.logHistory.SearchStream(id, stream, query)
	} else {
		lines = s.logHistory.Search(id, query)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lines)
}

package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"
)

// Server is the HTTP server for the web dashboard.
type Server struct {
	mux             *http.ServeMux
	daemon          DaemonAPI
	templates       *template.Template
	hub             *Hub
	logHistory      *LogHistory
	activityFeed    *ActivityFeed
	alertChecker    *AlertChecker
	terminalManager *TerminalManager
	terminalHandler *TerminalHandler
	vmUser          string
}

// NewServer creates a new dashboard HTTP server.
// The daemon parameter will be used for API calls (nil allowed for testing).
// vmUser is the SSH username for terminal connections (defaults to "mooby" if empty).
func NewServer(daemon DaemonAPI, vmUser string) *Server {
	hub := NewHub()
	go hub.Run()

	if vmUser == "" {
		vmUser = "mooby"
	}

	terminalManager := NewTerminalManager()
	s := &Server{
		mux:             http.NewServeMux(),
		daemon:          daemon,
		hub:             hub,
		logHistory:      NewLogHistory(10000),
		activityFeed:    NewActivityFeedWithHub(100, hub),
		alertChecker:    NewAlertChecker(),
		terminalManager: terminalManager,
		terminalHandler: NewTerminalHandler(terminalManager, daemon, vmUser),
		vmUser:          vmUser,
	}
	// Load templates, but don't fail if they're not available (for testing)
	s.templates, _ = LoadTemplates()
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/ws", s.handleWebSocket)
	s.mux.HandleFunc("/ws/terminal/", s.terminalHandler.ServeHTTP)
	s.mux.HandleFunc("/activity", s.handleActivity)
	s.mux.HandleFunc("/settings", s.handleSettings)
	s.mux.HandleFunc("/resources", s.handleResources)
	s.mux.HandleFunc("/api/vm-logs/", s.handleLogSearch)
	s.mux.HandleFunc("/api/vm/create", s.handleAPIVMCreate)
	s.mux.HandleFunc("/api/vm/", s.handleAPIVM)
	s.mux.HandleFunc("/preview/vm/", s.handleVMPreview)
	s.mux.HandleFunc("/vm/", s.handleVMDetail)
	s.mux.Handle("/static/", StaticFileHandler())
	s.mux.HandleFunc("/", s.handleFleet)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	// Only handle exact "/" path
	if r.URL.Path != "/" {
		s.renderError(w, r, http.StatusNotFound, "Page Not Found", "The page you're looking for doesn't exist.")
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

	// Group tasks by Owner
	groupedByOwner := make(map[string][]Task)
	for _, task := range tasks {
		owner := task.Owner
		if owner == "" {
			owner = "(unknown)"
		}
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
		"UserAvatar":     GetUserAvatar(r.Context()),
		"ActiveNav":      "fleet",
		"Tasks":          tasks,
		"GroupedTasks":   groupedByRepo, // Keep for backward compatibility
		"GroupedByRepo":  groupedByRepo,
		"GroupedByOwner": groupedByOwner,
		"ActivityCount":  s.activityFeed.Count(),
		"AlertCount":     s.alertChecker.GetAlertCount(),
		"VMUser":         s.vmUser,
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
		s.renderError(w, r, http.StatusNotFound, "VM Not Found", "No VM ID was provided.")
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
		s.renderError(w, r, http.StatusNotFound, "VM Not Found", "The VM you're looking for doesn't exist or has been destroyed.")
		return
	}

	snapshots, _ := s.daemon.ListSnapshots(ctx, id)

	data := map[string]interface{}{
		"Title":      task.ID,
		"User":       GetUser(r.Context()),
		"UserAvatar": GetUserAvatar(r.Context()),
		"ActiveNav":  "fleet",
		"Task":       task,
		"Snapshots":  snapshots,
		"VMUser":     s.vmUser,
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
		s.renderError(w, r, http.StatusNotFound, "VM Not Found", "No VM ID was provided.")
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
		s.renderError(w, r, http.StatusNotFound, "VM Not Found", "The VM you're looking for doesn't exist or has been destroyed.")
		return
	}

	snapshots, _ := s.daemon.ListSnapshots(ctx, id)

	data := map[string]interface{}{
		"Task":      task,
		"Snapshots": snapshots,
		"VMUser":    s.vmUser,
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

// renderError renders a styled error page for browser requests.
// API requests (paths starting with /api/) get plain text errors instead.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, code int, title, message string) {
	// API requests get plain text errors
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.Error(w, message, code)
		return
	}

	// If templates not available, fall back to plain text
	if s.templates == nil || s.templates.Lookup("error.html") == nil {
		http.Error(w, message, code)
		return
	}

	data := map[string]interface{}{
		"Code":       code,
		"Title":      title,
		"Message":    message,
		"User":       GetUser(r.Context()),
		"UserAvatar": GetUserAvatar(r.Context()),
		"ActiveNav":  "",
	}

	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "error.html", data); err != nil {
		http.Error(w, message, code)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(code)
	buf.WriteTo(w)
}

func (s *Server) handleAPIVMCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.daemon == nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Repo        string            `json:"repo"`
		Ref         string            `json:"ref"`
		Name        string            `json:"name"`
		Command     []string          `json:"command"`
		CPUs        int32             `json:"cpus"`
		MemoryMB    int32             `json:"memory_mb"`
		Env         map[string]string `json:"env"`
		NoTailscale bool              `json:"no_tailscale"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Repo == "" {
		http.Error(w, "repo is required", http.StatusBadRequest)
		return
	}

	// Defaults
	if req.Ref == "" {
		req.Ref = "main"
	}
	if req.CPUs == 0 {
		req.CPUs = 2
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 4096
	}

	task, err := s.daemon.CreateTask(r.Context(), CreateTaskRequest{
		Repo:        req.Repo,
		Ref:         req.Ref,
		Name:        req.Name,
		Command:     req.Command,
		CPUs:        req.CPUs,
		MemoryMB:    req.MemoryMB,
		Env:         req.Env,
		NoTailscale: req.NoTailscale,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     task.ID,
		"status": task.Status,
	})
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

	case r.Method == "POST" && action == "restart":
		if err := s.daemon.RestartTask(ctx, id); err != nil {
			http.Error(w, "Failed to restart task", http.StatusInternalServerError)
			return
		}
		w.Header().Set("HX-Refresh", "true")
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

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	events := s.activityFeed.GetRecent(50)

	data := map[string]interface{}{
		"Events":     events,
		"User":       GetUser(r.Context()),
		"UserAvatar": GetUserAvatar(r.Context()),
		"ActiveNav":  "activity",
	}

	// Check if template exists, fallback for testing
	if s.templates == nil || s.templates.Lookup("activity.html") == nil {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("Activity page"))
		return
	}

	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "activity.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	buf.WriteTo(w)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"User":       GetUser(r.Context()),
		"UserAvatar": GetUserAvatar(r.Context()),
		"ActiveNav":  "settings",
		"InstanceID": "stockyard",
		"Version":    "dev",
		"ZFSPool":    "tank",
		"ZFSVMsPath": "stockyard/vms",
		"BridgeName": "flbr0",
		"VMSubnet":   "10.0.100.0/24",
	}

	// Check if template exists, fallback for testing
	if s.templates == nil || s.templates.Lookup("settings.html") == nil {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("Settings page"))
		return
	}

	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "settings.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	buf.WriteTo(w)
}

func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	if s.daemon == nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	tasks, err := s.daemon.ListTasks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get paths from environment or use defaults
	vmDir := os.Getenv("STOCKYARD_VM_DIR")
	if vmDir == "" {
		vmDir = "/var/lib/stockyard/vms/stockyard"
	}
	leaseFile := os.Getenv("STOCKYARD_LEASE_FILE")
	if leaseFile == "" {
		leaseFile = "/var/lib/stockyard/dnsmasq.leases"
	}
	zfsPool := os.Getenv("STOCKYARD_ZFS_POOL")
	if zfsPool == "" {
		zfsPool = "tank/stockyard"
	}

	collector := NewResourceCollector(vmDir, leaseFile, zfsPool)

	resources, err := collector.Collect(tasks)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Group by type
	grouped := make(map[string][]Resource)
	for _, res := range resources {
		grouped[res.Type] = append(grouped[res.Type], res)
	}

	// Count orphans
	orphanCount := 0
	for _, res := range resources {
		if res.Status == "orphan" {
			orphanCount++
		}
	}

	data := map[string]interface{}{
		"Title":       "Resources",
		"User":        GetUser(r.Context()),
		"UserAvatar":  GetUserAvatar(r.Context()),
		"ActiveNav":   "resources",
		"Resources":   grouped,
		"OrphanCount": orphanCount,
		"TotalCount":  len(resources),
	}

	// Check if template exists, fallback for testing
	if s.templates == nil || s.templates.Lookup("resources.html") == nil {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("Resources page"))
		return
	}

	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, "resources.html", data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	buf.WriteTo(w)
}

// ActivityFeed returns the activity feed for recording events.
func (s *Server) ActivityFeed() *ActivityFeed {
	return s.activityFeed
}

// AlertChecker returns the alert checker for evaluating VM metrics.
func (s *Server) AlertChecker() *AlertChecker {
	return s.alertChecker
}

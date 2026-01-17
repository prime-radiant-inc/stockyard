package dashboard

import (
	"net/http"
)

// Server is the HTTP server for the web dashboard.
type Server struct {
	mux    *http.ServeMux
	daemon DaemonAPI
}

// NewServer creates a new dashboard HTTP server.
// The daemon parameter will be used for API calls (nil allowed for testing).
func NewServer(daemon DaemonAPI) *Server {
	s := &Server{
		mux:    http.NewServeMux(),
		daemon: daemon,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_HealthEndpoint(t *testing.T) {
	srv := NewServer(nil)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %s", w.Body.String())
	}
}

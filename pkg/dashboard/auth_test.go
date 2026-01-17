package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware_NoTailscale(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		if user != "anonymous" {
			t.Errorf("expected anonymous user, got %s", user)
		}
		w.WriteHeader(http.StatusOK)
	})

	wrapped := AuthMiddleware(handler, nil)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_WithTailscale(t *testing.T) {
	mockTS := &MockTailscaleClient{
		user: &User{Login: "jesse@example.com", Name: "Jesse"},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		if user != "jesse@example.com" {
			t.Errorf("expected jesse@example.com, got %s", user)
		}
		w.WriteHeader(http.StatusOK)
	})

	wrapped := AuthMiddleware(handler, mockTS)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

type MockTailscaleClient struct {
	user *User
}

func (m *MockTailscaleClient) WhoIs(ctx context.Context, remoteAddr string) (*User, error) {
	return m.user, nil
}

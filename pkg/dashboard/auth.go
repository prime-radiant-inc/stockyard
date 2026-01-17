package dashboard

import (
	"context"
	"net/http"
)

type contextKey string

const userContextKey contextKey = "user"

// User represents an authenticated user.
type User struct {
	Login   string
	Name    string
	IsAdmin bool
}

// TailscaleClient is the interface for Tailscale local API.
type TailscaleClient interface {
	WhoIs(ctx context.Context, remoteAddr string) (*User, error)
}

// AuthMiddleware adds user information to the request context.
func AuthMiddleware(next http.Handler, ts TailscaleClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := "anonymous"

		if ts != nil {
			if u, err := ts.WhoIs(r.Context(), r.RemoteAddr); err == nil && u != nil {
				user = u.Login
			}
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetUser returns the authenticated user from the context.
func GetUser(ctx context.Context) string {
	if user, ok := ctx.Value(userContextKey).(string); ok {
		return user
	}
	return "anonymous"
}

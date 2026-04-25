package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

// Middleware chain (outermost first):
// 1. RequireAuth -- validates session, injects user+session into context
// 2. RequireCSRF -- validates CSRF token on state-changing requests
// 3. RequireRole -- checks user has required role (optional, for admin-only routes)

// RequireCSRF returns middleware that validates the _csrf form field
// (or X-CSRF-Token header) against the session's CSRF token
// for state-changing HTTP methods (POST, PUT, PATCH, DELETE).
// Passes through GET, HEAD, OPTIONS without validation.
// This middleware must run after RequireAuth so the session is in context.
func RequireCSRF() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			session := auth.SessionFromContext(r.Context())
			if session == nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			token := r.FormValue("_csrf")
			if token == "" {
				token = r.Header.Get("X-CSRF-Token")
			}

			if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(session.CSRFToken)) != 1 {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

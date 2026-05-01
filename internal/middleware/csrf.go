package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

// Middleware chain (outermost first):
// 1. RequireAuth -- accepts an admin session cookie OR a device bearer token
// 2. RequireCSRF -- validates CSRF on state-changing admin requests; exempts devices
// 3. RequireRole -- checks the admin user has the required role (devices have no User)

// RequireCSRF returns middleware that validates the _csrf form field
// (or X-CSRF-Token header) against the session's CSRF token
// for state-changing HTTP methods (POST, PUT, PATCH, DELETE).
//
// Passes through GET, HEAD, OPTIONS without validation. Device-authenticated
// requests are also exempt: they authenticate with a Bearer header (or a
// device cookie that browsers will not auto-attach cross-site for non-form
// requests), so CSRF protections do not apply.
//
// This middleware must run after RequireAuth so the identity (and session,
// for the admin path) is in context.
func RequireCSRF() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			// Device requests are not CSRF-protected: the bearer token /
			// device cookie is not auto-attached cross-site by browsers in a
			// way that exposes a CSRF surface.
			if id := auth.IdentityFromContext(r.Context()); id != nil && id.IsDevice() {
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

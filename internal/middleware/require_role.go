package middleware

import (
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

// Middleware chain (outermost first):
// 1. RequireAuth -- validates session, injects user+session into context
// 2. RequireCSRF -- validates CSRF token on state-changing requests
// 3. RequireRole -- checks user has required role (optional, for admin-only routes)

// RequireRole returns middleware that checks the authenticated user
// has one of the allowed roles. Returns 403 if not.
// This middleware must run after RequireAuth so the user is in context.
func RequireRole(roles ...auth.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFromContext(r.Context())
			if user == nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			for _, role := range roles {
				if user.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}

			http.Error(w, "Forbidden", http.StatusForbidden)
		})
	}
}

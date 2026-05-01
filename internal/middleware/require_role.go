package middleware

import (
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

// Middleware chain (outermost first):
// 1. RequireAuth -- accepts an admin session cookie OR a device bearer token
// 2. RequireCSRF -- validates CSRF on state-changing admin requests; exempts devices
// 3. RequireRole -- checks the admin user has the required role (devices have no User)

// RequireRole returns middleware that checks the authenticated user
// has one of the allowed roles. Returns 403 if not.
// This middleware must run after RequireAuth so the user is in context.
func RequireRole(roles ...auth.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Devices do not have a User and are rejected here even though RequireAuth admitted them.
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

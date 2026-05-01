package middleware

import (
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

// Middleware chain (outermost first):
// 1. RequireAuth -- accepts an admin session cookie OR a device bearer token
// 2. RequireCSRF -- validates CSRF on state-changing admin requests; exempts devices
// 3. RequireDevice -- checks the request was authenticated as a device (rejects admins)

// RequireDevice returns middleware that allows the request through only when
// the context carries a device identity. Admin sessions and unauthenticated
// requests get a 403. Use this on routes that exclusively serve display
// devices (e.g. screen content endpoints in Phase 2).
//
// Must run after RequireAuth so the identity is in context.
func RequireDevice() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := auth.IdentityFromContext(r.Context())
			if id == nil || !id.IsDevice() {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

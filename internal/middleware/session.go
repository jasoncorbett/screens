package middleware

import (
	"log/slog"
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

// Middleware chain (outermost first):
// 1. RequireAuth -- validates session, injects user+session into context
// 2. RequireCSRF -- validates CSRF token on state-changing requests
// 3. RequireRole -- checks user has required role (optional, for admin-only routes)

// RequireAuth returns middleware that validates the session cookie,
// injects the user and session into the request context, and
// redirects to loginURL if unauthenticated or unauthorized.
func RequireAuth(authService *auth.Service, cookieName, loginURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cookieName)
			if err != nil {
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}

			user, session, err := authService.ValidateSession(r.Context(), cookie.Value)
			if err != nil {
				slog.Info("session validation failed", "err", err)
				// Clear the invalid cookie.
				http.SetCookie(w, &http.Cookie{
					Name:     cookieName,
					Value:    "",
					Path:     "/",
					MaxAge:   -1,
					HttpOnly: true,
				})
				http.Redirect(w, r, loginURL, http.StatusFound)
				return
			}

			ctx := auth.ContextWithUser(r.Context(), user)
			ctx = auth.ContextWithSession(ctx, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

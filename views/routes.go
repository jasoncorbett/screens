package views

import (
	"log/slog"
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/middleware"
)

type routeRegistrationFunc func(router *http.ServeMux)

var routes []routeRegistrationFunc

func registerRoute(r routeRegistrationFunc) {
	routes = append(routes, r)
}

// Deps holds dependencies needed by auth-dependent view handlers.
type Deps struct {
	Auth         *auth.Service
	Google       *auth.GoogleClient
	ClientID     string
	CookieName   string
	SecureCookie bool
}

// AddRoutes registers all view routes on the given mux.
// Public routes (registered via init/registerRoute) are added directly.
// Auth-dependent routes are registered using the provided deps,
// with protected routes wrapped in session and CSRF middleware.
func AddRoutes(router *http.ServeMux, deps *Deps) {
	slog.Debug("registering view routes", "count", len(routes))
	for _, r := range routes {
		r(router)
	}
	if deps != nil {
		registerAuthRoutes(router, deps)
	}
}

// registerAuthRoutes registers public auth routes and protected admin routes.
func registerAuthRoutes(mux *http.ServeMux, deps *Deps) {
	// Public routes (no auth required).
	mux.HandleFunc("GET /admin/login", handleLogin(deps.Auth, deps.CookieName))
	mux.HandleFunc("GET /auth/google/start", handleGoogleStart(deps.Google))
	mux.HandleFunc("GET /auth/google/callback", handleGoogleCallback(deps))

	// Protected admin routes.
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin/{$}", handleAdmin)
	adminMux.HandleFunc("POST /admin/logout", handleLogout(deps.Auth, deps.CookieName))

	// User management routes require admin role.
	userMux := http.NewServeMux()
	userMux.HandleFunc("GET /admin/users", handleUserList(deps.Auth))
	userMux.HandleFunc("POST /admin/users/invite", handleInvite(deps.Auth))
	userMux.HandleFunc("POST /admin/users/{id}/deactivate", handleDeactivate(deps.Auth))
	userMux.HandleFunc("POST /admin/invitations/{id}/revoke", handleRevokeInvitation(deps.Auth))
	adminMux.Handle("/admin/users", middleware.RequireRole(auth.RoleAdmin)(userMux))
	adminMux.Handle("/admin/users/", middleware.RequireRole(auth.RoleAdmin)(userMux))
	adminMux.Handle("/admin/invitations/", middleware.RequireRole(auth.RoleAdmin)(userMux))

	protected := middleware.RequireAuth(deps.Auth, deps.CookieName, "/admin/login")(
		middleware.RequireCSRF()(adminMux),
	)
	mux.Handle("/admin/", protected)
}

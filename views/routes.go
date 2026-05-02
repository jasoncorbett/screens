package views

import (
	"log/slog"
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/middleware"
	"github.com/jasoncorbett/screens/internal/themes"
)

type routeRegistrationFunc func(router *http.ServeMux)

var routes []routeRegistrationFunc

func registerRoute(r routeRegistrationFunc) {
	routes = append(routes, r)
}

// Deps holds dependencies needed by auth-dependent view handlers.
type Deps struct {
	Auth             *auth.Service
	Google           *auth.GoogleClient
	ClientID         string
	CookieName       string
	DeviceCookieName string
	DeviceLandingURL string
	SecureCookie     bool
	Themes           *themes.Service
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

	// Device management routes require admin role.
	deviceMux := http.NewServeMux()
	deviceMux.HandleFunc("GET /admin/devices", handleDeviceList(deps.Auth))
	deviceMux.HandleFunc("POST /admin/devices", handleDeviceCreate(deps.Auth))
	deviceMux.HandleFunc("POST /admin/devices/{id}/revoke", handleDeviceRevoke(deps.Auth))
	deviceMux.HandleFunc("POST /admin/devices/{id}/enroll-browser", handleDeviceEnrollExisting(deps))
	deviceMux.HandleFunc("POST /admin/devices/enroll-new-browser", handleDeviceEnrollNew(deps))
	adminMux.Handle("/admin/devices", middleware.RequireRole(auth.RoleAdmin)(deviceMux))
	adminMux.Handle("/admin/devices/", middleware.RequireRole(auth.RoleAdmin)(deviceMux))

	// Theme management routes require admin role.
	themeMux := http.NewServeMux()
	themeMux.HandleFunc("GET /admin/themes", handleThemeList(deps.Themes))
	themeMux.HandleFunc("POST /admin/themes", handleThemeCreate(deps.Themes))
	themeMux.HandleFunc("GET /admin/themes/{id}/edit", handleThemeEditForm(deps.Themes))
	themeMux.HandleFunc("POST /admin/themes/{id}", handleThemeUpdate(deps.Themes))
	themeMux.HandleFunc("POST /admin/themes/{id}/delete", handleThemeDelete(deps.Themes))
	themeMux.HandleFunc("POST /admin/themes/{id}/set-default", handleThemeSetDefault(deps.Themes))
	adminMux.Handle("/admin/themes", middleware.RequireRole(auth.RoleAdmin)(themeMux))
	adminMux.Handle("/admin/themes/", middleware.RequireRole(auth.RoleAdmin)(themeMux))

	protected := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
		middleware.RequireCSRF()(adminMux),
	)
	mux.Handle("/admin/", protected)

	// Device landing route: gated by RequireAuth only -- a device identity
	// is sufficient. NOT wrapped in RequireRole or RequireCSRF because the
	// admin cookie has been cleared by the time the browser arrives.
	landingHandler := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
		http.HandlerFunc(handleDeviceLanding(deps.Auth)),
	)
	mux.Handle("GET "+deps.DeviceLandingURL, landingHandler)
}

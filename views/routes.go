package views

import (
	"log/slog"
	"net/http"
)

type routeRegistrationFunc func(router *http.ServeMux)

var routes []routeRegistrationFunc

func registerRoute(r routeRegistrationFunc) {
	routes = append(routes, r)
}

// AddRoutes registers all view routes on the given mux.
func AddRoutes(router *http.ServeMux) {
	slog.Debug("registering view routes", "count", len(routes))
	for _, r := range routes {
		r(router)
	}
}

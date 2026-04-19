package v1

import (
	"log/slog"
	"net/http"
)

type routeRegistrationFunc func(router *http.ServeMux)

var routes []routeRegistrationFunc

func registerRoute(r routeRegistrationFunc) {
	routes = append(routes, r)
}

// AddRoutes registers all v1 API routes on the given mux.
func AddRoutes(router *http.ServeMux) {
	slog.Debug("registering v1 API routes", "count", len(routes))
	for _, r := range routes {
		r(router)
	}
}

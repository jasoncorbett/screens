package api

import (
	"log/slog"
	"net/http"

	v1 "github.com/jasoncorbett/screens/api/v1"
)

// AddRoutes registers all API routes on the given mux.
func AddRoutes(router *http.ServeMux) {
	slog.Debug("registering API routes")

	router.HandleFunc("GET /health", handleHealth)

	v1.AddRoutes(router)
}

package v1

import (
	"net/http"

	"github.com/jasoncorbett/screens/internal/version"
)

func init() {
	registerRoute(func(router *http.ServeMux) {
		router.HandleFunc("GET /api/v1/version", handleVersion)
	})
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(version.Version))
}


package views

import (
	"net/http"
	"time"
)

func init() {
	registerRoute(func(router *http.ServeMux) {
		router.HandleFunc("GET /{$}", handleDemo)
		router.HandleFunc("GET /views/status", handleStatus)
	})
}

func handleDemo(w http.ResponseWriter, r *http.Request) {
	demoPage().Render(r.Context(), w)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Format("15:04:05")
	statusFragment(now).Render(r.Context(), w)
}


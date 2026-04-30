package views

import (
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

// handleDeviceLanding renders the device landing page. The placeholder body
// only reads identity from the request context; the authSvc parameter is kept
// in the signature so Phase 2 (Screen Display) can attach real screen content
// without changing the route registration shape.
func handleDeviceLanding(authSvc *auth.Service) http.HandlerFunc {
	_ = authSvc
	return func(w http.ResponseWriter, r *http.Request) {
		id := auth.IdentityFromContext(r.Context())
		if id == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		var name string
		switch {
		case id.IsDevice() && id.Device != nil:
			name = id.Device.Name
		case id.IsAdmin() && id.User != nil:
			name = "(viewing as admin: " + id.User.Email + ")"
		default:
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		deviceLandingPage(name).Render(r.Context(), w)
	}
}

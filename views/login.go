package views

import (
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

func handleLogin(authSvc *auth.Service, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If the user already has a valid session, redirect to admin.
		cookie, err := r.Cookie(cookieName)
		if err == nil {
			_, _, err := authSvc.ValidateSession(r.Context(), cookie.Value)
			if err == nil {
				http.Redirect(w, r, "/admin/", http.StatusFound)
				return
			}
		}

		errorMsg := r.URL.Query().Get("error")
		loginPage(errorMsg).Render(r.Context(), w)
	}
}

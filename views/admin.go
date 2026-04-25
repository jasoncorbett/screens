package views

import (
	"net/http"

	"github.com/jasoncorbett/screens/internal/auth"
)

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	session := auth.SessionFromContext(r.Context())

	if user == nil || session == nil {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}

	adminPage(
		user.Email,
		user.DisplayName,
		session.CSRFToken,
		user.Role == auth.RoleAdmin,
	).Render(r.Context(), w)
}

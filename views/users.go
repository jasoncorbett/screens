package views

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasoncorbett/screens/internal/auth"
)

func handleUserList(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		users, err := authSvc.ListUsers(ctx)
		if err != nil {
			slog.Error("list users", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		invitations, err := authSvc.ListInvitations(ctx)
		if err != nil {
			slog.Error("list invitations", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		msg := r.URL.Query().Get("msg")
		errMsg := r.URL.Query().Get("error")

		var flashMsg string
		switch msg {
		case "invited":
			flashMsg = "Invitation sent successfully."
		case "deactivated":
			flashMsg = "User deactivated."
		case "revoked":
			flashMsg = "Invitation revoked."
		}

		usersPage(users, invitations, user, session.CSRFToken, flashMsg, errMsg).Render(ctx, w)
	}
}

func handleInvite(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		if user == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		email := strings.TrimSpace(r.FormValue("email"))
		role := r.FormValue("role")

		if email == "" {
			http.Redirect(w, r, "/admin/users?error=Email+is+required", http.StatusFound)
			return
		}
		if !strings.Contains(email, "@") {
			http.Redirect(w, r, "/admin/users?error=Invalid+email+address", http.StatusFound)
			return
		}
		if role != string(auth.RoleAdmin) && role != string(auth.RoleMember) {
			http.Redirect(w, r, "/admin/users?error=Invalid+role", http.StatusFound)
			return
		}

		if err := authSvc.InviteUser(ctx, email, auth.Role(role), user.ID); err != nil {
			slog.Error("invite user", "err", err)
			http.Redirect(w, r, "/admin/users?error=Could+not+create+invitation", http.StatusFound)
			return
		}

		slog.Info("user invited", "email", email, "role", role, "invited_by", user.Email)
		http.Redirect(w, r, "/admin/users?msg=invited", http.StatusFound)
	}
}

func handleDeactivate(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		currentUser := auth.UserFromContext(ctx)
		if currentUser == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		userID := r.PathValue("id")
		if userID == "" {
			http.Redirect(w, r, "/admin/users?error=Missing+user+ID", http.StatusFound)
			return
		}

		if userID == currentUser.ID {
			http.Redirect(w, r, "/admin/users?error=Cannot+deactivate+yourself", http.StatusFound)
			return
		}

		if err := authSvc.DeactivateUser(ctx, userID); err != nil {
			slog.Error("deactivate user", "err", err, "user_id", userID)
			http.Redirect(w, r, "/admin/users?error=Could+not+deactivate+user", http.StatusFound)
			return
		}

		slog.Info("user deactivated", "user_id", userID, "deactivated_by", currentUser.Email)
		http.Redirect(w, r, "/admin/users?msg=deactivated", http.StatusFound)
	}
}

func handleRevokeInvitation(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		currentUser := auth.UserFromContext(ctx)
		if currentUser == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		invitationID := r.PathValue("id")
		if invitationID == "" {
			http.Redirect(w, r, "/admin/users?error=Missing+invitation+ID", http.StatusFound)
			return
		}

		if err := authSvc.RevokeInvitation(ctx, invitationID); err != nil {
			slog.Error("revoke invitation", "err", err, "invitation_id", invitationID)
			http.Redirect(w, r, "/admin/users?error=Could+not+revoke+invitation", http.StatusFound)
			return
		}

		slog.Info("invitation revoked", "invitation_id", invitationID, "revoked_by", currentUser.Email)
		http.Redirect(w, r, "/admin/users?msg=revoked", http.StatusFound)
	}
}

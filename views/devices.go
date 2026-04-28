package views

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasoncorbett/screens/internal/auth"
)

func handleDeviceList(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		devices, err := authSvc.ListDevices(ctx)
		if err != nil {
			slog.Error("list devices", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		msg := r.URL.Query().Get("msg")
		errMsg := r.URL.Query().Get("error")

		var flashMsg string
		switch msg {
		case "revoked":
			flashMsg = "Device revoked."
		}

		devicesPage(devices, user, session.CSRFToken, flashMsg, errMsg, "", "").Render(ctx, w)
	}
}

func handleDeviceCreate(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		user := auth.UserFromContext(ctx)
		session := auth.SessionFromContext(ctx)

		if user == nil || session == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			http.Redirect(w, r, "/admin/devices?error=Name+is+required", http.StatusFound)
			return
		}

		dev, rawToken, err := authSvc.CreateDevice(ctx, name, user.ID)
		if err != nil {
			slog.Error("create device", "err", err)
			http.Redirect(w, r, "/admin/devices?error=Could+not+create+device", http.StatusFound)
			return
		}

		slog.Info("device created", "device_id", dev.ID, "name", dev.Name, "created_by", user.Email)

		devices, err := authSvc.ListDevices(ctx)
		if err != nil {
			slog.Error("list devices after create", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Render in-place so the raw token can be shown exactly once. A
		// redirect would lose the token.
		devicesPage(devices, user, session.CSRFToken, "", "", dev.Name, rawToken).Render(ctx, w)
	}
}

func handleDeviceRevoke(authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		currentUser := auth.UserFromContext(ctx)
		if currentUser == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		deviceID := r.PathValue("id")
		if deviceID == "" {
			http.Redirect(w, r, "/admin/devices?error=Missing+device+ID", http.StatusFound)
			return
		}

		if err := authSvc.RevokeDevice(ctx, deviceID); err != nil {
			if errors.Is(err, auth.ErrDeviceNotFound) {
				http.Redirect(w, r, "/admin/devices?error=Device+not+found", http.StatusFound)
				return
			}
			slog.Error("revoke device", "err", err, "device_id", deviceID)
			http.Redirect(w, r, "/admin/devices?error=Could+not+revoke+device", http.StatusFound)
			return
		}

		slog.Info("device revoked", "device_id", deviceID, "revoked_by", currentUser.Email)
		http.Redirect(w, r, "/admin/devices?msg=revoked", http.StatusFound)
	}
}

// hasRevokedDevices reports whether the slice contains at least one revoked
// device. Used by the templ to conditionally render the revoked section.
func hasRevokedDevices(devices []auth.Device) bool {
	for _, d := range devices {
		if d.IsRevoked() {
			return true
		}
	}
	return false
}

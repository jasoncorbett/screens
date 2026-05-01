package views

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// hasActiveDevices reports whether the slice contains at least one
// non-revoked device. Used by the templ to conditionally render the
// "enroll as existing device" list.
func hasActiveDevices(devices []auth.Device) bool {
	for _, d := range devices {
		if !d.IsRevoked() {
			return true
		}
	}
	return false
}

// performBrowserEnrollment swaps the caller's admin session cookie for a
// fresh device cookie bound to the given deviceID, deletes the admin
// session row from the database, and writes a 302 to the device landing
// URL. Returns a non-nil error WITHOUT mutating any cookies if the device
// is missing or revoked; the caller MUST handle that case by redirecting
// back to /admin/devices?error=... .
func performBrowserEnrollment(w http.ResponseWriter, r *http.Request, deps *Deps, deviceID string) error {
	ctx := r.Context()

	// Step 1: Rotate the device token FIRST. If this fails (device missing
	// or revoked), abort BEFORE touching any cookies so the admin's session
	// remains intact.
	rawToken, err := deps.Auth.RotateDeviceToken(ctx, deviceID)
	if err != nil {
		if errors.Is(err, auth.ErrDeviceNotFound) {
			return errors.New("Device not found or revoked")
		}
		slog.Error("rotate device token", "err", err, "device_id", deviceID)
		return errors.New("Could not enroll browser")
	}

	// Step 2: Best-effort delete of the admin session row backing this
	// request. We continue with the cookie swap regardless of failure here:
	// the user-visible state (browser is now the device) is more important
	// than the orphan-row cleanup, which CleanExpiredSessions handles later.
	if c, cErr := r.Cookie(deps.CookieName); cErr == nil && c.Value != "" {
		if logoutErr := deps.Auth.Logout(ctx, c.Value); logoutErr != nil {
			slog.Error("enroll-browser logout admin session", "err", logoutErr)
		}
	}

	// Step 3: Clear the admin session cookie on this browser only.
	http.SetCookie(w, &http.Cookie{
		Name:     deps.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Expires:  time.Unix(0, 0),
	})

	// Step 4: Set the device cookie carrying the freshly rotated raw token.
	http.SetCookie(w, &http.Cookie{
		Name:     deps.DeviceCookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   deps.SecureCookie,
		SameSite: http.SameSiteLaxMode,
	})

	// Step 5: Audit log. NEVER include the raw token.
	enrolledBy := ""
	if currentUser := auth.UserFromContext(ctx); currentUser != nil {
		enrolledBy = currentUser.Email
	}
	slog.Info("device enrolled via browser",
		"device_id", deviceID,
		"enrolled_by", enrolledBy)

	// Step 6: Redirect to the device landing URL.
	http.Redirect(w, r, deps.DeviceLandingURL, http.StatusFound)
	return nil
}

func handleDeviceEnrollExisting(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("id")
		if deviceID == "" {
			http.Redirect(w, r, "/admin/devices?error=Missing+device+ID", http.StatusFound)
			return
		}
		if err := performBrowserEnrollment(w, r, deps, deviceID); err != nil {
			http.Redirect(w, r, "/admin/devices?error="+url.QueryEscape(err.Error()), http.StatusFound)
			return
		}
		// performBrowserEnrollment wrote the redirect on success.
	}
}

func handleDeviceEnrollNew(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		currentUser := auth.UserFromContext(ctx)
		if currentUser == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			http.Redirect(w, r, "/admin/devices?error=Name+is+required", http.StatusFound)
			return
		}

		dev, _, err := deps.Auth.CreateDevice(ctx, name, currentUser.ID)
		if err != nil {
			slog.Error("enroll-new-browser create device", "err", err)
			http.Redirect(w, r, "/admin/devices?error=Could+not+create+device", http.StatusFound)
			return
		}

		// Discard the raw token from CreateDevice -- the helper re-issues a
		// fresh one via RotateDeviceToken so the same swap path handles both
		// endpoints.
		if err := performBrowserEnrollment(w, r, deps, dev.ID); err != nil {
			slog.Error("enroll-new-browser swap", "device_id", dev.ID, "err", err)
			http.Redirect(w, r, "/admin/devices?error="+url.QueryEscape(err.Error()), http.StatusFound)
			return
		}
	}
}

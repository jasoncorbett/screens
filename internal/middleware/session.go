package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jasoncorbett/screens/internal/auth"
)

// Middleware chain (outermost first):
// 1. RequireAuth -- accepts an admin session cookie OR a device bearer token
//    (Authorization header or device cookie); injects auth.Identity into context
// 2. RequireCSRF -- validates CSRF on state-changing admin requests; exempts devices
// 3. RequireRole -- checks the admin user has the required role (devices have no User)

// RequireAuth returns middleware that authenticates the request using either
// an admin session cookie or a device bearer token (header or cookie), in
// that order:
//
//  1. Admin session cookie. On success, populates ContextWithUser,
//     ContextWithSession, and ContextWithIdentity{IdentityAdmin}.
//  2. Authorization: Bearer <token>. On success, populates ContextWithDevice
//     and ContextWithIdentity{IdentityDevice} and best-effort updates
//     last_seen_at via MarkDeviceSeen.
//  3. Device cookie. Same handling as the Bearer header.
//
// On failure the handler clears any stale session cookie and either redirects
// HTML navigations to loginURL (302) or responds 401 with a
// `WWW-Authenticate: Bearer` header for everything else. A single slog.Info
// line is emitted on failure -- raw tokens and cookie values are never logged.
func RequireAuth(authService *auth.Service, sessionCookie, deviceCookie, loginURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Admin session cookie (fast path: humans clicking around the UI).
			if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
				if user, sess, vErr := authService.ValidateSession(r.Context(), c.Value); vErr == nil {
					id := &auth.Identity{Kind: auth.IdentityAdmin, User: user}
					ctx := auth.ContextWithUser(r.Context(), user)
					ctx = auth.ContextWithSession(ctx, sess)
					ctx = auth.ContextWithIdentity(ctx, id)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Fall through to device probes; if all fail, denyUnauthenticated
				// will clear the bad cookie below.
			}

			// 2. Authorization: Bearer <token>.
			//    Track the most informative device-side failure so the final
			//    log line can attribute the failure to the device probe (e.g.
			//    "revoked") rather than always saying kind=none.
			deviceKind, deviceReason := "none", ""
			if raw := bearerToken(r); raw != "" {
				dev, err := authService.ValidateDeviceToken(r.Context(), raw)
				if err == nil {
					finishDevice(w, r, authService, next, dev)
					return
				}
				deviceKind, deviceReason = "device", deviceFailureReason(err)
			}

			// 3. Device cookie.
			if c, err := r.Cookie(deviceCookie); err == nil && c.Value != "" {
				dev, err := authService.ValidateDeviceToken(r.Context(), c.Value)
				if err == nil {
					finishDevice(w, r, authService, next, dev)
					return
				}
				// Prefer the bearer reason if it was already populated.
				if deviceReason == "" {
					deviceKind, deviceReason = "device", deviceFailureReason(err)
				}
			}

			if deviceReason != "" {
				slog.Info("auth failed", "kind", deviceKind, "reason", deviceReason, "path", r.URL.Path)
			} else {
				slog.Info("auth failed", "kind", "none", "path", r.URL.Path)
			}
			denyUnauthenticated(w, r, sessionCookie, loginURL)
		})
	}
}

// deviceFailureReason converts a ValidateDeviceToken error into a sanitised
// reason string suitable for an info-level log line. Raw token contents and
// internal SQL details are deliberately not included.
func deviceFailureReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrDeviceRevoked):
		return "revoked"
	case errors.Is(err, auth.ErrDeviceNotFound):
		return "unknown_token"
	default:
		return "lookup_error"
	}
}

// bearerToken extracts the token portion of an `Authorization: Bearer <token>`
// header. The `Bearer ` prefix is matched case-sensitively; any other scheme
// (Basic, Token, lowercase bearer) returns the empty string. Empty/whitespace
// values also return the empty string.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// finishDevice marks the device seen (best-effort) and dispatches to next with
// the device + identity injected into the request context.
func finishDevice(w http.ResponseWriter, r *http.Request, svc *auth.Service, next http.Handler, dev *auth.Device) {
	if err := svc.MarkDeviceSeen(r.Context(), dev.ID); err != nil {
		// Best-effort: throttled at the database level and not on the auth
		// path's critical chain.
		slog.Debug("mark device seen failed", "device_id", dev.ID, "err", err)
	}
	id := &auth.Identity{Kind: auth.IdentityDevice, Device: dev}
	ctx := auth.ContextWithDevice(r.Context(), dev)
	ctx = auth.ContextWithIdentity(ctx, id)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// denyUnauthenticated clears any stale session cookie and either redirects an
// HTML navigation to loginURL or returns a bare 401 with the Bearer challenge.
func denyUnauthenticated(w http.ResponseWriter, r *http.Request, sessionCookie, loginURL string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	if isHTMLNav(r) {
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthenticated", http.StatusUnauthorized)
}

// isHTMLNav reports whether the request is a page navigation that should be
// redirected to the login URL on failure -- i.e. a GET or HEAD whose Accept
// header advertises support for text/html. Anything else gets a 401.
func isHTMLNav(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

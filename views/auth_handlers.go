package views

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/jasoncorbett/screens/internal/auth"
)

func handleGoogleStart(google *auth.GoogleClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			slog.Error("generate oauth state", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		state := hex.EncodeToString(b)

		http.SetCookie(w, &http.Cookie{
			Name:     "oauth_state",
			Value:    state,
			Path:     "/",
			MaxAge:   300,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})

		url := google.AuthorizationURL(state)
		http.Redirect(w, r, url, http.StatusFound)
	}
}

func handleGoogleCallback(deps *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		state := r.URL.Query().Get("state")
		stateCookie, err := r.Cookie("oauth_state")
		if err != nil || state == "" || state != stateCookie.Value {
			http.Redirect(w, r, "/admin/login?error=Invalid+authentication+state", http.StatusFound)
			return
		}

		// Clear the state cookie.
		http.SetCookie(w, &http.Cookie{
			Name:     "oauth_state",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		})

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Redirect(w, r, "/admin/login?error=Missing+authorization+code", http.StatusFound)
			return
		}

		idToken, err := deps.Google.ExchangeCode(ctx, code)
		if err != nil {
			slog.Error("exchange authorization code", "err", err)
			http.Redirect(w, r, "/admin/login?error=Authentication+failed", http.StatusFound)
			return
		}

		email, displayName, err := deps.Google.ValidateIDToken(ctx, idToken, deps.ClientID)
		if err != nil {
			slog.Error("validate id token", "err", err)
			http.Redirect(w, r, "/admin/login?error=Authentication+failed", http.StatusFound)
			return
		}

		user, err := deps.Auth.ProvisionUser(ctx, email, displayName)
		if err != nil {
			slog.Info("user provisioning rejected", "email", email, "err", err)
			http.Redirect(w, r, "/admin/login?error=Access+denied", http.StatusFound)
			return
		}

		rawToken, err := deps.Auth.CreateSession(ctx, user.ID)
		if err != nil {
			slog.Error("create session", "err", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		maxAge := int(deps.Auth.SessionDuration().Seconds())
		http.SetCookie(w, &http.Cookie{
			Name:     deps.CookieName,
			Value:    rawToken,
			Path:     "/",
			MaxAge:   maxAge,
			HttpOnly: true,
			Secure:   deps.SecureCookie,
			SameSite: http.SameSiteLaxMode,
		})

		slog.Info("user authenticated", "email", email)
		http.Redirect(w, r, "/admin/", http.StatusFound)
	}
}

func handleLogout(authSvc *auth.Service, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err == nil {
			if logoutErr := authSvc.Logout(r.Context(), cookie.Value); logoutErr != nil {
				slog.Error("logout session delete", "err", logoutErr)
			}
		}

		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Expires:  time.Unix(0, 0),
		})

		http.Redirect(w, r, "/admin/login", http.StatusFound)
	}
}

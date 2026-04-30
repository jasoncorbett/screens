package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/db"
)

// --- helpers ---

// newDeviceTestService builds an auth.Service backed by a fresh in-memory
// database with the device-related config populated. The interval governs the
// MarkDeviceSeen throttle. Returns the service, queries, and a creator user
// for use as the device's CreatedBy.
//
// MaxOpenConns is pinned to 1 because modernc.org/sqlite gives each new
// connection its own private :memory: database; without this cap, parallel
// goroutines that hit a fresh connection see "no such table: devices".
func newDeviceTestService(t *testing.T, interval time.Duration) (*auth.Service, *db.Queries, db.User) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	sqlDB.SetMaxOpenConns(1)
	cfg := auth.Config{
		AdminEmail:             "admin@example.com",
		SessionDuration:        time.Hour,
		CookieName:             "session",
		SecureCookie:           false,
		DeviceCookieName:       "screens_device",
		DeviceLastSeenInterval: interval,
		DeviceLandingURL:       "/device/",
	}
	svc := auth.NewService(sqlDB, cfg)
	q := db.New(sqlDB)
	creator := createTestUser(t, q, "device-creator@example.com", "admin")
	return svc, q, creator
}

// makeDevice provisions a device and returns its row + raw token.
func makeDevice(t *testing.T, svc *auth.Service, creatorID, name string) (auth.Device, string) {
	t.Helper()
	dev, raw, err := svc.CreateDevice(context.Background(), name, creatorID)
	if err != nil {
		t.Fatalf("create device %q: %v", name, err)
	}
	return dev, raw
}

// captureIdentity returns a handler that records the request's Identity and
// writes 200.
func captureIdentity(out **auth.Identity) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*out = auth.IdentityFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}
}

// --- Bearer + cookie probe tests ---

func TestRequireAuth_AdminPath_PopulatesIdentity(t *testing.T) {
	t.Parallel()
	svc, q, _ := newDeviceTestService(t, time.Minute)

	user := createTestUser(t, q, "admin-user@example.com", "admin")
	rawSession, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var got *auth.Identity
	var gotUser *auth.User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = auth.IdentityFromContext(r.Context())
		gotUser = auth.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireAuth(svc, "session", "screens_device", "/login")(inner)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawSession})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsAdmin() {
		t.Fatalf("identity = %+v, want IsAdmin", got)
	}
	if got.User == nil || got.User.ID != user.ID {
		t.Errorf("identity.User = %+v, want id=%q", got.User, user.ID)
	}
	if gotUser == nil || gotUser.ID != user.ID {
		t.Errorf("UserFromContext = %+v, want id=%q (back-compat)", gotUser, user.ID)
	}
}

func TestRequireAuth_BearerHeader_DevicePath(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, raw := makeDevice(t, svc, creator.ID, "kitchen")

	var got *auth.Identity
	handler := RequireAuth(svc, "session", "screens_device", "/login")(captureIdentity(&got))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsDevice() {
		t.Fatalf("identity = %+v, want IsDevice", got)
	}
	if got.Device == nil || got.Device.ID != dev.ID {
		t.Errorf("identity.Device.ID = %q, want %q", got.Device.ID, dev.ID)
	}
}

func TestRequireAuth_DeviceCookie_DevicePath(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, raw := makeDevice(t, svc, creator.ID, "lobby")

	var got *auth.Identity
	handler := RequireAuth(svc, "session", "screens_device", "/login")(captureIdentity(&got))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "screens_device", Value: raw})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsDevice() {
		t.Fatalf("identity = %+v, want IsDevice", got)
	}
	if got.Device == nil || got.Device.ID != dev.ID {
		t.Errorf("identity.Device.ID = %q, want %q", got.Device.ID, dev.ID)
	}
}

func TestRequireAuth_HeaderBeatsCookie(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	devA, rawA := makeDevice(t, svc, creator.ID, "device-a")
	_, rawB := makeDevice(t, svc, creator.ID, "device-b")

	var got *auth.Identity
	handler := RequireAuth(svc, "session", "screens_device", "/login")(captureIdentity(&got))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+rawA)
	req.AddCookie(&http.Cookie{Name: "screens_device", Value: rawB})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsDevice() {
		t.Fatalf("identity = %+v, want IsDevice", got)
	}
	if got.Device == nil || got.Device.ID != devA.ID {
		t.Errorf("identity.Device.ID = %q, want %q (header should win)", got.Device.ID, devA.ID)
	}
}

// --- Failure-mode tests ---

func TestRequireAuth_NoCredentialHTMLNav_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q, want /admin/login", loc)
	}
}

func TestRequireAuth_NoCredentialNonHTML_Returns401(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())

	tests := []struct {
		name   string
		accept string
	}{
		{"no accept header", ""},
		{"json accept", "application/json"},
		{"plain text accept", "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
			if got := rr.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want Bearer", got)
			}
		})
	}
}

func TestRequireAuth_BadBearerScheme_Returns401(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())

	tests := []struct {
		name string
		auth string
	}{
		{"basic", "Basic abc"},
		{"lowercase bearer", "bearer somerawtoken"},
		{"token scheme", "Token abc"},
		{"no space", "Bearerabc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
			req.Header.Set("Authorization", tt.auth)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestRequireAuth_EmptyBearerValue_Returns401(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())

	tests := []struct {
		name string
		auth string
	}{
		{"empty after prefix", "Bearer "},
		{"only spaces", "Bearer    "},
		{"only tab", "Bearer \t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
			req.Header.Set("Authorization", tt.auth)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestRequireAuth_RevokedDevice_Returns401(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, raw := makeDevice(t, svc, creator.ID, "to-be-revoked")
	if err := svc.RevokeDevice(context.Background(), dev.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	called := false
	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	if called {
		t.Error("inner handler should not have been called for revoked device")
	}
	// The raw token must never appear anywhere observable. The slog handler
	// writes to stderr in tests; the strongest contract we can assert from
	// here without intercepting slog is that the request was rejected and
	// the inner handler did not run -- both true above.
}

func TestRequireAuth_StaleSessionCookieClearedOnNonHTML(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "stale-bogus-value"})
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "session" && c.MaxAge < 0 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Error("expected stale session cookie to be cleared with MaxAge < 0")
	}
}

// --- CSRF interaction tests ---

func TestRequireCSRF_DeviceExempt(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	_, raw := makeDevice(t, svc, creator.ID, "csrf-device")

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(
		RequireCSRF()(inner),
	)
	// POST with NO _csrf field, authenticated by bearer.
	req := httptest.NewRequest(http.MethodPost, "/api/state-change", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (devices are CSRF-exempt)", rr.Code, http.StatusOK)
	}
	if !called {
		t.Error("inner handler should have been called for device POST")
	}
}

func TestRequireCSRF_AdminWithoutTokenStillRejected(t *testing.T) {
	t.Parallel()
	svc, q, _ := newDeviceTestService(t, time.Minute)

	user := createTestUser(t, q, "csrf-admin@example.com", "admin")
	rawSession, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(
		RequireCSRF()(inner),
	)
	// POST with NO _csrf field, authenticated by session cookie.
	req := httptest.NewRequest(http.MethodPost, "/admin/action", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawSession})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (admin POST without csrf must be 403)", rr.Code, http.StatusForbidden)
	}
	if called {
		t.Error("inner handler should not have been called for admin POST without csrf")
	}
}

// --- RequireRole interaction with devices ---

func TestRequireRole_RejectsDevices(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	_, raw := makeDevice(t, svc, creator.ID, "role-test")
	adminUser := createTestUser(t, q, "role-admin@example.com", "admin")
	rawSession, err := svc.CreateSession(context.Background(), adminUser.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, sessRow, err := svc.ValidateSession(context.Background(), rawSession)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(
		RequireCSRF()(
			RequireRole(auth.RoleAdmin)(okHandler()),
		),
	)

	t.Run("device blocked", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/admin/action", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
		}
	})

	t.Run("admin allowed", func(t *testing.T) {
		t.Parallel()
		form := url.Values{"_csrf": {sessRow.CSRFToken}}
		req := httptest.NewRequest(http.MethodPost, "/admin/action", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: rawSession})
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
	})
}

// --- RequireDevice tests ---

func TestRequireDevice(t *testing.T) {
	t.Parallel()

	called := false
	handler := RequireDevice()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("no identity is forbidden", func(t *testing.T) {
		called = false
		req := httptest.NewRequest(http.MethodGet, "/device-only", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
		}
		if called {
			t.Error("handler should not have been called")
		}
	})

	t.Run("admin identity is forbidden", func(t *testing.T) {
		called = false
		id := &auth.Identity{Kind: auth.IdentityAdmin, User: &auth.User{ID: "u1"}}
		ctx := auth.ContextWithIdentity(context.Background(), id)
		req := httptest.NewRequest(http.MethodGet, "/device-only", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
		}
		if called {
			t.Error("handler should not have been called for admin")
		}
	})

	t.Run("device identity passes through", func(t *testing.T) {
		called = false
		id := &auth.Identity{Kind: auth.IdentityDevice, Device: &auth.Device{ID: "d1"}}
		ctx := auth.ContextWithIdentity(context.Background(), id)
		req := httptest.NewRequest(http.MethodGet, "/device-only", nil)
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		if !called {
			t.Error("handler should have been called for device")
		}
	})
}

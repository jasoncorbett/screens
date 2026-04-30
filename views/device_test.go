package views

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasoncorbett/screens/internal/middleware"
)

// TestHandleDeviceLanding_DeviceIdentity verifies that a request bearing a
// valid device cookie reaches the landing handler and the device's name
// appears in the rendered body.
func TestHandleDeviceLanding_DeviceIdentity(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	_, rawDeviceToken, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	chain := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
		http.HandlerFunc(handleDeviceLanding(deps.Auth)),
	)

	req := httptest.NewRequest(http.MethodGet, deps.DeviceLandingURL, nil)
	req.AddCookie(&http.Cookie{Name: deps.DeviceCookieName, Value: rawDeviceToken})
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "kitchen-tablet") {
		t.Errorf("response body missing device name; body = %q", body)
	}
	if !strings.Contains(body, "Enrolled") {
		t.Error("response body missing 'Enrolled' heading")
	}
}

// TestHandleDeviceLanding_AdminIdentity verifies that an admin who navigates
// directly to /device/ sees a "viewing as admin" message rather than a 403 or
// blank page.
func TestHandleDeviceLanding_AdminIdentity(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	chain := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
		http.HandlerFunc(handleDeviceLanding(deps.Auth)),
	)

	req := httptest.NewRequest(http.MethodGet, deps.DeviceLandingURL, nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "admin@example.com") {
		t.Errorf("response body missing admin email; body = %q", body)
	}
	if !strings.Contains(body, "viewing as admin") {
		t.Error("response body missing 'viewing as admin' formatting")
	}
}

// TestHandleDeviceLanding_NoAuth_HTML verifies that an HTML navigation with no
// auth cookie is redirected to /admin/login by the RequireAuth middleware.
func TestHandleDeviceLanding_NoAuth_HTML(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	chain := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
		http.HandlerFunc(handleDeviceLanding(deps.Auth)),
	)

	req := httptest.NewRequest(http.MethodGet, deps.DeviceLandingURL, nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("Location = %q, want %q", loc, "/admin/login")
	}
}

// TestHandleDeviceLanding_NoAuth_NonHTML verifies that a non-HTML request
// without auth gets a 401 with WWW-Authenticate: Bearer.
func TestHandleDeviceLanding_NoAuth_NonHTML(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	chain := middleware.RequireAuth(deps.Auth, deps.CookieName, deps.DeviceCookieName, "/admin/login")(
		http.HandlerFunc(handleDeviceLanding(deps.Auth)),
	)

	req := httptest.NewRequest(http.MethodGet, deps.DeviceLandingURL, nil)
	// No Accept header -- treated as a non-HTML / API call.
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", got)
	}
}

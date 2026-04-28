package views

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jasoncorbett/screens/internal/auth"
)

// hexTokenPattern matches a 64-character lowercase-hex token (the device
// token shape produced by auth.GenerateToken).
var hexTokenPattern = regexp.MustCompile(`[0-9a-f]{64}`)

func TestHandleDeviceList_RendersDeviceNames(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	if _, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID); err != nil {
		t.Fatalf("create device: %v", err)
	}
	if _, _, err := deps.Auth.CreateDevice(context.Background(), "lobby-display", admin.ID); err != nil {
		t.Fatalf("create device: %v", err)
	}

	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	handler := handleDeviceList(deps.Auth)
	req := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want to contain text/html", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "kitchen-tablet") {
		t.Error("response body missing 'kitchen-tablet'")
	}
	if !strings.Contains(body, "lobby-display") {
		t.Error("response body missing 'lobby-display'")
	}
	if !strings.Contains(body, "Device Management") {
		t.Error("response body missing page title")
	}
}

func TestHandleDeviceCreate_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	form := url.Values{}
	form.Set("name", "kitchen-tablet")

	handler := handleDeviceCreate(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// In-place render -- 200 OK, NOT a redirect. A redirect would lose the
	// raw token, breaking the "shown exactly once" contract.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (handler must render in-place to show token)", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Save this token now") {
		t.Error("response body missing 'Save this token now' copy")
	}
	if !hexTokenPattern.MatchString(body) {
		t.Error("response body missing a 64-character hex token")
	}
	if !strings.Contains(body, "kitchen-tablet") {
		t.Error("response body missing device name")
	}

	// Device persisted.
	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	if devices[0].Name != "kitchen-tablet" {
		t.Errorf("device name = %q, want %q", devices[0].Name, "kitchen-tablet")
	}
}

func TestHandleDeviceCreate_RejectsInvalidName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace", input: "   "},
		{name: "tab and newline", input: "\t\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps, q := newTestDeps(t)
			admin := createTestUser(t, q, "admin@example.com", "admin")
			session := createTestSession(t, deps.Auth, admin.ID)

			ctx := auth.ContextWithUser(context.Background(), &auth.User{
				ID:    admin.ID,
				Email: admin.Email,
				Role:  auth.RoleAdmin,
			})
			ctx = auth.ContextWithSession(ctx, session)

			form := url.Values{}
			form.Set("name", tt.input)

			handler := handleDeviceCreate(deps.Auth)
			req := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
			}
			loc := rr.Header().Get("Location")
			if !strings.Contains(loc, "error=") {
				t.Errorf("Location = %q, want redirect with error param", loc)
			}

			devices, err := deps.Auth.ListDevices(context.Background())
			if err != nil {
				t.Fatalf("list devices: %v", err)
			}
			if len(devices) != 0 {
				t.Errorf("len(devices) = %d, want 0 (no device should be created on invalid name)", len(devices))
			}
		})
	}
}

func TestHandleDeviceList_DoesNotLeakPreviousToken(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	// Create a device via the handler (so the create response contains the
	// raw token in the same surface a user would see).
	form := url.Values{}
	form.Set("name", "kitchen-tablet")
	createReq := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq = createReq.WithContext(ctx)
	createRR := httptest.NewRecorder()
	handleDeviceCreate(deps.Auth).ServeHTTP(createRR, createReq)

	if createRR.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d", createRR.Code, http.StatusOK)
	}

	// Capture the raw token from the create response.
	rawToken := hexTokenPattern.FindString(createRR.Body.String())
	if rawToken == "" {
		t.Fatal("could not extract raw token from create response")
	}

	// Now hit the list page and verify it does NOT contain the previously
	// shown token.
	listReq := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	listReq = listReq.WithContext(ctx)
	listRR := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(listRR, listReq)

	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRR.Code, http.StatusOK)
	}
	if strings.Contains(listRR.Body.String(), rawToken) {
		t.Error("list page leaked the raw token that was shown on create -- the token must only ever appear in the create response")
	}
}

func TestHandleDeviceRevoke_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	dev, rawToken, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	session := createTestSession(t, deps.Auth, admin.ID)
	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	handler := handleDeviceRevoke(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/devices/"+dev.ID+"/revoke", nil)
	req.SetPathValue("id", dev.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/admin/devices?msg=revoked" {
		t.Errorf("Location = %q, want %q", loc, "/admin/devices?msg=revoked")
	}

	// ValidateDeviceToken must now return ErrDeviceRevoked.
	if _, err := deps.Auth.ValidateDeviceToken(context.Background(), rawToken); !errors.Is(err, auth.ErrDeviceRevoked) {
		t.Errorf("ValidateDeviceToken err = %v, want ErrDeviceRevoked", err)
	}
}

func TestHandleDeviceRevoke_UnknownID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	handler := handleDeviceRevoke(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/devices/unknown-id/revoke", nil)
	req.SetPathValue("id", "unknown-id")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param", loc)
	}
}

func TestHandleDeviceList_NoUserContext_Returns403(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleDeviceList(deps.Auth)
	req := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

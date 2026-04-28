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

// findCookie returns the first Set-Cookie with the given name, or nil.
func findCookie(t *testing.T, rr *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// enrollmentRequest builds a POST request carrying the admin session cookie
// and the user / session in context, matching what RequireAuth would inject.
func enrollmentRequest(t *testing.T, deps *Deps, adminUser *auth.User, sessionToken, deviceID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/devices/"+deviceID+"/enroll-browser", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: sessionToken})
	if deviceID != "" {
		req.SetPathValue("id", deviceID)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), sessionToken)
	if err != nil {
		t.Fatalf("validate admin session: %v", err)
	}
	ctx := auth.ContextWithUser(req.Context(), adminUser)
	ctx = auth.ContextWithSession(ctx, session)
	return req.WithContext(ctx)
}

// TestHandleDeviceEnrollExisting_HappyPath exercises performBrowserEnrollment
// via the public enroll-existing handler. It is the load-bearing happy-path
// test for the cookie-swap operation.
func TestHandleDeviceEnrollExisting_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	dev, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	req := enrollmentRequest(t, deps, adminUser, adminToken, dev.ID)
	rr := httptest.NewRecorder()
	handleDeviceEnrollExisting(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != deps.DeviceLandingURL {
		t.Errorf("Location = %q, want %q", loc, deps.DeviceLandingURL)
	}

	clearedSession := findCookie(t, rr, deps.CookieName)
	if clearedSession == nil {
		t.Fatal("expected Set-Cookie clearing the admin session cookie")
	}
	if clearedSession.MaxAge >= 0 {
		t.Errorf("admin session cookie MaxAge = %d, want < 0", clearedSession.MaxAge)
	}
	if !clearedSession.HttpOnly {
		t.Error("admin session cookie should be HttpOnly")
	}

	deviceCookie := findCookie(t, rr, deps.DeviceCookieName)
	if deviceCookie == nil {
		t.Fatal("expected Set-Cookie setting the device cookie")
	}
	if deviceCookie.Value == "" {
		t.Error("device cookie value should be non-empty")
	}
	if !deviceCookie.HttpOnly {
		t.Error("device cookie should be HttpOnly")
	}
	if deviceCookie.Secure != deps.SecureCookie {
		t.Errorf("device cookie Secure = %v, want %v", deviceCookie.Secure, deps.SecureCookie)
	}
	if deviceCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("device cookie SameSite = %v, want %v", deviceCookie.SameSite, http.SameSiteLaxMode)
	}
	if !hexTokenPattern.MatchString(deviceCookie.Value) {
		t.Errorf("device cookie value does not look like a hex token: %q", deviceCookie.Value)
	}

	// The new device cookie value must validate against the enrolled device.
	gotDev, err := deps.Auth.ValidateDeviceToken(context.Background(), deviceCookie.Value)
	if err != nil {
		t.Fatalf("ValidateDeviceToken on new cookie: %v", err)
	}
	if gotDev.ID != dev.ID {
		t.Errorf("device cookie validates to id %q, want %q", gotDev.ID, dev.ID)
	}

	// The admin session row must be gone.
	if _, _, err := deps.Auth.ValidateSession(context.Background(), adminToken); err == nil {
		t.Error("admin session row should be deleted after enrollment, but ValidateSession still succeeds")
	}
}

// TestHandleDeviceEnrollExisting_TwoSessionsOnePreserved verifies that only
// the session row backing the enrolling request is deleted -- other admin
// sessions for the same user (e.g., on a laptop) MUST keep working.
func TestHandleDeviceEnrollExisting_TwoSessionsOnePreserved(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	tokenA, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session A: %v", err)
	}
	tokenB, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session B: %v", err)
	}
	if tokenA == tokenB {
		t.Fatal("CreateSession returned identical tokens")
	}

	dev, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	// Enrolling browser uses session A.
	req := enrollmentRequest(t, deps, adminUser, tokenA, dev.ID)
	rr := httptest.NewRecorder()
	handleDeviceEnrollExisting(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}

	// Session A is deleted.
	if _, _, err := deps.Auth.ValidateSession(context.Background(), tokenA); err == nil {
		t.Error("session A should be deleted after enrollment")
	}
	// Session B still works.
	if _, _, err := deps.Auth.ValidateSession(context.Background(), tokenB); err != nil {
		t.Errorf("session B should remain valid; ValidateSession err = %v", err)
	}
}

// TestHandleDeviceEnrollExisting_RevokedTargetAborts verifies that a revoked
// target device aborts the swap BEFORE any cookies are touched and BEFORE the
// admin session row is deleted. Catches a regression where the helper clears
// cookies before checking the device exists.
func TestHandleDeviceEnrollExisting_RevokedTargetAborts(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	dev, _, err := deps.Auth.CreateDevice(context.Background(), "soon-revoked", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := deps.Auth.RevokeDevice(context.Background(), dev.ID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	req := enrollmentRequest(t, deps, adminUser, adminToken, dev.ID)
	rr := httptest.NewRecorder()
	handleDeviceEnrollExisting(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/devices?error=") {
		t.Errorf("Location = %q, want /admin/devices?error=...", loc)
	}
	if !strings.Contains(loc, url.QueryEscape("Device not found or revoked")) {
		t.Errorf("Location = %q, want error mentioning 'Device not found or revoked'", loc)
	}

	// Critically: NO cookie mutations.
	if c := findCookie(t, rr, deps.CookieName); c != nil {
		t.Errorf("admin session cookie was mutated despite failed enrollment: %+v", c)
	}
	if c := findCookie(t, rr, deps.DeviceCookieName); c != nil {
		t.Errorf("device cookie was set despite failed enrollment: %+v", c)
	}

	// Admin session row must still validate.
	if _, _, err := deps.Auth.ValidateSession(context.Background(), adminToken); err != nil {
		t.Errorf("admin session row should NOT be deleted on failed enrollment; err = %v", err)
	}
}

// TestHandleDeviceEnrollExisting_EmptyPathID verifies that the handler
// short-circuits when the path id is empty rather than calling into the auth
// layer with "".
func TestHandleDeviceEnrollExisting_EmptyPathID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/devices//enroll-browser", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	req.SetPathValue("id", "")
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	ctx := auth.ContextWithUser(req.Context(), adminUser)
	ctx = auth.ContextWithSession(ctx, session)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handleDeviceEnrollExisting(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/admin/devices?error=Missing+device+ID" {
		t.Errorf("Location = %q, want %q", loc, "/admin/devices?error=Missing+device+ID")
	}
	if c := findCookie(t, rr, deps.CookieName); c != nil {
		t.Errorf("admin session cookie was mutated on empty id path: %+v", c)
	}
	if c := findCookie(t, rr, deps.DeviceCookieName); c != nil {
		t.Errorf("device cookie was set on empty id path: %+v", c)
	}
	if _, _, err := deps.Auth.ValidateSession(context.Background(), adminToken); err != nil {
		t.Errorf("admin session row should NOT be deleted on empty-id path; err = %v", err)
	}
}

// TestHandleDeviceEnrollNew_HappyPath verifies the create-and-enroll combined
// flow.
func TestHandleDeviceEnrollNew_HappyPath(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	form := url.Values{}
	form.Set("name", "kitchen-tablet")

	req := httptest.NewRequest(http.MethodPost, "/admin/devices/enroll-new-browser", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	ctx := auth.ContextWithUser(req.Context(), adminUser)
	ctx = auth.ContextWithSession(ctx, session)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handleDeviceEnrollNew(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != deps.DeviceLandingURL {
		t.Errorf("Location = %q, want %q", loc, deps.DeviceLandingURL)
	}

	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	if devices[0].Name != "kitchen-tablet" {
		t.Errorf("device name = %q, want kitchen-tablet", devices[0].Name)
	}

	deviceCookie := findCookie(t, rr, deps.DeviceCookieName)
	if deviceCookie == nil {
		t.Fatal("expected Set-Cookie setting the device cookie")
	}
	gotDev, err := deps.Auth.ValidateDeviceToken(context.Background(), deviceCookie.Value)
	if err != nil {
		t.Fatalf("ValidateDeviceToken on new cookie: %v", err)
	}
	if gotDev.ID != devices[0].ID {
		t.Errorf("cookie validates to %q, want %q", gotDev.ID, devices[0].ID)
	}
}

// TestHandleDeviceEnrollNew_EmptyName verifies the handler rejects whitespace-
// only names without creating any device row.
func TestHandleDeviceEnrollNew_EmptyName(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	form := url.Values{}
	form.Set("name", "   ")

	req := httptest.NewRequest(http.MethodPost, "/admin/devices/enroll-new-browser", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	ctx := auth.ContextWithUser(req.Context(), adminUser)
	ctx = auth.ContextWithSession(ctx, session)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handleDeviceEnrollNew(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	if loc := rr.Header().Get("Location"); loc != "/admin/devices?error=Name+is+required" {
		t.Errorf("Location = %q, want /admin/devices?error=Name+is+required", loc)
	}

	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("len(devices) = %d, want 0", len(devices))
	}
	if c := findCookie(t, rr, deps.CookieName); c != nil {
		t.Errorf("admin cookie mutated despite empty-name rejection: %+v", c)
	}
	if c := findCookie(t, rr, deps.DeviceCookieName); c != nil {
		t.Errorf("device cookie set despite empty-name rejection: %+v", c)
	}
	if _, _, err := deps.Auth.ValidateSession(context.Background(), adminToken); err != nil {
		t.Errorf("admin session row should not be deleted on empty-name path; err = %v", err)
	}
}

// TestHandleDeviceEnrollExisting_DeviceCookieReplacement verifies that a
// browser arriving with an OLD device cookie ends up with the NEW token in
// its device cookie -- the Set-Cookie must overwrite the prior value.
func TestHandleDeviceEnrollExisting_DeviceCookieReplacement(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	// Two devices: the browser is currently "device-old" (via cookie) and
	// will be enrolled as "device-new".
	oldDev, oldRawToken, err := deps.Auth.CreateDevice(context.Background(), "device-old", admin.ID)
	if err != nil {
		t.Fatalf("create old device: %v", err)
	}
	newDev, _, err := deps.Auth.CreateDevice(context.Background(), "device-new", admin.ID)
	if err != nil {
		t.Fatalf("create new device: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/devices/"+newDev.ID+"/enroll-browser", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	req.AddCookie(&http.Cookie{Name: deps.DeviceCookieName, Value: oldRawToken})
	req.SetPathValue("id", newDev.ID)
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	ctx := auth.ContextWithUser(req.Context(), adminUser)
	ctx = auth.ContextWithSession(ctx, session)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handleDeviceEnrollExisting(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}

	deviceCookie := findCookie(t, rr, deps.DeviceCookieName)
	if deviceCookie == nil {
		t.Fatal("expected Set-Cookie setting the device cookie")
	}
	if deviceCookie.Value == oldRawToken {
		t.Error("device cookie still carries the OLD token; expected the freshly minted token for the new device")
	}

	gotDev, err := deps.Auth.ValidateDeviceToken(context.Background(), deviceCookie.Value)
	if err != nil {
		t.Fatalf("ValidateDeviceToken on new cookie: %v", err)
	}
	if gotDev.ID != newDev.ID {
		t.Errorf("new device cookie validates to %q, want %q", gotDev.ID, newDev.ID)
	}
	// The old device is untouched (still its own row; its token is still valid
	// because we only rotated the new device).
	_ = oldDev
	if _, err := deps.Auth.ValidateDeviceToken(context.Background(), oldRawToken); err != nil {
		t.Errorf("old device's token should still be valid (we rotated the new device, not the old); err = %v", err)
	}
}

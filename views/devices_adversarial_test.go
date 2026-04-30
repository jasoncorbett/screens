package views

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasoncorbett/screens/internal/auth"
)

// TestDeviceMgmt_MemberCannotAccessDevicesPage verifies the role-check on
// /admin/devices is wired through the full middleware chain. A regression in
// the chain (e.g., forgetting to wrap the deviceMux in RequireRole(RoleAdmin))
// would silently allow members to view the device fleet.
func TestDeviceMgmt_MemberCannotAccessDevicesPage(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	member := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/devices", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/devices: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (member must be 403 on /admin/devices)", resp.StatusCode, http.StatusForbidden)
	}
}

// TestDeviceMgmt_MemberCannotPostCreate verifies the role-check applies to
// POST /admin/devices through the full chain. Catches a regression where
// somebody adds a new POST handler outside the RequireRole-wrapped sub-mux.
func TestDeviceMgmt_MemberCannotPostCreate(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	member := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)
	form.Set("name", "evil-device")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/devices: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (member POST /admin/devices must be 403)", resp.StatusCode, http.StatusForbidden)
	}

	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("expected no devices created by rejected member, got %d", len(devices))
	}
}

// TestDeviceMgmt_MemberCannotPostRevoke verifies the role-check applies to
// the parametric revoke route as well.
func TestDeviceMgmt_MemberCannotPostRevoke(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "victim-device", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	member := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices/"+dev.ID+"/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST revoke: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (member POST revoke must be 403)", resp.StatusCode, http.StatusForbidden)
	}

	// Device should still be active.
	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	for _, d := range devices {
		if d.ID == dev.ID && d.IsRevoked() {
			t.Error("device should not be revoked after member's rejected revoke")
		}
	}
}

// TestDeviceMgmt_AdminCreateThroughChainSucceeds verifies the happy path for
// admin device creation works end-to-end through the full middleware stack
// (auth + csrf + role).
func TestDeviceMgmt_AdminCreateThroughChainSucceeds(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)
	form.Set("name", "kitchen-tablet")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/devices: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d (admin should create device through full chain)", resp.StatusCode, http.StatusOK)
	}

	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 {
		t.Errorf("len(devices) = %d, want 1 after admin POST", len(devices))
	}
}

// TestDeviceMgmt_CreateWithoutCSRF_Returns403 verifies the CSRF middleware
// rejects POST /admin/devices without a _csrf field, even from an admin.
func TestDeviceMgmt_CreateWithoutCSRF_Returns403(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	form := url.Values{}
	form.Set("name", "no-csrf-device")
	// Note: no _csrf field

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/devices: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (missing CSRF should reject create)", resp.StatusCode, http.StatusForbidden)
	}

	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("expected no device created without CSRF, got %d", len(devices))
	}
}

// TestDeviceMgmt_RevokeWithoutCSRF_Returns403 verifies the CSRF middleware
// rejects POST /admin/devices/{id}/revoke without a _csrf field.
func TestDeviceMgmt_RevokeWithoutCSRF_Returns403(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	dev, rawDeviceToken, err := deps.Auth.CreateDevice(context.Background(), "victim", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices/"+dev.ID+"/revoke", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST revoke: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (missing CSRF must reject revoke)", resp.StatusCode, http.StatusForbidden)
	}

	// Device must still be valid.
	if _, err := deps.Auth.ValidateDeviceToken(context.Background(), rawDeviceToken); err != nil {
		t.Errorf("device should still be valid after rejected revoke, ValidateDeviceToken err = %v", err)
	}
}

// TestDeviceMgmt_UnauthenticatedHTML_RedirectsToLogin verifies the
// RequireAuth middleware redirects unauthenticated GET /admin/devices with
// Accept: text/html to /admin/login.
func TestDeviceMgmt_UnauthenticatedHTML_RedirectsToLogin(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDepsWithGoogle(t)

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/devices", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/devices: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want %d (unauthenticated HTML must redirect)", resp.StatusCode, http.StatusFound)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/admin/login") {
		t.Errorf("Location = %q, want redirect to /admin/login", loc)
	}
}

// TestDeviceMgmt_UnauthenticatedAPI_Returns401 verifies that a non-HTML
// request to /admin/devices (no Accept header, e.g. an API caller) gets a
// 401 with the WWW-Authenticate: Bearer challenge instead of the HTML
// redirect.
func TestDeviceMgmt_UnauthenticatedAPI_Returns401(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDepsWithGoogle(t)

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// POST without Accept text/html simulates an API caller.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices", strings.NewReader(""))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/devices: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (unauthenticated API must 401)", resp.StatusCode, http.StatusUnauthorized)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", got)
	}
}

// TestDeviceMgmt_TokenNotInSlogOutput captures the global slog output during
// CreateDevice and asserts the freshly minted raw token does NOT appear in
// any structured log line. The "shown exactly once" contract is meaningless
// if the token also surfaces in the operator's log files.
func TestDeviceMgmt_TokenNotInSlogOutput(t *testing.T) {
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	session := createTestSession(t, deps.Auth, admin.ID)
	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	// Swap the default slog handler with a buffer-backed JSON handler so
	// we can grep its output for the raw token.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	form := url.Values{}
	form.Set("name", "log-leak-check")
	req := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceCreate(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("create status = %d, want %d", rr.Code, http.StatusOK)
	}
	rawToken := hexTokenPattern.FindString(rr.Body.String())
	if rawToken == "" {
		t.Fatal("could not capture raw token from create response")
	}

	if strings.Contains(buf.String(), rawToken) {
		t.Errorf("raw device token leaked into slog output:\n%s", buf.String())
	}
}

// TestDeviceMgmt_TokenNotInResponseHeaders verifies the raw token from a
// successful create is NOT present in any response header (Set-Cookie,
// Location, custom headers). It must live only in the body.
func TestDeviceMgmt_TokenNotInResponseHeaders(t *testing.T) {
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
	form.Set("name", "header-leak-check")
	req := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceCreate(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	rawToken := hexTokenPattern.FindString(rr.Body.String())
	if rawToken == "" {
		t.Fatal("could not extract token from response body")
	}

	for k, vs := range rr.Header() {
		for _, v := range vs {
			if strings.Contains(v, rawToken) {
				t.Errorf("raw token leaked into response header %q: %q", k, v)
			}
		}
	}
}

// TestDeviceMgmt_DeviceNameXSSEscaped renders a device with HTML-metacharacter
// content in the name and verifies the templ auto-escape is intact -- a
// regression to templ.Raw or unsafe string concatenation would let a malicious
// admin (or a compromised admin form) inject scripts into the page.
func TestDeviceMgmt_DeviceNameXSSEscaped(t *testing.T) {
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

	xssName := `<script>alert('xss')</script>`
	if _, _, err := deps.Auth.CreateDevice(context.Background(), xssName, admin.ID); err != nil {
		t.Fatalf("create device: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if strings.Contains(body, xssName) {
		t.Error("XSS: literal <script> tag from device name found in response body")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected device name with HTML metacharacters to be HTML-escaped in response body")
	}
}

// TestDeviceMgmt_FlashErrorXSSEscaped verifies the ?error= flash query param
// is HTML-escaped when rendered. An attacker could craft a /admin/devices?error=<script>...
// link and rely on browsers auto-loading it via redirect; we ensure templ
// escapes it.
func TestDeviceMgmt_FlashErrorXSSEscaped(t *testing.T) {
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

	xss := `<script>alert('xss')</script>`
	req := httptest.NewRequest(http.MethodGet, "/admin/devices?error="+url.QueryEscape(xss), nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if strings.Contains(body, xss) {
		t.Error("XSS: unescaped error query param in response body")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected escaped error message in response body")
	}
}

// TestDeviceMgmt_FlashMsgIsAllowlisted verifies that an attacker-supplied
// ?msg= value is NOT rendered verbatim. The handler maps a small set of
// known msg values ("revoked") to friendly strings; anything else must be
// dropped (no flash banner) so that arbitrary text cannot be reflected even
// if it happens to be HTML-safe today.
func TestDeviceMgmt_FlashMsgIsAllowlisted(t *testing.T) {
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

	bogusMsg := "totally-bogus-msg-key-xyz"
	req := httptest.NewRequest(http.MethodGet, "/admin/devices?msg="+url.QueryEscape(bogusMsg), nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if strings.Contains(rr.Body.String(), bogusMsg) {
		t.Error("unmapped ?msg= value reflected verbatim into response body; handler should allowlist known keys only")
	}
}

// TestDeviceMgmt_RevokedDeviceShowsInRevokedSection verifies the AC-12 UI
// half: after revoke, a subsequent GET shows the device under the "Revoked
// Devices" section and NOT in the Active table.
func TestDeviceMgmt_RevokedDeviceShowsInRevokedSection(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "soon-revoked", admin.ID)
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

	// Revoke via the handler.
	revokeReq := httptest.NewRequest(http.MethodPost, "/admin/devices/"+dev.ID+"/revoke", nil)
	revokeReq.SetPathValue("id", dev.ID)
	revokeReq = revokeReq.WithContext(ctx)
	revokeRR := httptest.NewRecorder()
	handleDeviceRevoke(deps.Auth).ServeHTTP(revokeRR, revokeReq)
	if revokeRR.Code != http.StatusFound {
		t.Fatalf("revoke status = %d, want %d", revokeRR.Code, http.StatusFound)
	}

	// Fetch the list and inspect.
	listReq := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	listReq = listReq.WithContext(ctx)
	listRR := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRR.Code, http.StatusOK)
	}

	body := listRR.Body.String()
	if !strings.Contains(body, "Revoked Devices") {
		t.Error("expected Revoked Devices section to appear after a revoke")
	}
	if !strings.Contains(body, dev.ID) {
		t.Errorf("expected revoked device id %q to appear in the response body", dev.ID)
	}
	// Active table for that device must not contain a revoke form.
	if strings.Contains(body, `action="/admin/devices/`+dev.ID+`/revoke"`) {
		t.Error("revoked device must not appear with an active-row revoke form")
	}
}

// TestDeviceMgmt_RevokeFormActionUsesCorrectID verifies the rendered revoke
// form's action attribute carries the actual device id, not a placeholder
// or a different device's id. Catches a regression where the templ uses
// an outer-scope variable instead of the loop variable.
func TestDeviceMgmt_RevokeFormActionUsesCorrectID(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	dev1, _, err := deps.Auth.CreateDevice(context.Background(), "first", admin.ID)
	if err != nil {
		t.Fatalf("create device 1: %v", err)
	}
	dev2, _, err := deps.Auth.CreateDevice(context.Background(), "second", admin.ID)
	if err != nil {
		t.Fatalf("create device 2: %v", err)
	}

	session := createTestSession(t, deps.Auth, admin.ID)
	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	req := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `action="/admin/devices/`+dev1.ID+`/revoke"`) {
		t.Errorf("missing revoke form for device 1 (id=%q) in response body", dev1.ID)
	}
	if !strings.Contains(body, `action="/admin/devices/`+dev2.ID+`/revoke"`) {
		t.Errorf("missing revoke form for device 2 (id=%q) in response body", dev2.ID)
	}
}

// TestDeviceMgmt_RevokedDeviceCannotBeRevokedAgain verifies a previously
// revoked device's row is detected; the handler must not advertise success
// (msg=revoked) on the second attempt.
//
// Behaviour rationale: showing "Device revoked" twice is misleading -- the
// second click was a no-op. The fix lifts the "already revoked" detection
// into the auth layer: RevokeDevice returns ErrDeviceNotFound when the row
// is present but already revoked, so the handler routes to the same flash
// the spec calls for ("Device not found").
func TestDeviceMgmt_RevokedDeviceCannotBeRevokedAgain(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "double-revoke", admin.ID)
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

	doRevoke := func() *httptest.ResponseRecorder {
		rrr := httptest.NewRequest(http.MethodPost, "/admin/devices/"+dev.ID+"/revoke", nil)
		rrr.SetPathValue("id", dev.ID)
		rrr = rrr.WithContext(ctx)
		out := httptest.NewRecorder()
		handleDeviceRevoke(deps.Auth).ServeHTTP(out, rrr)
		return out
	}

	first := doRevoke()
	if first.Code != http.StatusFound {
		t.Fatalf("first revoke status = %d, want %d", first.Code, http.StatusFound)
	}
	if loc := first.Header().Get("Location"); !strings.Contains(loc, "msg=revoked") {
		t.Errorf("first revoke Location = %q, want msg=revoked", loc)
	}

	second := doRevoke()
	if second.Code != http.StatusFound {
		t.Fatalf("second revoke status = %d, want %d", second.Code, http.StatusFound)
	}
	loc := second.Header().Get("Location")
	if strings.Contains(loc, "msg=revoked") {
		t.Errorf("second revoke falsely reports msg=revoked: %q", loc)
	}
	if !strings.Contains(loc, "error=") {
		t.Errorf("second revoke Location = %q, want error param indicating the device was already revoked", loc)
	}
}

// TestDeviceMgmt_RevokeMissingPathID verifies that a request whose path id
// portion is empty (e.g. via SetPathValue("id", "")) is redirected with an
// error rather than passing an empty id to the auth layer.
func TestDeviceMgmt_RevokeMissingPathID(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPost, "/admin/devices//revoke", nil)
	req.SetPathValue("id", "")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceRevoke(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param for missing id", loc)
	}
}

// TestDeviceMgmt_LongDeviceNameNoCrash verifies the handler does not panic
// or 5xx on a 1MB device name. Whatever the auth layer does (accept or
// reject due to db constraints), the handler must surface a clean response.
func TestDeviceMgmt_LongDeviceNameNoCrash(t *testing.T) {
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

	long := strings.Repeat("a", 1024*1024) // 1MB
	form := url.Values{}
	form.Set("name", long)
	req := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceCreate(deps.Auth).ServeHTTP(rr, req)

	// Either OK (created and rendered) or Found (rejected via redirect)
	// is acceptable; what is NOT acceptable is a 500 / panic.
	if rr.Code != http.StatusOK && rr.Code != http.StatusFound {
		t.Errorf("status = %d, want 200 or 302 (handler should not crash on long name)", rr.Code)
	}
}

// TestDeviceMgmt_UnicodeDeviceName verifies non-ASCII device names round-trip
// cleanly through the handler -- emoji, RTL, accented chars all need to
// survive form decoding, db storage, and HTML rendering.
func TestDeviceMgmt_UnicodeDeviceName(t *testing.T) {
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

	tests := []struct {
		label string
		name  string
	}{
		{"emoji", "kitchen 🍳 tablet"},
		{"rtl", "شاشة"},
		{"accented", "café-écran"},
		{"cjk", "看板"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			form := url.Values{}
			form.Set("name", tt.name)
			req := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleDeviceCreate(deps.Auth).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if !strings.Contains(rr.Body.String(), tt.name) {
				t.Errorf("response body missing unicode device name %q", tt.name)
			}
		})
	}
}

// TestDeviceMgmt_ConcurrentCreatesUniqueTokens fires N concurrent creates
// from the same admin and verifies every response yielded a distinct token
// and every device persisted with a distinct id. Catches accidental shared
// state in the create handler (a regression where rawToken or dev outlived
// its function-local scope).
func TestDeviceMgmt_ConcurrentCreatesUniqueTokens(t *testing.T) {
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	session := createTestSession(t, deps.Auth, admin.ID)
	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	const n = 20
	tokens := make([]string, n)
	var wg sync.WaitGroup
	var failures int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			form := url.Values{}
			form.Set("name", "concurrent-device")
			req := httptest.NewRequest(http.MethodPost, "/admin/devices", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			handleDeviceCreate(deps.Auth).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				atomic.AddInt32(&failures, 1)
				return
			}
			tokens[i] = hexTokenPattern.FindString(rr.Body.String())
		}(i)
	}
	wg.Wait()

	if failures > 0 {
		t.Fatalf("%d/%d concurrent creates returned non-200", failures, n)
	}

	seen := make(map[string]struct{}, n)
	for i, tok := range tokens {
		if tok == "" {
			t.Errorf("create #%d: no token in response body", i)
			continue
		}
		if _, dup := seen[tok]; dup {
			t.Errorf("duplicate token across concurrent creates: %s", tok)
		}
		seen[tok] = struct{}{}
	}
	if len(seen) != n {
		t.Errorf("unique tokens = %d, want %d", len(seen), n)
	}

	devices, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != n {
		t.Errorf("len(devices) = %d, want %d", len(devices), n)
	}
}

// TestDeviceMgmt_ListEmptyRendersCleanly verifies the page renders sensibly
// when no devices exist -- the empty-state should not crash the templ
// (e.g. a nil-deref on devices[0]) and should not show the Revoked Devices
// section.
func TestDeviceMgmt_ListEmptyRendersCleanly(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Active Devices") {
		t.Error("expected Active Devices heading even when empty")
	}
	if strings.Contains(body, "Revoked Devices") {
		t.Error("Revoked Devices section should be hidden when no revoked devices exist")
	}
}

// TestDeviceMgmt_ListMany verifies the page renders cleanly with a hundred
// devices. Catches accidental quadratic logic or buffer overflows in the
// templ render for non-trivial fleet sizes.
func TestDeviceMgmt_ListMany(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	for i := 0; i < 120; i++ {
		if _, _, err := deps.Auth.CreateDevice(context.Background(), "device", admin.ID); err != nil {
			t.Fatalf("create device #%d: %v", i, err)
		}
	}

	session := createTestSession(t, deps.Auth, admin.ID)
	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	req := httptest.NewRequest(http.MethodGet, "/admin/devices", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handleDeviceList(deps.Auth).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	// 120 distinct revoke form actions should render.
	if got := strings.Count(rr.Body.String(), `/revoke"`); got < 120 {
		t.Errorf("revoke form count = %d, want >= 120", got)
	}
}

// TestDeviceMgmt_AdminListThroughChainSucceeds verifies an admin can GET
// /admin/devices through the full middleware stack and the page renders
// cleanly with the admin's existing devices.
func TestDeviceMgmt_AdminListThroughChainSucceeds(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	if _, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID); err != nil {
		t.Fatalf("create device: %v", err)
	}
	rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/devices", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/devices: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d (admin GET /admin/devices through chain)", resp.StatusCode, http.StatusOK)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(buf.String(), "kitchen-tablet") {
		t.Error("response body missing existing device name")
	}
}

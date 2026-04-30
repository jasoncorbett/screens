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

// adversarialClient builds an *http.Client that returns the raw response
// instead of following redirects, the way every browser-enrollment test wants
// to inspect Set-Cookie + Location headers without chasing the 302.
func adversarialClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// hasSetCookie reports whether resp's headers contain a Set-Cookie with
// the given name (regardless of value or whether it's a clear vs. set).
func hasSetCookie(resp *http.Response, name string) bool {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return true
		}
	}
	return false
}

// findSetCookie returns the first Set-Cookie with the given name on resp, or
// nil. Used when the test wants to inspect attributes rather than just
// presence.
func findSetCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestEnrollChain_AdminPostHappyPath_ThroughFullMiddlewareChain exercises the
// real route registration end-to-end with httptest.NewServer. This catches
// regressions that pure-handler tests miss -- e.g., a future refactor moves
// the enroll routes outside the admin sub-mux and direct-handler tests still
// pass while the integrated route is broken.
func TestEnrollChain_AdminPostHappyPath_ThroughFullMiddlewareChain(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices/"+dev.ID+"/enroll-browser", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})

	resp, err := adversarialClient().Do(req)
	if err != nil {
		t.Fatalf("POST enroll-browser: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if loc := resp.Header.Get("Location"); loc != deps.DeviceLandingURL {
		t.Errorf("Location = %q, want %q", loc, deps.DeviceLandingURL)
	}
	if !hasSetCookie(resp, deps.DeviceCookieName) {
		t.Error("missing Set-Cookie for device cookie -- the swap did not complete through the chain")
	}
	cleared := findSetCookie(resp, deps.CookieName)
	if cleared == nil {
		t.Fatal("missing Set-Cookie clearing the admin session cookie")
	}
	if cleared.MaxAge >= 0 {
		t.Errorf("admin cookie MaxAge = %d, want < 0", cleared.MaxAge)
	}

	// Admin session row must be gone.
	if _, _, err := deps.Auth.ValidateSession(context.Background(), adminToken); err == nil {
		t.Error("admin session row should be deleted; ValidateSession still succeeds")
	}
}

// TestEnrollChain_GetReturns405_NoCookieMutation verifies GET against either
// enroll route is rejected at the mux layer (405) without invoking the
// handler. Crucially the admin's session row MUST remain intact and no
// device cookie may be set. AC-34.
func TestEnrollChain_GetReturns405_NoCookieMutation(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Per AC-34, a GET MUST not trigger enrollment. The mux returns 405 for
	// GET against a route registered as POST. PUT/DELETE go through the
	// CSRF middleware first (state-changing methods are CSRF-checked) and
	// are rejected at 403; either status is acceptable as long as no
	// cookies are mutated and the admin session row remains.
	tests := []struct {
		name, method, path string
		wantStatuses       []int
	}{
		{"get-existing", http.MethodGet, "/admin/devices/" + dev.ID + "/enroll-browser", []int{http.StatusMethodNotAllowed}},
		{"get-new", http.MethodGet, "/admin/devices/enroll-new-browser", []int{http.StatusMethodNotAllowed}},
		{"put-existing", http.MethodPut, "/admin/devices/" + dev.ID + "/enroll-browser", []int{http.StatusMethodNotAllowed, http.StatusForbidden}},
		{"delete-new", http.MethodDelete, "/admin/devices/enroll-new-browser", []int{http.StatusMethodNotAllowed, http.StatusForbidden}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})

			resp, err := adversarialClient().Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", tt.method, tt.path, err)
			}
			defer resp.Body.Close()

			ok := false
			for _, s := range tt.wantStatuses {
				if resp.StatusCode == s {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("status = %d, want one of %v", resp.StatusCode, tt.wantStatuses)
			}
			if hasSetCookie(resp, deps.DeviceCookieName) {
				t.Error("device cookie was set despite a non-POST method")
			}
			// With a valid admin cookie present, RequireAuth does NOT clear
			// the session cookie -- so any clear indicates the helper ran,
			// which must not happen on a non-POST request.
			if c := findSetCookie(resp, deps.CookieName); c != nil && c.MaxAge < 0 && c.Value == "" {
				t.Errorf("admin cookie was cleared on non-POST request: %+v", c)
			}

			if _, _, err := deps.Auth.ValidateSession(context.Background(), adminToken); err != nil {
				t.Errorf("admin session row should NOT be deleted on non-POST; err = %v", err)
			}
		})
	}
}

// TestEnrollChain_PostWithoutCSRF_403_NoCookieMutation verifies the CSRF
// middleware rejects a POST to either enroll route without the _csrf field,
// even from a valid admin session. AC-35.
func TestEnrollChain_PostWithoutCSRF_403_NoCookieMutation(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []string{
		"/admin/devices/" + dev.ID + "/enroll-browser",
		"/admin/devices/enroll-new-browser",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			form := url.Values{}
			form.Set("name", "should-be-rejected")
			// Note: NO _csrf field.
			req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})

			resp, err := adversarialClient().Do(req)
			if err != nil {
				t.Fatalf("POST %s: %v", path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (CSRF must reject)", resp.StatusCode)
			}
			if hasSetCookie(resp, deps.DeviceCookieName) {
				t.Error("device cookie was set despite missing CSRF")
			}
			if _, _, err := deps.Auth.ValidateSession(context.Background(), adminToken); err != nil {
				t.Errorf("admin session row should NOT be deleted on CSRF rejection; err = %v", err)
			}
		})
	}

	// And no device row was created by enroll-new-browser without CSRF.
	devs, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	for _, d := range devs {
		if d.Name == "should-be-rejected" {
			t.Error("device was created by enroll-new-browser despite CSRF rejection")
		}
	}
}

// TestEnrollChain_MemberPost_403_NoCookieMutation verifies the role check
// rejects a member-authenticated POST to either enroll route. AC-33.
func TestEnrollChain_MemberPost_403_NoCookieMutation(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "victim", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	member := createTestUser(t, q, "member@example.com", "member")
	memberToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create member session: %v", err)
	}
	_, memberSession, err := deps.Auth.ValidateSession(context.Background(), memberToken)
	if err != nil {
		t.Fatalf("validate member session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []struct {
		name string
		path string
		form url.Values
	}{
		{
			name: "enroll-existing",
			path: "/admin/devices/" + dev.ID + "/enroll-browser",
			form: url.Values{"_csrf": {memberSession.CSRFToken}},
		},
		{
			name: "enroll-new",
			path: "/admin/devices/enroll-new-browser",
			form: url.Values{"_csrf": {memberSession.CSRFToken}, "name": {"member-cant-create"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, srv.URL+tt.path, strings.NewReader(tt.form.Encode()))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: memberToken})

			resp, err := adversarialClient().Do(req)
			if err != nil {
				t.Fatalf("POST %s: %v", tt.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (member must not enroll)", resp.StatusCode)
			}
			if hasSetCookie(resp, deps.DeviceCookieName) {
				t.Error("device cookie was set despite member-rejected enrollment")
			}
			if _, _, err := deps.Auth.ValidateSession(context.Background(), memberToken); err != nil {
				t.Errorf("member session row should NOT be deleted on role rejection; err = %v", err)
			}
		})
	}

	// No new device row was created by enroll-new-browser.
	devs, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	for _, d := range devs {
		if d.Name == "member-cant-create" {
			t.Error("device was created by member-rejected enroll-new-browser")
		}
	}
}

// TestEnrollChain_UnauthenticatedPost_NoCookieMutation_NoSessionDeletion
// verifies that an unauthenticated POST does not invoke the helper at all --
// no token rotation, no device cookie set, and (since there is no admin
// session to delete) the sessions table is unchanged. AC-32.
func TestEnrollChain_UnauthenticatedPost_NoCookieMutation_NoSessionDeletion(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	// A pre-existing admin session belonging to SOMEONE ELSE that must
	// remain intact when an unauthenticated client tries to enroll.
	other := createTestUser(t, q, "other@example.com", "admin")
	otherToken, err := deps.Auth.CreateSession(context.Background(), other.ID)
	if err != nil {
		t.Fatalf("create other session: %v", err)
	}
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", other.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices/"+dev.ID+"/enroll-browser", strings.NewReader(""))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// No cookie -- truly unauthenticated.

	resp, err := adversarialClient().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (unauthenticated POST)", resp.StatusCode)
	}
	if hasSetCookie(resp, deps.DeviceCookieName) {
		t.Error("device cookie was set on unauthenticated request")
	}
	// The other admin's session must still be valid -- a wholly unrelated
	// caller cannot trigger session deletion.
	if _, _, err := deps.Auth.ValidateSession(context.Background(), otherToken); err != nil {
		t.Errorf("unrelated admin session was affected by unauthenticated enroll attempt: %v", err)
	}
}

// TestEnrollChain_LiteralBeatsWildcard verifies Go's ServeMux precedence:
// POST /admin/devices/enroll-new-browser routes to handleDeviceEnrollNew
// (creates a new device named per the form), while POST
// /admin/devices/<some-uuid>/enroll-browser routes to handleDeviceEnrollExisting
// (does NOT create a new device).
//
// A regression where the wildcard captured the literal would silently break
// the enroll-new flow OR (worse) treat "enroll-new-browser" as a device id
// and rotate a non-existent device's token.
func TestEnrollChain_LiteralBeatsWildcard(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)
	form.Set("name", "literal-routed-correctly")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices/enroll-new-browser", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})

	resp, err := adversarialClient().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (literal route should succeed)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != deps.DeviceLandingURL {
		t.Errorf("Location = %q, want %q", loc, deps.DeviceLandingURL)
	}

	// A new device must have been created -- proving the literal handler
	// ran, NOT the wildcard handler (which would have tried to rotate a
	// device with id="enroll-new-browser" and failed).
	devs, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	found := false
	for _, d := range devs {
		if d.Name == "literal-routed-correctly" {
			found = true
			break
		}
	}
	if !found {
		t.Error("literal route did not create the expected device -- wildcard may have swallowed the request")
	}
}

// TestEnrollChain_TokenNotInResponseHeaders verifies the freshly minted
// device token appears ONLY in the device cookie's Set-Cookie value -- not
// in the Location header, not in any other header, not in the response body.
func TestEnrollChain_TokenNotInResponseHeaders(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
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
	deviceCookie := findCookie(t, rr, deps.DeviceCookieName)
	if deviceCookie == nil {
		t.Fatal("missing device cookie")
	}
	rawToken := deviceCookie.Value

	// Location header MUST NOT contain the raw token.
	if loc := rr.Header().Get("Location"); strings.Contains(loc, rawToken) {
		t.Errorf("raw token leaked into Location header: %q", loc)
	}

	// No header other than Set-Cookie may carry the raw token. (Set-Cookie
	// for the device cookie is the legitimate channel; anything else is a
	// leak.)
	for k, vs := range rr.Header() {
		if strings.EqualFold(k, "Set-Cookie") {
			continue // legitimate channel for the device cookie
		}
		for _, v := range vs {
			if strings.Contains(v, rawToken) {
				t.Errorf("raw token leaked into header %q: %q", k, v)
			}
		}
	}

	// Response body MUST NOT contain the raw token (a 302 typically has an
	// empty body but http.Redirect may write an HTML stub).
	if strings.Contains(rr.Body.String(), rawToken) {
		t.Errorf("raw token leaked into response body: %q", rr.Body.String())
	}
}

// TestEnrollChain_TokenNotInSlogOutput captures slog output across the
// enrollment helper and asserts the freshly minted device token does not
// appear in any structured log line. The helper logs the audit message
// without the token, but a sloppy refactor (e.g., adding "raw_token"
// attribute for debugging) would silently regress this contract.
func TestEnrollChain_TokenNotInSlogOutput(t *testing.T) {
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	req := enrollmentRequest(t, deps, adminUser, adminToken, dev.ID)
	rr := httptest.NewRecorder()
	handleDeviceEnrollExisting(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	deviceCookie := findCookie(t, rr, deps.DeviceCookieName)
	if deviceCookie == nil {
		t.Fatal("missing device cookie")
	}
	rawToken := deviceCookie.Value

	if strings.Contains(buf.String(), rawToken) {
		t.Errorf("raw device token leaked into slog output:\n%s", buf.String())
	}
}

// TestEnrollChain_ConcurrentEnrollmentsForSameDevice fires N concurrent
// enrollment requests for the same target device and verifies (a) at most one
// device cookie is "the winner" (its raw token validates against the device);
// (b) RotateDeviceToken's UPDATE is serialised by the underlying SQLite layer
// without panicking; (c) the device's hash matches exactly one of the
// returned tokens after the dust settles.
//
// Catches a regression where concurrent calls to the helper interleave the
// "rotate then read" pattern and end up with a database row pointing at one
// token while a different token is returned to the caller (silent auth
// breakage on the kiosk).
func TestEnrollChain_ConcurrentEnrollmentsForSameDevice(t *testing.T) {
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}

	dev, _, err := deps.Auth.CreateDevice(context.Background(), "race-target", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	const n = 10
	tokens := make([]string, n)
	var wg sync.WaitGroup
	var failures int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine needs its own admin session so we don't trip
			// on Logout deleting a row a sibling goroutine is still using.
			rawToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
			if err != nil {
				atomic.AddInt32(&failures, 1)
				return
			}
			req := enrollmentRequest(t, deps, adminUser, rawToken, dev.ID)
			rr := httptest.NewRecorder()
			handleDeviceEnrollExisting(deps).ServeHTTP(rr, req)
			if rr.Code != http.StatusFound {
				atomic.AddInt32(&failures, 1)
				return
			}
			c := findCookie(t, rr, deps.DeviceCookieName)
			if c == nil {
				atomic.AddInt32(&failures, 1)
				return
			}
			tokens[i] = c.Value
		}(i)
	}
	wg.Wait()

	if failures > 0 {
		t.Fatalf("%d/%d concurrent enrollments failed", failures, n)
	}

	// Exactly one of the returned tokens must validate -- the last writer
	// wins at the SQLite level.
	validCount := 0
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		gotDev, err := deps.Auth.ValidateDeviceToken(context.Background(), tok)
		if err == nil && gotDev.ID == dev.ID {
			validCount++
		}
	}
	if validCount != 1 {
		t.Errorf("expected exactly 1 winning token after concurrent rotations, got %d valid", validCount)
	}
	_ = q
}

// TestEnrollChain_LongDeviceNameDoesNotCrash exercises the create-and-enroll
// path with a 1MB device name. The handler must surface a clean response
// (302 to the landing URL on success, or a 302 with ?error= on a db-side
// constraint failure); no panic, no 500 error.
func TestEnrollChain_LongDeviceNameDoesNotCrash(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminUser := &auth.User{ID: admin.ID, Email: admin.Email, Role: auth.RoleAdmin}
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	long := strings.Repeat("a", 1024*1024)
	form := url.Values{}
	form.Set("name", long)

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

	// 302 is the only acceptable response shape (success-redirect or
	// flash-error redirect); 5xx or a panic would mean the handler did not
	// guard against pathological input.
	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want 302 on long-name input", rr.Code)
	}
}

// TestEnrollChain_AdminPostingForRevokedDeviceLeavesAdminAuthenticated
// verifies that a failed enrollment (revoked target) leaves the admin's
// session row in place AND a follow-up GET to /admin/devices through the
// same chain still succeeds. AC-31 end-to-end through the chain.
func TestEnrollChain_AdminPostingForRevokedDeviceLeavesAdminAuthenticated(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	dev, _, err := deps.Auth.CreateDevice(context.Background(), "soon-revoked", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := deps.Auth.RevokeDevice(context.Background(), dev.ID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// First POST -- enrollment must fail without mutating cookies.
	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices/"+dev.ID+"/enroll-browser", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	resp, err := adversarialClient().Do(req)
	if err != nil {
		t.Fatalf("POST enroll-browser: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 (flash-error redirect)", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/admin/devices?error=") {
		t.Errorf("Location = %q, want /admin/devices?error=...", loc)
	}
	if hasSetCookie(resp, deps.DeviceCookieName) {
		t.Error("device cookie was set despite revoked target")
	}
	if c := findSetCookie(resp, deps.CookieName); c != nil && c.MaxAge < 0 {
		t.Errorf("admin cookie was cleared despite failed enrollment: %+v", c)
	}

	// Follow-up GET to /admin/devices with the original admin cookie must
	// still succeed -- the session row was preserved.
	listReq, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/devices", nil)
	if err != nil {
		t.Fatalf("new list request: %v", err)
	}
	listReq.Header.Set("Accept", "text/html")
	listReq.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	listResp, err := adversarialClient().Do(listReq)
	if err != nil {
		t.Fatalf("GET /admin/devices: %v", err)
	}
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		t.Errorf("follow-up GET status = %d, want 200 (admin session must survive failed enroll)", listResp.StatusCode)
	}
}

// TestEnrollChain_LandingPageAdminTakesPrecedence verifies the RequireAuth
// probe order: when a request carries BOTH an admin session cookie AND a
// device cookie, the landing handler sees the admin identity, not the device
// identity. (This matches RequireAuth's documented order: admin session
// first.)
func TestEnrollChain_LandingPageAdminTakesPrecedence(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, deviceRaw, err := deps.Auth.CreateDevice(context.Background(), "kitchen-tablet", admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+deps.DeviceLandingURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})
	req.AddCookie(&http.Cookie{Name: deps.DeviceCookieName, Value: deviceRaw})

	resp, err := adversarialClient().Do(req)
	if err != nil {
		t.Fatalf("GET landing: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := readAll(resp)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(body, "viewing as admin") {
		t.Errorf("expected admin-precedence formatting; body did not include 'viewing as admin': %q", body)
	}
	if strings.Contains(body, "kitchen-tablet") {
		t.Error("device name should not appear when admin identity wins")
	}
}

// TestEnrollChain_LandingPageXSSEscapesDeviceName verifies the device name
// containing HTML metacharacters is escaped in the rendered landing page.
func TestEnrollChain_LandingPageXSSEscapesDeviceName(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	xssName := `<script>alert("xss")</script>`
	_, rawDeviceToken, err := deps.Auth.CreateDevice(context.Background(), xssName, admin.ID)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+deps.DeviceLandingURL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: deps.DeviceCookieName, Value: rawDeviceToken})

	resp, err := adversarialClient().Do(req)
	if err != nil {
		t.Fatalf("GET landing: %v", err)
	}
	defer resp.Body.Close()

	body, err := readAll(resp)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(body, xssName) {
		t.Errorf("XSS: literal <script> tag from device name found in landing body")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected device name to be HTML-escaped in landing body; got %q", body)
	}
}

// TestEnrollChain_NewBrowserRouteCreatesDeviceWithEnrollerAsCreator
// verifies the audit trail: a new device created via enroll-new-browser
// records the enrolling admin as created_by so that "who provisioned this
// kiosk" is answerable from the database.
func TestEnrollChain_NewBrowserRouteCreatesDeviceWithEnrollerAsCreator(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin-enroller@example.com", "admin")
	adminToken, err := deps.Auth.CreateSession(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := deps.Auth.ValidateSession(context.Background(), adminToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	mux := http.NewServeMux()
	AddRoutes(mux, deps)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)
	form.Set("name", "audit-trail-device")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/devices/enroll-new-browser", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: adminToken})

	resp, err := adversarialClient().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}

	devs, err := deps.Auth.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	var found *auth.Device
	for i := range devs {
		if devs[i].Name == "audit-trail-device" {
			found = &devs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected device 'audit-trail-device' in the list")
	}
	if found.CreatedBy != admin.ID {
		t.Errorf("device.CreatedBy = %q, want %q (the enrolling admin)", found.CreatedBy, admin.ID)
	}
}

// readAll reads the body of an *http.Response into a string. Tiny helper to
// keep the table-driven assertions readable.
func readAll(resp *http.Response) (string, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return "", err
	}
	return buf.String(), nil
}

package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/auth"
)

// withCapturedSlog swaps the default slog handler for a buffer-backed JSON
// handler so a test can assert on the structured fields emitted during the
// call. The original handler is restored on cleanup. NOT safe for parallel
// tests because slog's default handler is process-global.
func withCapturedSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestRequireAuth_RevokedDevice_LogsKindDevice covers SPEC-003 AC-14: a
// revoked device must produce an info-level slog line with kind=device and a
// sanitised reason; the raw token must not appear.
//
// This test is NOT t.Parallel() because slog's default handler is global.
func TestRequireAuth_RevokedDevice_LogsKindDevice(t *testing.T) {
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, raw := makeDevice(t, svc, creator.ID, "log-revoked")
	if err := svc.RevokeDevice(context.Background(), dev.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	buf := withCapturedSlog(t)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	out := buf.String()
	if !strings.Contains(out, `"kind":"device"`) {
		t.Errorf("expected kind=device in slog output, got: %s", out)
	}
	if !strings.Contains(out, `"reason":"revoked"`) {
		t.Errorf("expected reason=revoked in slog output, got: %s", out)
	}
	if strings.Contains(out, raw) {
		t.Errorf("FATAL: raw device token leaked into slog output: %s", out)
	}
}

// TestRequireAuth_UnknownBearer_LogsKindDevice verifies a similar contract
// for an unknown bearer token: kind=device, reason=unknown_token.
func TestRequireAuth_UnknownBearer_LogsKindDevice(t *testing.T) {
	svc, _, _ := newDeviceTestService(t, time.Minute)

	buf := withCapturedSlog(t)

	bogus := strings.Repeat("a", 64)
	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer "+bogus)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	out := buf.String()
	if !strings.Contains(out, `"kind":"device"`) {
		t.Errorf("expected kind=device, got: %s", out)
	}
	if !strings.Contains(out, `"reason":"unknown_token"`) {
		t.Errorf("expected reason=unknown_token, got: %s", out)
	}
	if strings.Contains(out, bogus) {
		t.Errorf("FATAL: raw token leaked into slog output: %s", out)
	}
}

// TestRequireAuth_NoCredential_LogsKindNone preserves the existing behaviour:
// when there is no credential at all, the log line says kind=none.
func TestRequireAuth_NoCredential_LogsKindNone(t *testing.T) {
	svc, _, _ := newDeviceTestService(t, time.Minute)

	buf := withCapturedSlog(t)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	out := buf.String()
	if !strings.Contains(out, `"kind":"none"`) {
		t.Errorf("expected kind=none, got: %s", out)
	}
}

// TestRequireAuth_AdminAndDevice_AdminWins covers the probe order for the
// adversarial case where BOTH credentials are valid: the admin session must
// win. CSRF protection still applies because the identity is admin.
func TestRequireAuth_AdminAndDevice_AdminWins(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	user := createTestUser(t, q, "both-creds@example.com", "admin")
	rawSession, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	dev, rawBearer := makeDevice(t, svc, creator.ID, "both-creds-device")

	var got *auth.Identity
	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(captureIdentity(&got))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawSession})
	req.Header.Set("Authorization", "Bearer "+rawBearer)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsAdmin() {
		t.Fatalf("identity = %+v, want IsAdmin (admin must win over device)", got)
	}
	if got.User == nil || got.User.ID != user.ID {
		t.Errorf("admin user mismatch: got %+v, want %q", got.User, user.ID)
	}
	// Sanity: the device that was sent in the bearer header must NOT be the one
	// in the identity (admin wins).
	if got.Device != nil {
		t.Errorf("Device should be nil for admin identity, got %+v (collision with %q)",
			got.Device, dev.ID)
	}
}

// TestRequireAuth_BadBearer_FallsThroughToCookie verifies the documented
// fallback chain: an invalid bearer header does NOT short-circuit, so a valid
// device cookie still authenticates.
func TestRequireAuth_BadBearer_FallsThroughToCookie(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)

	dev, raw := makeDevice(t, svc, creator.ID, "fallback-device")

	var got *auth.Identity
	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(captureIdentity(&got))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	// Garbage bearer that hashes to nothing in the DB.
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	req.AddCookie(&http.Cookie{Name: "screens_device", Value: raw})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (cookie should fall through bad bearer)", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsDevice() || got.Device == nil || got.Device.ID != dev.ID {
		t.Errorf("identity = %+v, want device id=%q", got, dev.ID)
	}
}

// TestRequireAuth_DeactivatedAdmin_FallsThroughToDevice covers the case where
// an admin session is presented but the underlying user has been deactivated
// (ValidateSession returns an error). The probe must continue to the bearer
// header and accept a valid device.
func TestRequireAuth_DeactivatedAdmin_FallsThroughToDevice(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	user := createTestUser(t, q, "deactivated-fallback@example.com", "admin")
	rawSession, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := q.DeactivateUser(context.Background(), user.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	dev, rawBearer := makeDevice(t, svc, creator.ID, "device-after-admin-fail")

	var got *auth.Identity
	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(captureIdentity(&got))
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawSession})
	req.Header.Set("Authorization", "Bearer "+rawBearer)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (device should authenticate after admin probe fails)", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsDevice() {
		t.Fatalf("identity = %+v, want IsDevice", got)
	}
	if got.Device == nil || got.Device.ID != dev.ID {
		t.Errorf("device id = %v, want %q", got.Device, dev.ID)
	}
}

// TestRequireAuth_BearerWithTabSeparator_NotAccepted verifies that "Bearer\t<token>"
// (tab instead of space) is rejected — only "Bearer " (capital B + space) is
// honoured per the documented contract.
func TestRequireAuth_BearerWithTabSeparator_NotAccepted(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)
	_, raw := makeDevice(t, svc, creator.ID, "tab-test")

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer\t"+raw)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (tab separator is not space)", rr.Code, http.StatusUnauthorized)
	}
}

// TestRequireAuth_BearerDoubleSpace_TrimsAndAccepts verifies that "Bearer  <token>"
// with two spaces still authenticates because TrimSpace removes the leading
// space. (The contract is "case-sensitive Bearer prefix"; what follows is
// trimmed.)
func TestRequireAuth_BearerDoubleSpace_TrimsAndAccepts(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)
	dev, raw := makeDevice(t, svc, creator.ID, "double-space")

	var got *auth.Identity
	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(captureIdentity(&got))
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer  "+raw) // two spaces
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got == nil || !got.IsDevice() || got.Device == nil || got.Device.ID != dev.ID {
		t.Errorf("identity = %+v, want device id=%q", got, dev.ID)
	}
}

// TestRequireAuth_VeryLongBearer_NoPanic asserts the middleware does not panic
// or 500 when handed a 1MB Authorization header value.
func TestRequireAuth_VeryLongBearer_NoPanic(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 1<<20))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (huge unknown token must 401, not 500/panic)", rr.Code, http.StatusUnauthorized)
	}
}

// TestRequireAuth_BearerWithControlChars_NoPanic asserts the middleware does
// not crash when handed control characters and other oddities in the token.
func TestRequireAuth_BearerWithControlChars_NoPanic(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	tests := []string{
		"Bearer \x00\x01\x02\x03",
		"Bearer " + string([]byte{0x7f, 0x80, 0xff}),
		"Bearer " + strings.Repeat("\u0000", 100),
	}
	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
		req.Header.Set("Authorization", tc)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("auth=%q: status = %d, want %d", tc, rr.Code, http.StatusUnauthorized)
		}
	}
}

// TestRequireAuth_DeviceCookieWithSessionLikeValue ensures a device cookie
// whose value happens to look like a session token (e.g., 64 hex chars) does
// not somehow re-enter the admin probe. The deviceCookie ONLY feeds
// ValidateDeviceToken; it never reaches ValidateSession.
func TestRequireAuth_DeviceCookieWithSessionLikeValue(t *testing.T) {
	t.Parallel()
	svc, q, _ := newDeviceTestService(t, time.Minute)

	user := createTestUser(t, q, "session-victim@example.com", "admin")
	rawSession, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Send a real session token AS THE DEVICE COOKIE. There is no session
	// cookie present. The device probe must NOT accept this; the request must
	// be unauthenticated.
	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.AddCookie(&http.Cookie{Name: "screens_device", Value: rawSession})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (session token in device cookie must NOT authenticate)", rr.Code, http.StatusUnauthorized)
	}
}

// TestRequireAuth_401Body_Sanitised confirms the 401 response body is the
// fixed string "unauthenticated" and never echoes back token contents.
func TestRequireAuth_401Body_Sanitised(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.Header.Set("Authorization", "Bearer SECRET-LEAK-MARKER-abc123")
	req.AddCookie(&http.Cookie{Name: "session", Value: "ANOTHER-SECRET-MARKER"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "unauthenticated") {
		t.Errorf("body missing 'unauthenticated': %q", body)
	}
	if strings.Contains(body, "SECRET-LEAK-MARKER") {
		t.Errorf("FATAL: bearer token echoed in 401 body: %q", body)
	}
	if strings.Contains(body, "ANOTHER-SECRET-MARKER") {
		t.Errorf("FATAL: session cookie echoed in 401 body: %q", body)
	}
}

// TestRequireAuth_WWWAuthenticateExactly verifies the WWW-Authenticate header
// is exactly "Bearer" (no realm, no challenge string). This pins the exact
// challenge format.
func TestRequireAuth_WWWAuthenticateExactly(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want %q exactly", got, "Bearer")
	}
}

// TestRequireAuth_HTMLNav_QValueAccept covers a real-world Accept header with
// q-values where text/html appears alongside JSON. Per the documented
// contract, isHTMLNav uses strings.Contains, so any Accept containing
// "text/html" triggers the redirect — including q-weighted entries.
func TestRequireAuth_HTMLNav_QValueAccept(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	tests := []struct {
		name, accept string
		wantStatus   int
	}{
		{"text/html only", "text/html", http.StatusFound},
		{"q-weighted html alongside json", "text/html;q=0.9, application/json;q=1.0", http.StatusFound},
		{"text/* (no text/html literally)", "text/*", http.StatusUnauthorized},
		{"*/*", "*/*", http.StatusUnauthorized},
		{"empty Accept", "", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
			req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Errorf("Accept=%q: status = %d, want %d", tc.accept, rr.Code, tc.wantStatus)
			}
		})
	}
}

// TestRequireAuth_POSTHTMLAccept_StillReturns401 verifies that a state-changing
// POST whose Accept header includes text/html is still treated as an API call
// (401), not redirected to login. Only GET/HEAD HTML navs redirect.
func TestRequireAuth_POSTHTMLAccept_StillReturns401(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/thing", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d (POST with text/html Accept must be 401, not 302)", rr.Code, http.StatusUnauthorized)
	}
}

// TestRequireAuth_HEADHTMLAccept_Redirects verifies HEAD requests are also
// considered HTML navs when Accept includes text/html.
func TestRequireAuth_HEADHTMLAccept_Redirects(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodHead, "/page", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
}

// TestRequireAuth_ConcurrentDeviceRequests stresses the device path with a
// shared bearer token under -race. MarkDeviceSeen runs on every successful
// auth and writes to the same row from many goroutines; a race or panic here
// would fail the test under -race.
func TestRequireAuth_ConcurrentDeviceRequests(t *testing.T) {
	t.Parallel()
	svc, _, creator := newDeviceTestService(t, time.Minute)
	_, raw := makeDevice(t, svc, creator.ID, "concurrent-device")

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())

	var wg sync.WaitGroup
	const N = 50
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
			req.Header.Set("Authorization", "Bearer "+raw)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("concurrent device request status = %d, want %d", rr.Code, http.StatusOK)
			}
		}()
	}
	wg.Wait()
}

// TestRequireAuth_ConcurrentMixedAuth alternates admin and device requests
// concurrently. The middleware must serve both correctly under contention.
func TestRequireAuth_ConcurrentMixedAuth(t *testing.T) {
	t.Parallel()
	svc, q, creator := newDeviceTestService(t, time.Minute)

	user := createTestUser(t, q, "mixed-conc@example.com", "admin")
	rawSession, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, rawBearer := makeDevice(t, svc, creator.ID, "mixed-device")

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())

	var wg sync.WaitGroup
	const N = 40
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
			if i%2 == 0 {
				req.AddCookie(&http.Cookie{Name: "session", Value: rawSession})
			} else {
				req.Header.Set("Authorization", "Bearer "+rawBearer)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("mixed auth req %d status = %d, want %d", i, rr.Code, http.StatusOK)
			}
		}()
	}
	wg.Wait()
}

// TestRequireCSRF_DeviceWithSessionContextDoesNotApply ensures that the device
// exemption check happens BEFORE the session CSRF check. A device identity in
// context skips CSRF even if a session is also present (defensive ordering).
func TestRequireCSRF_DeviceWithSessionContextDoesNotApply(t *testing.T) {
	t.Parallel()

	devID := &auth.Identity{Kind: auth.IdentityDevice, Device: &auth.Device{ID: "d1"}}
	sess := &auth.Session{CSRFToken: "real-csrf-token"}
	ctx := auth.ContextWithIdentity(context.Background(), devID)
	ctx = auth.ContextWithSession(ctx, sess)

	called := false
	handler := RequireCSRF()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	// POST with no _csrf and a non-matching token deliberately.
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("_csrf=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (device id in context exempts CSRF)", rr.Code, http.StatusOK)
	}
	if !called {
		t.Error("handler should have been called")
	}
}

// TestRequireDevice_BodyDoesNotLeak verifies the 403 body is the fixed string
// "Forbidden" and contains no caller-supplied data.
func TestRequireDevice_BodyDoesNotLeak(t *testing.T) {
	t.Parallel()
	handler := RequireDevice()(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/device-only?leakparam=SECRET", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	body := strings.TrimSpace(rr.Body.String())
	if body != "Forbidden" {
		t.Errorf("body = %q, want %q", body, "Forbidden")
	}
	if strings.Contains(rr.Body.String(), "SECRET") {
		t.Errorf("query param leaked into body: %q", rr.Body.String())
	}
}

// TestRequireAuth_ClearedCookieAttributes ensures the stale-session-cookie
// clear has the right attributes (HttpOnly, Path=/, MaxAge<0). A stale cookie
// without HttpOnly would weaken the original session cookie's protection.
func TestRequireAuth_ClearedCookieAttributes(t *testing.T) {
	t.Parallel()
	svc, _, _ := newDeviceTestService(t, time.Minute)

	handler := RequireAuth(svc, "session", "screens_device", "/admin/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/api/thing", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "stale"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var cleared *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == "session" {
			cleared = c
			break
		}
	}
	if cleared == nil {
		t.Fatal("session cookie not present in Set-Cookie")
	}
	if cleared.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want < 0", cleared.MaxAge)
	}
	if cleared.Value != "" {
		t.Errorf("Value = %q, want empty", cleared.Value)
	}
	if !cleared.HttpOnly {
		t.Error("cleared session cookie should be HttpOnly")
	}
	if cleared.Path != "/" {
		t.Errorf("Path = %q, want /", cleared.Path)
	}
}

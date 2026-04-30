package views

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/db"
)

// newTestDepsWithGoogle is like newTestDeps but also creates a GoogleClient
// so integration tests that register all routes don't panic.
func newTestDepsWithGoogle(t *testing.T) (*Deps, *db.Queries) {
	t.Helper()
	deps, q := newTestDeps(t)
	deps.Google = auth.NewGoogleClient("test-id", "test-secret", "http://localhost/callback")
	return deps, q
}

// TestLogoutThroughMiddlewareChain verifies that POST /admin/logout works
// when the full middleware chain is wired (Auth then CSRF).
// This catches middleware ordering bugs where CSRF runs before Auth,
// causing session to be nil when CSRF checks it.
func TestLogoutThroughMiddlewareChain(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	user := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Look up the session to get the CSRF token.
	_, session, err := deps.Auth.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
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

	form := url.Values{}
	form.Set("_csrf", session.CSRFToken)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/logout", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/logout: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d (middleware may be blocking the request)", resp.StatusCode, http.StatusFound)
	}
	loc := resp.Header.Get("Location")
	if loc != "/admin/login" {
		t.Errorf("Location = %q, want %q", loc, "/admin/login")
	}

	// Verify session was actually deleted.
	_, _, err = deps.Auth.ValidateSession(context.Background(), rawToken)
	if err == nil {
		t.Error("expected session to be deleted after logout through middleware")
	}
}

// TestLogoutWithoutCSRFToken_Returns403 verifies that POST /admin/logout
// without a CSRF token is rejected with 403 when going through the middleware chain.
func TestLogoutWithoutCSRFToken_Returns403(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	user := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), user.ID)
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

	// POST logout without CSRF token.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/logout", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/logout: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (missing CSRF should be rejected)", resp.StatusCode, http.StatusForbidden)
	}
}

// TestLogoutWithWrongCSRFToken_Returns403 verifies that POST /admin/logout
// with an incorrect CSRF token is rejected.
func TestLogoutWithWrongCSRFToken_Returns403(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	user := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), user.ID)
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

	form := url.Values{}
	form.Set("_csrf", "wrong-csrf-token")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/logout", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /admin/logout: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d (wrong CSRF should be rejected)", resp.StatusCode, http.StatusForbidden)
	}
}

// TestCallback_EmptyStateAndCookieBothEmpty prevents bypass where attacker
// sets both state param and cookie to empty string.
func TestCallback_EmptyStateAndCookieBothEmpty(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleGoogleCallback(deps)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=&code=authcode", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: ""})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "/admin/login") {
		t.Errorf("Location = %q, want redirect to /admin/login", loc)
	}
	if !strings.Contains(loc, "error=") {
		t.Error("Location missing error parameter")
	}
}

// TestCallback_NoStateParam verifies callback rejects request with
// no state parameter at all.
func TestCallback_NoStateParam(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleGoogleCallback(deps)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?code=authcode", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "some-state"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "/admin/login") {
		t.Errorf("Location = %q, want redirect to /admin/login", loc)
	}
}

// TestLogin_ErrorParamHTMLInjection verifies that HTML in the error query
// parameter is escaped and not rendered as raw HTML (XSS prevention).
func TestLogin_ErrorParamHTMLInjection(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleLogin(deps.Auth, deps.CookieName)

	xss := `<script>alert('xss')</script>`
	req := httptest.NewRequest(http.MethodGet, "/admin/login?error="+url.QueryEscape(xss), nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	// The raw script tag must NOT appear unescaped.
	if strings.Contains(body, "<script>") {
		t.Error("XSS: unescaped <script> tag found in response body")
	}
	// The escaped form should be present.
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected HTML-escaped script tag in response body")
	}
}

// TestLogin_InvalidSessionCookie verifies that a corrupt/invalid session
// cookie doesn't cause a panic -- user sees the login page.
func TestLogin_InvalidSessionCookie(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleLogin(deps.Auth, deps.CookieName)

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: "invalid-garbage-token"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (invalid cookie should show login page)", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Sign in with Google") {
		t.Error("response body missing 'Sign in with Google' when cookie is invalid")
	}
}

// TestLogin_EmptySessionCookie verifies that an empty session cookie
// shows the login page.
func TestLogin_EmptySessionCookie(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleLogin(deps.Auth, deps.CookieName)

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: ""})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// TestAdmin_NilSessionInContext verifies the admin handler handles
// the case where user exists in context but session doesn't.
func TestAdmin_NilSessionInContext(t *testing.T) {
	t.Parallel()

	user := &auth.User{
		ID:    "user-123",
		Email: "test@example.com",
		Role:  auth.RoleMember,
	}

	ctx := auth.ContextWithUser(context.Background(), user)
	// Intentionally NOT setting session in context.

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleAdmin(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d (nil session should redirect)", rr.Code, http.StatusFound)
	}
}

// TestAdmin_NilUserInContext verifies the admin handler handles
// the case where session exists in context but user doesn't.
func TestAdmin_NilUserInContext(t *testing.T) {
	t.Parallel()

	session := &auth.Session{
		CSRFToken: "some-token",
	}

	ctx := auth.ContextWithSession(context.Background(), session)
	// Intentionally NOT setting user in context.

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleAdmin(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d (nil user should redirect)", rr.Code, http.StatusFound)
	}
}

// TestPublicRoutes_AccessibleWithoutAuth verifies that public routes
// are accessible without authentication.
func TestPublicRoutes_AccessibleWithoutAuth(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)
	deps.Google = auth.NewGoogleClient("test-id", "test-secret", "http://localhost/callback")

	mux := http.NewServeMux()
	AddRoutes(mux, deps)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"login page", "/admin/login", http.StatusOK},
		{"google start", "/auth/google/start", http.StatusFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tt.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("GET %s: status = %d, want %d", tt.path, resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// TestProtectedRoutes_MultipleRoutes verifies that various admin routes
// redirect to login when not authenticated.
func TestProtectedRoutes_MultipleRoutes(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDepsWithGoogle(t)

	mux := http.NewServeMux()
	AddRoutes(mux, deps)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	paths := []string{
		"/admin/",
		"/admin/anything",
		"/admin/users",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Accept", "text/html")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusFound {
				t.Errorf("GET %s: status = %d, want %d", path, resp.StatusCode, http.StatusFound)
			}
			loc := resp.Header.Get("Location")
			if loc != "/admin/login" {
				t.Errorf("GET %s: Location = %q, want %q", path, loc, "/admin/login")
			}
		})
	}
}

// TestGoogleStart_StateCookieAttributes verifies the oauth_state cookie
// has correct security attributes.
func TestGoogleStart_StateCookieAttributes(t *testing.T) {
	t.Parallel()

	gc := auth.NewGoogleClient("test-client-id", "test-secret", "http://localhost/callback")
	handler := handleGoogleStart(gc)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/start", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	cookies := rr.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "oauth_state" {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("missing oauth_state cookie")
	}

	if stateCookie.MaxAge != 300 {
		t.Errorf("MaxAge = %d, want 300", stateCookie.MaxAge)
	}
	if !stateCookie.HttpOnly {
		t.Error("oauth_state cookie should be HttpOnly")
	}
	if stateCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %d, want Lax (%d)", stateCookie.SameSite, http.SameSiteLaxMode)
	}
	if stateCookie.Path != "/" {
		t.Errorf("Path = %q, want %q", stateCookie.Path, "/")
	}
}

// TestLogout_SessionCookieAttributes verifies the cleared session cookie
// has correct security attributes.
func TestLogout_SessionCookieAttributes(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	user := createTestUser(t, q, "user@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	handler := handleLogout(deps.Auth, deps.CookieName)
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	cookies := rr.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == deps.CookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("missing cleared session cookie")
	}

	if sessionCookie.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want < 0 (cookie should be cleared)", sessionCookie.MaxAge)
	}
	if !sessionCookie.HttpOnly {
		t.Error("cleared session cookie should be HttpOnly")
	}
	if sessionCookie.Value != "" {
		t.Errorf("Value = %q, want empty", sessionCookie.Value)
	}
	if sessionCookie.Path != "/" {
		t.Errorf("Path = %q, want %q", sessionCookie.Path, "/")
	}
}

// TestAuthenticatedAdmin_IntegrationWithMiddleware verifies the full
// request flow: authenticated GET /admin/ serves the admin page.
func TestAuthenticatedAdmin_IntegrationWithMiddleware(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	user := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), user.ID)
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

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// TestGoogleStart_UniqueStatePerRequest verifies each call to /auth/google/start
// generates a different state value.
func TestGoogleStart_UniqueStatePerRequest(t *testing.T) {
	t.Parallel()

	gc := auth.NewGoogleClient("test-client-id", "test-secret", "http://localhost/callback")
	handler := handleGoogleStart(gc)

	var states []string
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/auth/google/start", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		for _, c := range rr.Result().Cookies() {
			if c.Name == "oauth_state" {
				states = append(states, c.Value)
			}
		}
	}

	if len(states) != 5 {
		t.Fatalf("expected 5 state values, got %d", len(states))
	}

	// Check all are unique.
	seen := make(map[string]bool)
	for _, s := range states {
		if seen[s] {
			t.Errorf("duplicate state value: %s", s)
		}
		seen[s] = true
	}
}

// TestCallback_ClearsStateCookieEvenOnError verifies the oauth_state cookie
// is cleared even when the code is missing (preventing reuse).
func TestCallback_ClearsStateCookieEvenOnError(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleGoogleCallback(deps)

	state := "valid-state"
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+state, nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: state})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should redirect to login with error (missing code).
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}

	// The oauth_state cookie should be cleared.
	var cleared bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == "oauth_state" && c.MaxAge < 0 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Error("oauth_state cookie should be cleared even when code is missing")
	}
}

// TestAddRoutes_NilDeps verifies that AddRoutes works when deps is nil
// (only public non-auth routes are registered).
func TestAddRoutes_NilDeps(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	// Should not panic with nil deps.
	AddRoutes(mux, nil)
}

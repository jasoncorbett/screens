package views

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/db"
)

func newTestDeps(t *testing.T) (*Deps, *db.Queries) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	svc := auth.NewService(sqlDB, auth.Config{
		AdminEmail:             "admin@example.com",
		SessionDuration:        time.Hour,
		CookieName:             "session",
		SecureCookie:           false,
		DeviceCookieName:       "device",
		DeviceLastSeenInterval: time.Minute,
		DeviceLandingURL:       "/device/",
	})
	q := db.New(sqlDB)
	return &Deps{
		Auth:             svc,
		Google:           nil, // tests that need Google use a mock server
		ClientID:         "test-client-id",
		CookieName:       "session",
		DeviceCookieName: "device",
		DeviceLandingURL: "/device/",
		SecureCookie:     false,
	}, q
}

func createTestUser(t *testing.T, q *db.Queries, email, role string) db.User {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate id: %v", err)
	}
	id := hex.EncodeToString(b)
	row, err := q.CreateUser(context.Background(), db.CreateUserParams{
		ID:          id,
		Email:       email,
		DisplayName: "Test User",
		Role:        role,
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return row
}

func TestHandleLogin_ShowsLoginPage(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleLogin(deps.Auth, deps.CookieName)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Sign in with Google") {
		t.Error("response body missing 'Sign in with Google'")
	}
	if !strings.Contains(body, "/auth/google/start") {
		t.Error("response body missing link to /auth/google/start")
	}
}

func TestHandleLogin_ShowsErrorMessage(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleLogin(deps.Auth, deps.CookieName)
	req := httptest.NewRequest(http.MethodGet, "/admin/login?error=Access+denied", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Access denied") {
		t.Error("response body missing error message 'Access denied'")
	}
}

func TestHandleLogin_RedirectsIfAuthenticated(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	user := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := deps.Auth.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	handler := handleLogin(deps.Auth, deps.CookieName)
	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/admin/" {
		t.Errorf("Location = %q, want %q", loc, "/admin/")
	}
}

func TestHandleGoogleStart_SetsStateCookieAndRedirects(t *testing.T) {
	t.Parallel()

	gc := auth.NewGoogleClient("test-client-id", "test-secret", "http://localhost/callback")
	handler := handleGoogleStart(gc)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/start", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}

	// Check oauth_state cookie was set.
	cookies := rr.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "oauth_state" {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("expected oauth_state cookie to be set")
	}
	if len(stateCookie.Value) != 32 { // 16 bytes hex-encoded
		t.Errorf("oauth_state cookie length = %d, want 32", len(stateCookie.Value))
	}
	if !stateCookie.HttpOnly {
		t.Error("oauth_state cookie should be HttpOnly")
	}

	// Check redirect URL points to Google.
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "accounts.google.com") {
		t.Errorf("Location = %q, want URL containing accounts.google.com", loc)
	}
	if !strings.Contains(loc, "state="+stateCookie.Value) {
		t.Error("Location URL missing state parameter matching cookie")
	}
}

func TestHandleGoogleCallback_MismatchedState(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleGoogleCallback(deps)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=wrong&code=authcode", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "correct-state"})
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

func TestHandleGoogleCallback_MissingStateCookie(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleGoogleCallback(deps)

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=some-state&code=authcode", nil)
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

func TestHandleGoogleCallback_MissingCode(t *testing.T) {
	t.Parallel()

	// Use a mock token server so we can test the code exchange path.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("token endpoint should not be called when code is missing")
	}))
	defer tokenServer.Close()

	deps, _ := newTestDeps(t)
	handler := handleGoogleCallback(deps)

	state := "valid-state"
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+state, nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: state})
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

func TestHandleLogout_ClearsCookieAndRedirects(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	user := createTestUser(t, q, "user@example.com", "member")
	rawToken, err := deps.Auth.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	handler := handleLogout(deps.Auth, deps.CookieName)
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/admin/login" {
		t.Errorf("Location = %q, want %q", loc, "/admin/login")
	}

	// Verify session cookie is cleared.
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "session" && c.MaxAge < 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Set-Cookie to clear session cookie with MaxAge < 0")
	}

	// Verify session was deleted from DB.
	_, _, err = deps.Auth.ValidateSession(context.Background(), rawToken)
	if err == nil {
		t.Error("expected session to be deleted after logout")
	}
}

func TestHandleLogout_NoCookie(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleLogout(deps.Auth, deps.CookieName)
	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/admin/login" {
		t.Errorf("Location = %q, want %q", loc, "/admin/login")
	}
}

func TestHandleAdmin_ShowsUserInfo(t *testing.T) {
	t.Parallel()

	user := &auth.User{
		ID:          "user-123",
		Email:       "admin@example.com",
		DisplayName: "Admin User",
		Role:        auth.RoleAdmin,
		Active:      true,
	}
	session := &auth.Session{
		CSRFToken: "csrf-token-value",
	}

	ctx := auth.ContextWithUser(context.Background(), user)
	ctx = auth.ContextWithSession(ctx, session)

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleAdmin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "admin@example.com") {
		t.Error("response body missing user email")
	}
	if !strings.Contains(body, "Admin User") {
		t.Error("response body missing display name")
	}
	if !strings.Contains(body, "Logout") {
		t.Error("response body missing Logout button")
	}
	if !strings.Contains(body, "/admin/users") {
		t.Error("response body missing link to /admin/users for admin user")
	}
	if !strings.Contains(body, "csrf-token-value") {
		t.Error("response body missing CSRF token in form")
	}
}

func TestHandleAdmin_MemberNoUsersLink(t *testing.T) {
	t.Parallel()

	user := &auth.User{
		ID:          "user-456",
		Email:       "member@example.com",
		DisplayName: "Member User",
		Role:        auth.RoleMember,
		Active:      true,
	}
	session := &auth.Session{
		CSRFToken: "csrf-token-value",
	}

	ctx := auth.ContextWithUser(context.Background(), user)
	ctx = auth.ContextWithSession(ctx, session)

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handleAdmin(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if strings.Contains(body, "/admin/users") {
		t.Error("response body should not contain /admin/users link for member")
	}
}

func TestHandleAdmin_RedirectsWhenNoUser(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rr := httptest.NewRecorder()

	handleAdmin(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/admin/login" {
		t.Errorf("Location = %q, want %q", loc, "/admin/login")
	}
}

func TestProtectedRoutes_RedirectWithoutAuth(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

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
	req.Header.Set("Accept", "text/html")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	loc := resp.Header.Get("Location")
	if loc != "/admin/login" {
		t.Errorf("Location = %q, want %q", loc, "/admin/login")
	}
}

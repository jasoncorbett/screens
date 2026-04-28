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

func newTestAuthService(t *testing.T) (*auth.Service, *db.Queries) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	cfg := auth.Config{
		AdminEmail:      "admin@example.com",
		SessionDuration: time.Hour,
		CookieName:      "session",
		SecureCookie:    false,
	}
	svc := auth.NewService(sqlDB, cfg)
	q := db.New(sqlDB)
	return svc, q
}

func createTestUser(t *testing.T, q *db.Queries, email, role string) db.User {
	t.Helper()
	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	row, err := q.CreateUser(context.Background(), db.CreateUserParams{
		ID:          token[:32],
		Email:       email,
		DisplayName: "Test User",
		Role:        role,
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return row
}

// okHandler is a simple handler that records that it was called and writes 200.
func okHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}

// --- RequireAuth tests ---

func TestRequireAuth_NoCookie(t *testing.T) {
	t.Parallel()
	svc, _ := newTestAuthService(t)

	handler := RequireAuth(svc, "session", "device", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("Location = %q, want %q", loc, "/login")
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	t.Parallel()
	svc, _ := newTestAuthService(t)

	handler := RequireAuth(svc, "session", "device", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: "session", Value: "bogus-token-value"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if loc != "/login" {
		t.Errorf("Location = %q, want %q", loc, "/login")
	}
}

func TestRequireAuth_ClearsInvalidCookie(t *testing.T) {
	t.Parallel()
	svc, _ := newTestAuthService(t)

	handler := RequireAuth(svc, "session", "device", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "bogus-token"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Check that a Set-Cookie header clears the session cookie.
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
}

func TestRequireAuth_ExpiredSession(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "user@example.com", "member")

	// Insert an already-expired session directly.
	rawToken, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	csrfToken, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate csrf: %v", err)
	}
	pastTime := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	err = q.CreateSession(context.Background(), db.CreateSessionParams{
		TokenHash: auth.HashToken(rawToken),
		UserID:    user.ID,
		CsrfToken: csrfToken,
		ExpiresAt: pastTime,
	})
	if err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	handler := RequireAuth(svc, "session", "device", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
}

func TestRequireAuth_ValidSession(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "user@example.com", "member")
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var gotUser *auth.User
	var gotSession *auth.Session
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = auth.UserFromContext(r.Context())
		gotSession = auth.SessionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireAuth(svc, "session", "device", "/login")(inner)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser == nil {
		t.Fatal("expected user in context, got nil")
	}
	if gotUser.ID != user.ID {
		t.Errorf("user ID = %q, want %q", gotUser.ID, user.ID)
	}
	if gotSession == nil {
		t.Fatal("expected session in context, got nil")
	}
	if gotSession.CSRFToken == "" {
		t.Error("expected non-empty CSRF token in session")
	}
}

// --- RequireCSRF tests ---

func TestRequireCSRF_GETPassesThrough(t *testing.T) {
	t.Parallel()

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireCSRF_HEADPassesThrough(t *testing.T) {
	t.Parallel()

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodHead, "/page", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireCSRF_OPTIONSPassesThrough(t *testing.T) {
	t.Parallel()

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodOptions, "/page", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireCSRF_POSTNoSession(t *testing.T) {
	t.Parallel()

	handler := RequireCSRF()(okHandler())
	form := url.Values{"_csrf": {"some-token"}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_POSTMissingToken(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-csrf-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/action", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_POSTWrongToken(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-csrf-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	form := url.Values{"_csrf": {"wrong-csrf-token"}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_POSTValidFormToken(t *testing.T) {
	t.Parallel()

	csrfToken := "correct-csrf-token-value"
	session := &auth.Session{CSRFToken: csrfToken}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	form := url.Values{"_csrf": {csrfToken}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireCSRF_POSTValidHeaderToken(t *testing.T) {
	t.Parallel()

	csrfToken := "correct-csrf-token-value"
	session := &auth.Session{CSRFToken: csrfToken}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodPost, "/action", nil)
	req.Header.Set("X-CSRF-Token", csrfToken)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireCSRF_DELETERequiresToken(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodDelete, "/resource", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_PUTRequiresToken(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodPut, "/resource", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_PATCHRequiresToken(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest(http.MethodPatch, "/resource", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

// --- RequireRole tests ---

func TestRequireRole_NoUserInContext(t *testing.T) {
	t.Parallel()

	handler := RequireRole(auth.RoleAdmin)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireRole_WrongRole(t *testing.T) {
	t.Parallel()

	user := &auth.User{ID: "u1", Role: auth.RoleMember}
	ctx := auth.ContextWithUser(context.Background(), user)

	handler := RequireRole(auth.RoleAdmin)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestRequireRole_AllowedRole(t *testing.T) {
	t.Parallel()

	user := &auth.User{ID: "u1", Role: auth.RoleAdmin}
	ctx := auth.ContextWithUser(context.Background(), user)

	handler := RequireRole(auth.RoleAdmin)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireRole_MultipleAllowedRoles(t *testing.T) {
	t.Parallel()

	user := &auth.User{ID: "u1", Role: auth.RoleMember}
	ctx := auth.ContextWithUser(context.Background(), user)

	handler := RequireRole(auth.RoleAdmin, auth.RoleMember)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/shared", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

// --- Integration: full middleware chain ---

func TestMiddlewareChain_Integration(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "admin@example.com", "admin")
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Validate the session to get the CSRF token.
	_, session, err := svc.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	var gotUser *auth.User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = auth.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	// Chain: RequireRole(admin) -> RequireCSRF -> RequireAuth -> inner
	handler := RequireAuth(svc, "session", "device", "/login")(
		RequireCSRF()(
			RequireRole(auth.RoleAdmin)(inner),
		),
	)

	form := url.Values{"_csrf": {session.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/admin/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser == nil {
		t.Fatal("expected user in context")
	}
	if gotUser.Role != auth.RoleAdmin {
		t.Errorf("user role = %q, want %q", gotUser.Role, auth.RoleAdmin)
	}
}

func TestMiddlewareChain_MemberBlockedByAdminRole(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "member@example.com", "member")
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, session, err := svc.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	handler := RequireAuth(svc, "session", "device", "/login")(
		RequireCSRF()(
			RequireRole(auth.RoleAdmin)(okHandler()),
		),
	)

	form := url.Values{"_csrf": {session.CSRFToken}}
	req := httptest.NewRequest(http.MethodPost, "/admin/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

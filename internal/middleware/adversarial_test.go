package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/db"
)

// --- helpers ---

// newTestAuthServiceSingleConn creates a test auth service with MaxOpenConns(1)
// to avoid SQLite in-memory concurrency issues where each connection gets a
// separate database.
func newTestAuthServiceSingleConn(t *testing.T) (*auth.Service, *db.Queries) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	sqlDB.SetMaxOpenConns(1)
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

// --- RequireAuth adversarial tests ---

func TestRequireAuth_EmptyCookieValue(t *testing.T) {
	t.Parallel()
	svc, _ := newTestAuthService(t)

	handler := RequireAuth(svc, "session", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: ""})
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

func TestRequireAuth_VeryLongCookieValue(t *testing.T) {
	t.Parallel()
	svc, _ := newTestAuthService(t)

	handler := RequireAuth(svc, "session", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	// A cookie value with 10K characters -- should not crash or panic.
	req.AddCookie(&http.Cookie{Name: "session", Value: strings.Repeat("a", 10000)})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
}

func TestRequireAuth_NullByteInCookie(t *testing.T) {
	t.Parallel()
	svc, _ := newTestAuthService(t)

	handler := RequireAuth(svc, "session", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid\x00injected"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
}

func TestRequireAuth_UnicodeInCookie(t *testing.T) {
	t.Parallel()
	svc, _ := newTestAuthService(t)

	handler := RequireAuth(svc, "session", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "\u0000\u0001\u0002\uFFFD"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
}

func TestRequireAuth_DeactivatedUserDenied(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "deactivated@example.com", "member")
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Deactivate the user after session creation.
	err = svc.DeactivateUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	handler := RequireAuth(svc, "session", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should redirect because the session was deleted during deactivation.
	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
}

func TestRequireAuth_ConcurrentRequests(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthServiceSingleConn(t)

	user := createTestUser(t, q, "concurrent@example.com", "member")
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var gotUserMu sync.Mutex
	gotUsers := make([]*auth.User, 0, 10)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		gotUserMu.Lock()
		gotUsers = append(gotUsers, u)
		gotUserMu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireAuth(svc, "session", "/login")(inner)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("concurrent request status = %d, want %d", rr.Code, http.StatusOK)
			}
		}()
	}
	wg.Wait()

	gotUserMu.Lock()
	defer gotUserMu.Unlock()
	if len(gotUsers) != 10 {
		t.Errorf("got %d users, want 10", len(gotUsers))
	}
	for i, u := range gotUsers {
		if u == nil || u.ID != user.ID {
			t.Errorf("gotUsers[%d] = %v, want user with ID %q", i, u, user.ID)
		}
	}
}

func TestRequireAuth_MultipleSessionCookies(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "multi@example.com", "member")
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Send two session cookies -- Go's r.Cookie returns the first one.
	handler := RequireAuth(svc, "session", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	req.AddCookie(&http.Cookie{Name: "session", Value: "bogus-second"})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (first cookie should be used)", rr.Code, http.StatusOK)
	}
}

func TestRequireAuth_InactiveUser_RecreatedSession(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "inactive-resession@example.com", "member")

	// Create a session, then deactivate user, then re-create session manually.
	// The user is inactive but a session exists (e.g., race or direct DB manipulation).
	rawToken, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	csrfToken, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("generate csrf: %v", err)
	}

	// First deactivate the user.
	if err := q.DeactivateUser(context.Background(), user.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// Manually insert a session for the deactivated user.
	futureTime := time.Now().UTC().Add(time.Hour).Format("2006-01-02 15:04:05")
	err = q.CreateSession(context.Background(), db.CreateSessionParams{
		TokenHash: auth.HashToken(rawToken),
		UserID:    user.ID,
		CsrfToken: csrfToken,
		ExpiresAt: futureTime,
	})
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	handler := RequireAuth(svc, "session", "/login")(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Must reject -- user is deactivated even though session is valid.
	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d (inactive user should be rejected)", rr.Code, http.StatusFound)
	}
}

// --- RequireCSRF adversarial tests ---

func TestRequireCSRF_EmptySessionCSRFToken(t *testing.T) {
	t.Parallel()

	// Session with empty CSRF token -- should reject all CSRF submissions.
	session := &auth.Session{CSRFToken: ""}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	form := url.Values{"_csrf": {""}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (empty tokens should be rejected)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_FormFieldTakesPriorityOverHeader(t *testing.T) {
	t.Parallel()

	csrfToken := "correct-token-value"
	session := &auth.Session{CSRFToken: csrfToken}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	// Form has the correct token, header has the wrong token.
	form := url.Values{"_csrf": {csrfToken}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", "wrong-header-token")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (form field should take priority)", rr.Code, http.StatusOK)
	}
}

func TestRequireCSRF_HeaderUsedWhenNoFormField(t *testing.T) {
	t.Parallel()

	csrfToken := "correct-token-value"
	session := &auth.Session{CSRFToken: csrfToken}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	// No form body, only header.
	req := httptest.NewRequest(http.MethodPost, "/action", nil)
	req.Header.Set("X-CSRF-Token", csrfToken)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestRequireCSRF_VeryLongToken(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	form := url.Values{"_csrf": {strings.Repeat("x", 100000)}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (oversized token should be rejected)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_NullByteInToken(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	form := url.Values{"_csrf": {"real-token\x00extra"}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (null byte in token should be rejected)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_TRACEMethodBlocked(t *testing.T) {
	t.Parallel()

	// TRACE is not a safe method for CSRF purposes.
	session := &auth.Session{CSRFToken: "real-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest("TRACE", "/action", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// TRACE without a CSRF token should be blocked.
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (TRACE should require CSRF)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_UnknownMethodBlocked(t *testing.T) {
	t.Parallel()

	session := &auth.Session{CSRFToken: "real-token"}
	ctx := auth.ContextWithSession(context.Background(), session)

	handler := RequireCSRF()(okHandler())
	req := httptest.NewRequest("FOOBAR", "/action", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Custom/unknown methods should require CSRF validation.
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (unknown methods should require CSRF)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireCSRF_ConstantTimeCompareUsed(t *testing.T) {
	t.Parallel()

	// This test verifies the comparison is done with crypto/subtle by checking that
	// tokens of the same length but different content are both rejected -- the timing
	// is not trivially testable, so we verify the functional behavior.
	csrfToken := "abcdef1234567890abcdef1234567890"
	session := &auth.Session{CSRFToken: csrfToken}

	tests := []struct {
		name      string
		submitted string
		wantCode  int
	}{
		{"first-char-wrong", "Xbcdef1234567890abcdef1234567890", http.StatusForbidden},
		{"last-char-wrong", "abcdef1234567890abcdef123456789X", http.StatusForbidden},
		{"all-chars-wrong", "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX", http.StatusForbidden},
		{"exact-match", csrfToken, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := auth.ContextWithSession(context.Background(), session)

			handler := RequireCSRF()(okHandler())
			form := url.Values{"_csrf": {tt.submitted}}
			req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantCode)
			}
		})
	}
}

func TestRequireCSRF_FormBodyStillAccessibleDownstream(t *testing.T) {
	t.Parallel()

	csrfToken := "valid-csrf-token"
	session := &auth.Session{CSRFToken: csrfToken}
	ctx := auth.ContextWithSession(context.Background(), session)

	var gotName string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The downstream handler should still be able to read form values.
		gotName = r.FormValue("name")
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireCSRF()(inner)
	form := url.Values{
		"_csrf": {csrfToken},
		"name":  {"test-value"},
	}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotName != "test-value" {
		t.Errorf("downstream got name = %q, want %q", gotName, "test-value")
	}
}

// --- RequireRole adversarial tests ---

func TestRequireRole_EmptyRolesList(t *testing.T) {
	t.Parallel()

	user := &auth.User{ID: "u1", Role: auth.RoleAdmin}
	ctx := auth.ContextWithUser(context.Background(), user)

	// No roles specified -- should block everyone.
	handler := RequireRole()(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (empty roles list should block)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireRole_CustomRoleString(t *testing.T) {
	t.Parallel()

	// If someone sets an arbitrary role string, it should not match known roles.
	user := &auth.User{ID: "u1", Role: auth.Role("superadmin")}
	ctx := auth.ContextWithUser(context.Background(), user)

	handler := RequireRole(auth.RoleAdmin, auth.RoleMember)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (unknown role should be blocked)", rr.Code, http.StatusForbidden)
	}
}

func TestRequireRole_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	handler := RequireRole(auth.RoleAdmin)(okHandler())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		role := auth.RoleMember
		if i%2 == 0 {
			role = auth.RoleAdmin
		}
		go func(r auth.Role) {
			defer wg.Done()
			user := &auth.User{ID: "u1", Role: r}
			ctx := auth.ContextWithUser(context.Background(), user)

			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if r == auth.RoleAdmin && rr.Code != http.StatusOK {
				t.Errorf("admin status = %d, want %d", rr.Code, http.StatusOK)
			}
			if r == auth.RoleMember && rr.Code != http.StatusForbidden {
				t.Errorf("member status = %d, want %d", rr.Code, http.StatusForbidden)
			}
		}(role)
	}
	wg.Wait()
}

// --- Full chain adversarial tests ---

func TestMiddlewareChain_CSRFWithoutAuth_Returns403(t *testing.T) {
	t.Parallel()

	// If CSRF middleware is accidentally placed before auth, it should
	// return 403 on POST because there's no session in context.
	handler := RequireCSRF()(okHandler())
	form := url.Values{"_csrf": {"any-token"}}
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestMiddlewareChain_RoleWithoutAuth_Returns403(t *testing.T) {
	t.Parallel()

	// If role middleware runs without auth, there's no user in context.
	handler := RequireRole(auth.RoleAdmin)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

func TestMiddlewareChain_GETBypassesCSRFButNotRole(t *testing.T) {
	t.Parallel()

	// GET requests pass through CSRF but still need correct role.
	user := &auth.User{ID: "u1", Role: auth.RoleMember}
	session := &auth.Session{CSRFToken: "unused-for-GET"}

	ctx := auth.ContextWithUser(context.Background(), user)
	ctx = auth.ContextWithSession(ctx, session)

	handler := RequireCSRF()(
		RequireRole(auth.RoleAdmin)(okHandler()),
	)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d (member should be blocked from admin-only route)", rr.Code, http.StatusForbidden)
	}
}

func TestMiddlewareChain_ValidPOSTWithAllMiddleware(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "chain-admin@example.com", "admin")
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := svc.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}

	var gotUser *auth.User
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = auth.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := RequireAuth(svc, "session", "/login")(
		RequireCSRF()(
			RequireRole(auth.RoleAdmin)(inner),
		),
	)

	// Valid POST with correct cookie and CSRF token via header (htmx style).
	req := httptest.NewRequest(http.MethodPost, "/admin/action", nil)
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if gotUser == nil || gotUser.Role != auth.RoleAdmin {
		t.Errorf("expected admin user, got %v", gotUser)
	}
}

func TestMiddlewareChain_ExpiredSessionClearedAndRedirected(t *testing.T) {
	t.Parallel()
	svc, q := newTestAuthService(t)

	user := createTestUser(t, q, "expired-chain@example.com", "admin")

	// Manually create an expired session.
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

	handler := RequireAuth(svc, "session", "/login")(
		RequireCSRF()(
			RequireRole(auth.RoleAdmin)(okHandler()),
		),
	)

	form := url.Values{"_csrf": {csrfToken}}
	req := httptest.NewRequest(http.MethodPost, "/admin/action", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: rawToken})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Should redirect to login, not return 403.
	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	// Cookie should be cleared.
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "session" && c.MaxAge < 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected cookie to be cleared on expired session")
	}
}

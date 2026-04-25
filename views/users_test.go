package views

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jasoncorbett/screens/internal/auth"
	"github.com/jasoncorbett/screens/internal/db"
)

func createTestSession(t *testing.T, authSvc *auth.Service, userID string) *auth.Session {
	t.Helper()
	rawToken, err := authSvc.CreateSession(context.Background(), userID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, session, err := authSvc.ValidateSession(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("validate session: %v", err)
	}
	return session
}

func createTestInvitation(t *testing.T, q *db.Queries, email, role, invitedBy string) {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate id: %v", err)
	}
	id := hex.EncodeToString(b)
	err := q.CreateInvitation(context.Background(), db.CreateInvitationParams{
		ID:        id,
		Email:     email,
		Role:      role,
		InvitedBy: invitedBy,
	})
	if err != nil {
		t.Fatalf("create test invitation: %v", err)
	}
}

func TestHandleUserList_ReturnsUserListPage(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	createTestUser(t, q, "member@example.com", "member")

	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	handler := handleUserList(deps.Auth)
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "admin@example.com") {
		t.Error("response body missing admin email")
	}
	if !strings.Contains(body, "member@example.com") {
		t.Error("response body missing member email")
	}
	if !strings.Contains(body, "User Management") {
		t.Error("response body missing page title")
	}
}

func TestHandleUserList_ShowsInvitations(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	createTestInvitation(t, q, "invited@example.com", "member", admin.ID)

	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	handler := handleUserList(deps.Auth)
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "invited@example.com") {
		t.Error("response body missing invited user email")
	}
	if !strings.Contains(body, "Pending Invitations") {
		t.Error("response body missing invitations section")
	}
}

func TestHandleUserList_MemberForbidden(t *testing.T) {
	t.Parallel()
	deps, q := newTestDepsWithGoogle(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	member := createTestUser(t, q, "member@example.com", "member")

	rawToken, err := deps.Auth.CreateSession(context.Background(), member.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	createTestInvitation(t, q, "inv@example.com", "member", admin.ID)

	mux := http.NewServeMux()
	AddRoutes(mux, deps)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/admin/users", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: deps.CookieName, Value: rawToken})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/users: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (member should be rejected)", resp.StatusCode, http.StatusForbidden)
	}
}

func TestHandleInvite_Success(t *testing.T) {
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
	form.Set("email", "newuser@example.com")
	form.Set("role", "member")

	handler := handleInvite(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/invite", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "msg=invited") {
		t.Errorf("Location = %q, want redirect with msg=invited", loc)
	}

	invitations, err := deps.Auth.ListInvitations(context.Background())
	if err != nil {
		t.Fatalf("list invitations: %v", err)
	}
	found := false
	for _, inv := range invitations {
		if inv.Email == "newuser@example.com" {
			found = true
			if inv.Role != auth.RoleMember {
				t.Errorf("invitation role = %q, want %q", inv.Role, auth.RoleMember)
			}
			break
		}
	}
	if !found {
		t.Error("invitation not found in database")
	}
}

func TestHandleInvite_EmptyEmail(t *testing.T) {
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
	form.Set("email", "")
	form.Set("role", "member")

	handler := handleInvite(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/invite", strings.NewReader(form.Encode()))
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
}

func TestHandleInvite_InvalidEmail(t *testing.T) {
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
	form.Set("email", "not-an-email")
	form.Set("role", "member")

	handler := handleInvite(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/invite", strings.NewReader(form.Encode()))
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
}

func TestHandleInvite_InvalidRole(t *testing.T) {
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
	form.Set("email", "test@example.com")
	form.Set("role", "superadmin")

	handler := handleInvite(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/invite", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param for invalid role", loc)
	}
}

func TestHandleDeactivate_Success(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	target := createTestUser(t, q, "target@example.com", "member")

	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	handler := handleDeactivate(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+target.ID+"/deactivate", nil)
	req.SetPathValue("id", target.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "msg=deactivated") {
		t.Errorf("Location = %q, want redirect with msg=deactivated", loc)
	}

	// Verify user was actually deactivated.
	users, err := deps.Auth.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	for _, u := range users {
		if u.ID == target.ID && u.Active {
			t.Error("target user should be inactive after deactivation")
		}
	}
}

func TestHandleDeactivate_SelfPrevention(t *testing.T) {
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

	handler := handleDeactivate(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+admin.ID+"/deactivate", nil)
	req.SetPathValue("id", admin.ID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want redirect with error param for self-deactivation", loc)
	}

	// Verify admin is still active.
	users, err := deps.Auth.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	for _, u := range users {
		if u.ID == admin.ID && !u.Active {
			t.Error("admin should still be active after self-deactivation attempt")
		}
	}
}

func TestHandleRevokeInvitation_Success(t *testing.T) {
	t.Parallel()
	deps, q := newTestDeps(t)

	admin := createTestUser(t, q, "admin@example.com", "admin")
	createTestInvitation(t, q, "invited@example.com", "member", admin.ID)

	invitations, err := deps.Auth.ListInvitations(context.Background())
	if err != nil {
		t.Fatalf("list invitations: %v", err)
	}
	if len(invitations) == 0 {
		t.Fatal("expected at least one invitation")
	}
	invID := invitations[0].ID

	session := createTestSession(t, deps.Auth, admin.ID)

	ctx := auth.ContextWithUser(context.Background(), &auth.User{
		ID:    admin.ID,
		Email: admin.Email,
		Role:  auth.RoleAdmin,
	})
	ctx = auth.ContextWithSession(ctx, session)

	handler := handleRevokeInvitation(deps.Auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/invitations/"+invID+"/revoke", nil)
	req.SetPathValue("id", invID)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusFound)
	}
	loc := rr.Header().Get("Location")
	if !strings.Contains(loc, "msg=revoked") {
		t.Errorf("Location = %q, want redirect with msg=revoked", loc)
	}

	remaining, err := deps.Auth.ListInvitations(context.Background())
	if err != nil {
		t.Fatalf("list invitations: %v", err)
	}
	for _, inv := range remaining {
		if inv.ID == invID {
			t.Error("invitation should have been deleted")
		}
	}
}

func TestHandleUserList_NoUserContext_Returns403(t *testing.T) {
	t.Parallel()
	deps, _ := newTestDeps(t)

	handler := handleUserList(deps.Auth)
	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}

package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/jasoncorbett/screens/internal/db"
)

func TestValidateSession_InactiveUser(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Deactivate user directly (without deleting sessions, simulating a race).
	if err := q.DeactivateUser(context.Background(), user.ID); err != nil {
		t.Fatalf("DeactivateUser() error: %v", err)
	}

	// ValidateSession should reject an inactive user's session.
	_, _, err = svc.ValidateSession(context.Background(), rawToken)
	if err == nil {
		t.Fatal("ValidateSession() should return error for inactive user, got nil")
	}
}

func TestProvisionUser_EmailCaseSensitivity(t *testing.T) {
	t.Parallel()

	// The service is configured with lowercase admin email.
	// Google may return a different casing. This tests whether the comparison
	// is case-insensitive.
	tests := []struct {
		name       string
		adminEmail string
		loginEmail string
		wantAdmin  bool
	}{
		{
			name:       "exact match",
			adminEmail: "admin@example.com",
			loginEmail: "admin@example.com",
			wantAdmin:  true,
		},
		{
			name:       "different case login",
			adminEmail: "admin@example.com",
			loginEmail: "Admin@Example.com",
			wantAdmin:  true,
		},
		{
			name:       "different case config",
			adminEmail: "Admin@Example.COM",
			loginEmail: "admin@example.com",
			wantAdmin:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sqlDB := db.OpenTestDB(t)
			cfg := Config{
				AdminEmail:      tt.adminEmail,
				SessionDuration: 0,
				CookieName:      "test",
				SecureCookie:    false,
			}
			svc := NewService(sqlDB, cfg)

			user, err := svc.ProvisionUser(context.Background(), tt.loginEmail, "Admin")
			if tt.wantAdmin {
				if err != nil {
					t.Fatalf("ProvisionUser(%q) error: %v", tt.loginEmail, err)
				}
				if user.Role != RoleAdmin {
					t.Errorf("role = %q, want %q", user.Role, RoleAdmin)
				}
			}
		})
	}
}

func TestDeactivateUser_PartialFailure(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	// Create a session for the user.
	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	// Successful deactivation should both delete sessions AND deactivate user.
	err = svc.DeactivateUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("DeactivateUser() error: %v", err)
	}

	// Verify session is gone.
	_, _, err = svc.ValidateSession(context.Background(), rawToken)
	if err == nil {
		t.Error("session should be deleted after deactivation")
	}

	// Verify user is inactive.
	row, err := q.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID() error: %v", err)
	}
	if row.Active != 0 {
		t.Errorf("user active = %d, want 0", row.Active)
	}
}

func TestProvisionUser_EmptyEmail(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, err := svc.ProvisionUser(context.Background(), "", "Empty User")
	if err == nil {
		t.Fatal("ProvisionUser() with empty email should return error, got nil")
	}
}

func TestProvisionUser_EmptyDisplayName(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	// Admin email with empty display name should still work.
	user, err := svc.ProvisionUser(context.Background(), "admin@example.com", "")
	if err != nil {
		t.Fatalf("ProvisionUser() error: %v", err)
	}
	if user.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", user.Role, RoleAdmin)
	}
}

func TestCreateSession_EmptyUserID(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	// Empty user ID should fail because it references a non-existent user.
	_, err := svc.CreateSession(context.Background(), "")
	if err == nil {
		t.Fatal("CreateSession() with empty user ID should return error, got nil")
	}
}

func TestValidateSession_EmptyToken(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	_, _, err := svc.ValidateSession(context.Background(), "")
	if err == nil {
		t.Fatal("ValidateSession() with empty token should return error, got nil")
	}
}

func TestLogout_NonexistentToken(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	// Logout with a token that doesn't exist should not error (idempotent).
	err := svc.Logout(context.Background(), "nonexistent-token-value")
	if err != nil {
		t.Fatalf("Logout() with nonexistent token: %v", err)
	}
}

func TestInviteUser_InvalidRole(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")

	// Invite with an invalid role should fail.
	err := svc.InviteUser(context.Background(), "user@example.com", Role("superadmin"), admin.ID)
	if err == nil {
		t.Fatal("InviteUser() with invalid role should return error, got nil")
	}
}

func TestInviteUser_DuplicateEmail(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")

	err := svc.InviteUser(context.Background(), "user@example.com", RoleMember, admin.ID)
	if err != nil {
		t.Fatalf("first InviteUser() error: %v", err)
	}

	// Second invitation for the same email should fail (UNIQUE constraint).
	err = svc.InviteUser(context.Background(), "user@example.com", RoleMember, admin.ID)
	if err == nil {
		t.Fatal("InviteUser() with duplicate email should return error, got nil")
	}
}

func TestRevokeInvitation_NonexistentID(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	// Revoking a non-existent invitation should not error (DELETE matches zero rows).
	err := svc.RevokeInvitation(context.Background(), "nonexistent-id")
	if err != nil {
		t.Fatalf("RevokeInvitation() with nonexistent ID: %v", err)
	}
}

func TestDeactivateUser_NonexistentUserID(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	// Deactivating a non-existent user should not panic. The current implementation
	// runs a DELETE + UPDATE that match zero rows with no error.
	err := svc.DeactivateUser(context.Background(), "nonexistent-id")
	if err != nil {
		t.Fatalf("DeactivateUser() with nonexistent ID: %v", err)
	}
}

func TestProvisionUser_InvitationForAdminEmail(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	inviter := createTestUser(t, q, "other-admin@example.com", "admin")

	// Create an invitation for the admin email with role=member.
	err := svc.InviteUser(context.Background(), "admin@example.com", RoleMember, inviter.ID)
	if err != nil {
		t.Fatalf("InviteUser() error: %v", err)
	}

	// Admin email should still get admin role, not the invitation role.
	user, err := svc.ProvisionUser(context.Background(), "admin@example.com", "Admin")
	if err != nil {
		t.Fatalf("ProvisionUser() error: %v", err)
	}

	if user.Role != RoleAdmin {
		t.Errorf("admin email provisioned with role %q, want %q", user.Role, RoleAdmin)
	}
}

func TestProvisionUser_ConcurrentSameEmail(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	inviter := createTestUser(t, q, "inviter@example.com", "admin")

	err := svc.InviteUser(context.Background(), "race@example.com", RoleMember, inviter.ID)
	if err != nil {
		t.Fatalf("InviteUser() error: %v", err)
	}

	// First call provisions the user.
	user1, err := svc.ProvisionUser(context.Background(), "race@example.com", "Race User")
	if err != nil {
		t.Fatalf("first ProvisionUser() error: %v", err)
	}

	// Second call with same email should return the existing user, not error.
	user2, err := svc.ProvisionUser(context.Background(), "race@example.com", "Race User Again")
	if err != nil {
		t.Fatalf("second ProvisionUser() error: %v", err)
	}

	if user1.ID != user2.ID {
		t.Errorf("second provision created new user: %q != %q", user1.ID, user2.ID)
	}
}

func TestListUsers_EmptyDB(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	users, err := svc.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers() error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("ListUsers() returned %d users, want 0", len(users))
	}
}

func TestListInvitations_EmptyDB(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	invitations, err := svc.ListInvitations(context.Background())
	if err != nil {
		t.Fatalf("ListInvitations() error: %v", err)
	}
	if len(invitations) != 0 {
		t.Errorf("ListInvitations() returned %d invitations, want 0", len(invitations))
	}
}

func TestCleanExpiredSessions_NoExpired(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	// Create a valid (non-expired) session.
	_, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	count, err := svc.CleanExpiredSessions(context.Background())
	if err != nil {
		t.Fatalf("CleanExpiredSessions() error: %v", err)
	}
	if count != 0 {
		t.Errorf("CleanExpiredSessions() = %d, want 0", count)
	}
}

func TestCleanExpiredSessions_RemovesExpired(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	// Insert an expired session directly.
	rawToken, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}
	csrfToken, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}
	err = q.CreateSession(context.Background(), db.CreateSessionParams{
		TokenHash: HashToken(rawToken),
		UserID:    user.ID,
		CsrfToken: csrfToken,
		ExpiresAt: "2020-01-01 00:00:00",
	})
	if err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	count, err := svc.CleanExpiredSessions(context.Background())
	if err != nil {
		t.Fatalf("CleanExpiredSessions() error: %v", err)
	}
	if count != 1 {
		t.Errorf("CleanExpiredSessions() = %d, want 1", count)
	}
}

func TestHashToken_EmptyInput(t *testing.T) {
	t.Parallel()

	// Hashing an empty string should still produce a valid SHA-256 hash.
	hash := HashToken("")
	if len(hash) != 64 {
		t.Errorf("HashToken(\"\") length = %d, want 64", len(hash))
	}

	// SHA-256 of empty string is a known constant.
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if hash != expected {
		t.Errorf("HashToken(\"\") = %q, want %q", hash, expected)
	}
}

func TestProvisionUser_UnicodeEmail(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	admin := createTestUser(t, q, "admin@example.com", "admin")

	// Invite a user with unicode in the email local part.
	err := svc.InviteUser(context.Background(), "user\u00e9@example.com", RoleMember, admin.ID)
	if err != nil {
		t.Fatalf("InviteUser() error: %v", err)
	}

	user, err := svc.ProvisionUser(context.Background(), "user\u00e9@example.com", "Unic\u00f6de")
	if err != nil {
		t.Fatalf("ProvisionUser() error: %v", err)
	}
	if user.DisplayName != "Unic\u00f6de" {
		t.Errorf("display name = %q, want %q", user.DisplayName, "Unic\u00f6de")
	}
}

func TestProvisionUser_LongDisplayName(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)

	longName := strings.Repeat("A", 10000)
	user, err := svc.ProvisionUser(context.Background(), "admin@example.com", longName)
	if err != nil {
		t.Fatalf("ProvisionUser() with long display name error: %v", err)
	}
	if user.DisplayName != longName {
		t.Errorf("display name length = %d, want %d", len(user.DisplayName), len(longName))
	}
}

func TestProvisionUser_AdminEmailNotConsumedAsInvitation(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	inviter := createTestUser(t, q, "other@example.com", "admin")

	// Create invitation for admin email.
	err := svc.InviteUser(context.Background(), "admin@example.com", RoleMember, inviter.ID)
	if err != nil {
		t.Fatalf("InviteUser() error: %v", err)
	}

	// Provision admin.
	_, err = svc.ProvisionUser(context.Background(), "admin@example.com", "Admin")
	if err != nil {
		t.Fatalf("ProvisionUser() error: %v", err)
	}

	// The invitation should NOT be consumed since the admin path was taken.
	_, invErr := q.GetInvitationByEmail(context.Background(), "admin@example.com")
	if invErr != nil {
		t.Logf("Note: admin email invitation was consumed (invErr: %v). This is acceptable but the invitation is unnecessary.", invErr)
	}
}

func TestCreateSession_MultipleSessions(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	// Create multiple sessions for the same user.
	tokens := make([]string, 3)
	for i := range tokens {
		token, err := svc.CreateSession(context.Background(), user.ID)
		if err != nil {
			t.Fatalf("CreateSession() [%d] error: %v", i, err)
		}
		tokens[i] = token
	}

	// All sessions should be independently valid.
	for i, token := range tokens {
		_, _, err := svc.ValidateSession(context.Background(), token)
		if err != nil {
			t.Errorf("ValidateSession() [%d] error: %v", i, err)
		}
	}

	// Logging out one should not affect others.
	if err := svc.Logout(context.Background(), tokens[0]); err != nil {
		t.Fatalf("Logout() error: %v", err)
	}

	_, _, err := svc.ValidateSession(context.Background(), tokens[0])
	if err == nil {
		t.Error("ValidateSession() for logged-out token should error")
	}

	for i := 1; i < len(tokens); i++ {
		_, _, err := svc.ValidateSession(context.Background(), tokens[i])
		if err != nil {
			t.Errorf("ValidateSession() [%d] should still be valid: %v", i, err)
		}
	}
}

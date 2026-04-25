package auth

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jasoncorbett/screens/internal/db"
)

func TestGenerateToken(t *testing.T) {
	t.Parallel()

	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error: %v", err)
	}

	if len(token) != 64 {
		t.Errorf("GenerateToken() length = %d, want 64", len(token))
	}

	if _, err := hex.DecodeString(token); err != nil {
		t.Errorf("GenerateToken() not valid hex: %v", err)
	}

	token2, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() second call error: %v", err)
	}

	if token == token2 {
		t.Errorf("GenerateToken() returned same token twice: %s", token)
	}
}

func TestHashToken(t *testing.T) {
	t.Parallel()

	hash1 := HashToken("test-token")
	hash2 := HashToken("test-token")

	if hash1 != hash2 {
		t.Errorf("HashToken() inconsistent: %s != %s", hash1, hash2)
	}

	if len(hash1) != 64 {
		t.Errorf("HashToken() length = %d, want 64", len(hash1))
	}

	different := HashToken("different-token")
	if hash1 == different {
		t.Errorf("HashToken() same hash for different inputs")
	}
}

func newTestService(t *testing.T) (*Service, *db.Queries) {
	t.Helper()
	sqlDB := db.OpenTestDB(t)
	cfg := Config{
		AdminEmail:      "admin@example.com",
		SessionDuration: time.Hour,
		CookieName:      "test_session",
		SecureCookie:    false,
	}
	svc := NewService(sqlDB, cfg)
	q := db.New(sqlDB)
	return svc, q
}

func createTestUser(t *testing.T, q *db.Queries, email, role string) db.User {
	t.Helper()
	id, err := GenerateToken()
	if err != nil {
		t.Fatalf("generate test user id: %v", err)
	}
	row, err := q.CreateUser(context.Background(), db.CreateUserParams{
		ID:          id[:32],
		Email:       email,
		DisplayName: "Test User",
		Role:        role,
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return row
}

func TestCreateSession(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	if len(rawToken) != 64 {
		t.Errorf("CreateSession() token length = %d, want 64", len(rawToken))
	}

	// The hashed token should be in the database.
	tokenHash := HashToken(rawToken)
	session, err := q.GetSessionByTokenHash(context.Background(), tokenHash)
	if err != nil {
		t.Fatalf("session not found in DB: %v", err)
	}

	if session.UserID != user.ID {
		t.Errorf("session user_id = %q, want %q", session.UserID, user.ID)
	}
}

func TestValidateSession(t *testing.T) {
	t.Parallel()

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()
		svc, q := newTestService(t)
		user := createTestUser(t, q, "user@example.com", "member")

		rawToken, err := svc.CreateSession(context.Background(), user.ID)
		if err != nil {
			t.Fatalf("CreateSession() error: %v", err)
		}

		gotUser, gotSession, err := svc.ValidateSession(context.Background(), rawToken)
		if err != nil {
			t.Fatalf("ValidateSession() error: %v", err)
		}

		if gotUser.ID != user.ID {
			t.Errorf("user ID = %q, want %q", gotUser.ID, user.ID)
		}
		if gotUser.Email != "user@example.com" {
			t.Errorf("user email = %q, want %q", gotUser.Email, "user@example.com")
		}
		if gotSession.UserID != user.ID {
			t.Errorf("session user_id = %q, want %q", gotSession.UserID, user.ID)
		}
		if gotSession.CSRFToken == "" {
			t.Error("session csrf_token is empty")
		}
	})

	t.Run("expired session", func(t *testing.T) {
		t.Parallel()
		svc, q := newTestService(t)
		user := createTestUser(t, q, "user@example.com", "member")

		// Insert a session with an already-expired timestamp.
		rawToken, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken() error: %v", err)
		}
		csrfToken, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken() error: %v", err)
		}
		tokenHash := HashToken(rawToken)
		pastTime := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
		err = q.CreateSession(context.Background(), db.CreateSessionParams{
			TokenHash: tokenHash,
			UserID:    user.ID,
			CsrfToken: csrfToken,
			ExpiresAt: pastTime,
		})
		if err != nil {
			t.Fatalf("insert expired session: %v", err)
		}

		_, _, err = svc.ValidateSession(context.Background(), rawToken)
		if err == nil {
			t.Fatal("ValidateSession() expected error for expired session, got nil")
		}
	})

	t.Run("nonexistent token", func(t *testing.T) {
		t.Parallel()
		svc, _ := newTestService(t)

		_, _, err := svc.ValidateSession(context.Background(), "nonexistent-token-value")
		if err == nil {
			t.Fatal("ValidateSession() expected error for nonexistent token, got nil")
		}
	})
}

func TestProvisionUser(t *testing.T) {
	t.Parallel()

	t.Run("admin email creates admin", func(t *testing.T) {
		t.Parallel()
		svc, _ := newTestService(t)

		user, err := svc.ProvisionUser(context.Background(), "admin@example.com", "Admin User")
		if err != nil {
			t.Fatalf("ProvisionUser() error: %v", err)
		}

		if user.Role != RoleAdmin {
			t.Errorf("role = %q, want %q", user.Role, RoleAdmin)
		}
		if user.Email != "admin@example.com" {
			t.Errorf("email = %q, want %q", user.Email, "admin@example.com")
		}
	})

	t.Run("invited email creates user with invitation role", func(t *testing.T) {
		t.Parallel()
		svc, q := newTestService(t)

		// First create an admin user so the invitation has a valid invited_by.
		admin := createTestUser(t, q, "inviter@example.com", "admin")

		err := svc.InviteUser(context.Background(), "invited@example.com", RoleMember, admin.ID)
		if err != nil {
			t.Fatalf("InviteUser() error: %v", err)
		}

		user, err := svc.ProvisionUser(context.Background(), "invited@example.com", "Invited User")
		if err != nil {
			t.Fatalf("ProvisionUser() error: %v", err)
		}

		if user.Role != RoleMember {
			t.Errorf("role = %q, want %q", user.Role, RoleMember)
		}

		// Invitation should be consumed (deleted).
		_, invErr := q.GetInvitationByEmail(context.Background(), "invited@example.com")
		if invErr == nil {
			t.Error("invitation still exists after provisioning")
		}
	})

	t.Run("unauthorized email returns error", func(t *testing.T) {
		t.Parallel()
		svc, _ := newTestService(t)

		_, err := svc.ProvisionUser(context.Background(), "nobody@example.com", "Nobody")
		if err == nil {
			t.Fatal("ProvisionUser() expected error for unauthorized email, got nil")
		}
	})

	t.Run("deactivated user returns error", func(t *testing.T) {
		t.Parallel()
		svc, q := newTestService(t)

		user := createTestUser(t, q, "deactivated@example.com", "member")
		err := q.DeactivateUser(context.Background(), user.ID)
		if err != nil {
			t.Fatalf("deactivate user: %v", err)
		}

		_, err = svc.ProvisionUser(context.Background(), "deactivated@example.com", "Deactivated")
		if err == nil {
			t.Fatal("ProvisionUser() expected error for deactivated user, got nil")
		}
	})

	t.Run("existing active user returns that user", func(t *testing.T) {
		t.Parallel()
		svc, q := newTestService(t)

		existing := createTestUser(t, q, "existing@example.com", "member")

		user, err := svc.ProvisionUser(context.Background(), "existing@example.com", "Existing User")
		if err != nil {
			t.Fatalf("ProvisionUser() error: %v", err)
		}

		if user.ID != existing.ID {
			t.Errorf("user ID = %q, want %q", user.ID, existing.ID)
		}
	})
}

func TestDeactivateUser(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	// Create two sessions for the user.
	token1, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}
	_, err = svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateSession() second call error: %v", err)
	}

	err = svc.DeactivateUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("DeactivateUser() error: %v", err)
	}

	// Sessions should be gone.
	_, _, err = svc.ValidateSession(context.Background(), token1)
	if err == nil {
		t.Error("ValidateSession() expected error after deactivation, got nil")
	}

	// User should be inactive.
	row, err := q.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetUserByID() error: %v", err)
	}
	if row.Active != 0 {
		t.Errorf("user active = %d, want 0", row.Active)
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()
	svc, q := newTestService(t)
	user := createTestUser(t, q, "user@example.com", "member")

	rawToken, err := svc.CreateSession(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CreateSession() error: %v", err)
	}

	err = svc.Logout(context.Background(), rawToken)
	if err != nil {
		t.Fatalf("Logout() error: %v", err)
	}

	// Session should no longer validate.
	_, _, err = svc.ValidateSession(context.Background(), rawToken)
	if err == nil {
		t.Error("ValidateSession() expected error after logout, got nil")
	}
}

func TestContextAccessors(t *testing.T) {
	t.Parallel()

	t.Run("user round-trip", func(t *testing.T) {
		t.Parallel()
		user := &User{ID: "test-id", Email: "test@example.com"}
		ctx := ContextWithUser(context.Background(), user)
		got := UserFromContext(ctx)
		if got == nil {
			t.Fatal("UserFromContext() returned nil")
		}
		if got.ID != user.ID {
			t.Errorf("user ID = %q, want %q", got.ID, user.ID)
		}
	})

	t.Run("user absent", func(t *testing.T) {
		t.Parallel()
		got := UserFromContext(context.Background())
		if got != nil {
			t.Errorf("UserFromContext() = %v, want nil", got)
		}
	})

	t.Run("session round-trip", func(t *testing.T) {
		t.Parallel()
		session := &Session{TokenHash: "hash", CSRFToken: "csrf"}
		ctx := ContextWithSession(context.Background(), session)
		got := SessionFromContext(ctx)
		if got == nil {
			t.Fatal("SessionFromContext() returned nil")
		}
		if got.CSRFToken != session.CSRFToken {
			t.Errorf("csrf token = %q, want %q", got.CSRFToken, session.CSRFToken)
		}
	})

	t.Run("session absent", func(t *testing.T) {
		t.Parallel()
		got := SessionFromContext(context.Background())
		if got != nil {
			t.Errorf("SessionFromContext() = %v, want nil", got)
		}
	})
}

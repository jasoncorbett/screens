package db

import (
	"context"
	"testing"
)

// TestUsersTable_EmailUniqueness verifies that the UNIQUE constraint on users.email
// rejects a second insert with the same email address.
func TestUsersTable_EmailUniqueness(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	_, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "user-1",
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}

	_, err = q.CreateUser(ctx, CreateUserParams{
		ID:          "user-2",
		Email:       "alice@example.com",
		DisplayName: "Alice Duplicate",
		Role:        "member",
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation for duplicate email, got nil")
	}
}

// TestUsersTable_RoleCheckConstraint verifies the CHECK constraint on users.role
// rejects values other than 'admin' and 'member'.
func TestUsersTable_RoleCheckConstraint(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	tests := []struct {
		name    string
		role    string
		wantErr bool
	}{
		{name: "admin accepted", role: "admin", wantErr: false},
		{name: "member accepted", role: "member", wantErr: false},
		{name: "superadmin rejected", role: "superadmin", wantErr: true},
		{name: "empty rejected", role: "", wantErr: true},
		{name: "ADMIN uppercase rejected", role: "ADMIN", wantErr: true},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := q.CreateUser(ctx, CreateUserParams{
				ID:          "role-test-" + tt.role + "-" + string(rune('0'+i)),
				Email:       tt.role + string(rune('0'+i)) + "@test.com",
				DisplayName: "Test",
				Role:        tt.role,
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateUser(role=%q) error = %v, wantErr %v", tt.role, err, tt.wantErr)
			}
		})
	}
}

// TestSessionsTable_ForeignKeyToUsers verifies that sessions.user_id must
// reference an existing user; inserting a session for a nonexistent user fails.
func TestSessionsTable_ForeignKeyToUsers(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	err := q.CreateSession(ctx, CreateSessionParams{
		TokenHash: "hash-no-user",
		UserID:    "nonexistent-user-id",
		CsrfToken: "csrf-token",
		ExpiresAt: "2099-12-31T23:59:59Z",
	})
	if err == nil {
		t.Fatal("expected foreign key violation for nonexistent user_id, got nil")
	}
}

// TestSessionsTable_CascadeDeleteOnUserRemoval verifies that deleting a user
// also deletes all their sessions (ON DELETE CASCADE).
func TestSessionsTable_CascadeDeleteOnUserRemoval(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "cascade-user",
		Email:       "cascade@example.com",
		DisplayName: "Cascade Test",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	err = q.CreateSession(ctx, CreateSessionParams{
		TokenHash: "session-hash-1",
		UserID:    user.ID,
		CsrfToken: "csrf-1",
		ExpiresAt: "2099-12-31T23:59:59Z",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Delete the user directly via SQL (simulating hard delete).
	if _, err := database.Exec("DELETE FROM users WHERE id = ?", user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	// Session should be gone due to cascade.
	_, err = q.GetSessionByTokenHash(ctx, "session-hash-1")
	if err == nil {
		t.Fatal("expected session to be deleted by cascade, but it still exists")
	}
}

// TestInvitationsTable_EmailUniqueness verifies that duplicate invitation emails
// are rejected by the UNIQUE constraint.
func TestInvitationsTable_EmailUniqueness(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	// Need a user first (invited_by foreign key).
	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "admin-inviter",
		Email:       "admin@example.com",
		DisplayName: "Admin",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	err = q.CreateInvitation(ctx, CreateInvitationParams{
		ID:        "inv-1",
		Email:     "invited@example.com",
		Role:      "member",
		InvitedBy: user.ID,
	})
	if err != nil {
		t.Fatalf("create first invitation: %v", err)
	}

	err = q.CreateInvitation(ctx, CreateInvitationParams{
		ID:        "inv-2",
		Email:     "invited@example.com",
		Role:      "member",
		InvitedBy: user.ID,
	})
	if err == nil {
		t.Fatal("expected UNIQUE constraint violation for duplicate invitation email, got nil")
	}
}

// TestInvitationsTable_RoleCheckConstraint verifies the CHECK constraint on
// invitations.role rejects invalid role values.
func TestInvitationsTable_RoleCheckConstraint(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "admin-role-check",
		Email:       "admin-rolecheck@example.com",
		DisplayName: "Admin",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	err = q.CreateInvitation(ctx, CreateInvitationParams{
		ID:        "inv-bad-role",
		Email:     "badrole@example.com",
		Role:      "superadmin",
		InvitedBy: user.ID,
	})
	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid invitation role, got nil")
	}
}

// TestInvitationsTable_ForeignKeyToUsers verifies that invitations.invited_by
// must reference an existing user.
func TestInvitationsTable_ForeignKeyToUsers(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	err := q.CreateInvitation(ctx, CreateInvitationParams{
		ID:        "inv-no-user",
		Email:     "orphan@example.com",
		Role:      "member",
		InvitedBy: "nonexistent-user",
	})
	if err == nil {
		t.Fatal("expected foreign key violation for nonexistent invited_by user, got nil")
	}
}

// TestUsersTable_UnicodeEmail verifies that unicode characters in user fields
// are stored and retrieved correctly.
func TestUsersTable_UnicodeEmail(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	unicodeName := "\u00e9\u00e8\u00ea\u00eb \u4e16\u754c \U0001f600"
	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "unicode-user",
		Email:       "unicode@example.com",
		DisplayName: unicodeName,
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("create user with unicode display name: %v", err)
	}
	if user.DisplayName != unicodeName {
		t.Errorf("display_name = %q, want %q", user.DisplayName, unicodeName)
	}

	// Verify round-trip through GetUserByID.
	got, err := q.GetUserByID(ctx, "unicode-user")
	if err != nil {
		t.Fatalf("get user by id: %v", err)
	}
	if got.DisplayName != unicodeName {
		t.Errorf("after round-trip: display_name = %q, want %q", got.DisplayName, unicodeName)
	}
}

// TestCountActiveAdmins_EmptyDatabase verifies CountActiveAdmins returns 0
// on an empty users table (important for first-run logic).
func TestCountActiveAdmins_EmptyDatabase(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	count, err := q.CountActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("CountActiveAdmins: %v", err)
	}
	if count != 0 {
		t.Errorf("CountActiveAdmins on empty db = %d, want 0", count)
	}
}

// TestCountActiveAdmins_ExcludesDeactivated verifies that deactivated admins
// are not counted.
func TestCountActiveAdmins_ExcludesDeactivated(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	_, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "admin-active",
		Email:       "active-admin@example.com",
		DisplayName: "Active Admin",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create active admin: %v", err)
	}

	_, err = q.CreateUser(ctx, CreateUserParams{
		ID:          "admin-deactivated",
		Email:       "deactivated@example.com",
		DisplayName: "Deactivated Admin",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create admin to deactivate: %v", err)
	}

	if err := q.DeactivateUser(ctx, "admin-deactivated"); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	count, err := q.CountActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("CountActiveAdmins: %v", err)
	}
	if count != 1 {
		t.Errorf("CountActiveAdmins = %d, want 1 (deactivated admin should not count)", count)
	}
}

// TestDeactivateUser_NonexistentID verifies that deactivating a nonexistent
// user does not return an error (silent no-op, which is correct for :exec).
func TestDeactivateUser_NonexistentID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	err := q.DeactivateUser(ctx, "does-not-exist")
	if err != nil {
		t.Errorf("DeactivateUser for nonexistent id returned error: %v", err)
	}
}

// TestDeleteExpiredSessions verifies the cleanup query removes only expired
// sessions and keeps valid ones.
func TestDeleteExpiredSessions(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "session-user",
		Email:       "session@example.com",
		DisplayName: "Session User",
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Create an expired session.
	err = q.CreateSession(ctx, CreateSessionParams{
		TokenHash: "expired-hash",
		UserID:    user.ID,
		CsrfToken: "csrf-expired",
		ExpiresAt: "2020-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("create expired session: %v", err)
	}

	// Create a valid session.
	err = q.CreateSession(ctx, CreateSessionParams{
		TokenHash: "valid-hash",
		UserID:    user.ID,
		CsrfToken: "csrf-valid",
		ExpiresAt: "2099-12-31T23:59:59Z",
	})
	if err != nil {
		t.Fatalf("create valid session: %v", err)
	}

	// Run cleanup.
	if err := q.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}

	// Expired session should be gone.
	_, err = q.GetSessionByTokenHash(ctx, "expired-hash")
	if err == nil {
		t.Error("expired session still exists after DeleteExpiredSessions")
	}

	// Valid session should still exist.
	_, err = q.GetSessionByTokenHash(ctx, "valid-hash")
	if err != nil {
		t.Errorf("valid session was deleted by DeleteExpiredSessions: %v", err)
	}
}

// TestDevicesTable_ExistsAfterMigration verifies the devices table is created
// by migration 005, and that the unique index on token_hash exists. The unique
// index is what guarantees a token-generation collision becomes a hard error
// rather than a silent partition.
func TestDevicesTable_ExistsAfterMigration(t *testing.T) {
	database := OpenTestDB(t)

	var name string
	err := database.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='devices'").Scan(&name)
	if err != nil {
		t.Fatalf("devices table not created by migration: %v", err)
	}
	if name != "devices" {
		t.Errorf("table name = %q, want %q", name, "devices")
	}

	// The UNIQUE constraint on token_hash creates an auto-index named
	// sqlite_autoindex_devices_1; the explicit named index for lookups is
	// idx_devices_token_hash. Verify the named index is present.
	var idxName string
	err = database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_devices_token_hash'",
	).Scan(&idxName)
	if err != nil {
		t.Fatalf("idx_devices_token_hash not created by migration: %v", err)
	}

	// And the revoked_at filter index.
	err = database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_devices_revoked_at'",
	).Scan(&idxName)
	if err != nil {
		t.Fatalf("idx_devices_revoked_at not created by migration: %v", err)
	}
}

// TestDevicesTable_TokenHashUniqueness verifies that the UNIQUE constraint on
// devices.token_hash rejects a second insert with the same hash. This is the
// schema-level guarantee that a (catastrophic) collision in the token generator
// surfaces as a database integrity error rather than letting two devices share
// a token.
func TestDevicesTable_TokenHashUniqueness(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "device-creator",
		Email:       "creator@example.com",
		DisplayName: "Creator",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "dev-1",
		Name:      "Kitchen",
		TokenHash: "shared-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("create first device: %v", err)
	}

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "dev-2",
		Name:      "Living Room",
		TokenHash: "shared-hash",
		CreatedBy: user.ID,
	}); err == nil {
		t.Fatal("expected UNIQUE constraint violation for duplicate token_hash, got nil")
	}
}

// TestDevicesTable_ForeignKeyRestrictsUserDelete verifies that ON DELETE
// RESTRICT on devices.created_by prevents deletion of a user who still owns a
// device. This is the inverse of the cascade pattern used elsewhere -- losing
// device records by deleting an admin would silently shrink the device fleet.
func TestDevicesTable_ForeignKeyRestrictsUserDelete(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "owner",
		Email:       "owner@example.com",
		DisplayName: "Owner",
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "owned-device",
		Name:      "Owned",
		TokenHash: "owned-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("create device: %v", err)
	}

	if _, err := database.Exec("DELETE FROM users WHERE id = ?", user.ID); err == nil {
		t.Fatal("expected RESTRICT to prevent deleting a user who owns a device, got nil")
	}
}

// TestDeleteSessionsByUserID verifies bulk session deletion by user ID.
func TestDeleteSessionsByUserID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, CreateUserParams{
		ID:          "multi-session-user",
		Email:       "multi@example.com",
		DisplayName: "Multi Session",
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Create two sessions for the same user.
	for _, hash := range []string{"hash-a", "hash-b"} {
		err := q.CreateSession(ctx, CreateSessionParams{
			TokenHash: hash,
			UserID:    user.ID,
			CsrfToken: "csrf-" + hash,
			ExpiresAt: "2099-12-31T23:59:59Z",
		})
		if err != nil {
			t.Fatalf("create session %s: %v", hash, err)
		}
	}

	if err := q.DeleteSessionsByUserID(ctx, user.ID); err != nil {
		t.Fatalf("DeleteSessionsByUserID: %v", err)
	}

	for _, hash := range []string{"hash-a", "hash-b"} {
		_, err := q.GetSessionByTokenHash(ctx, hash)
		if err == nil {
			t.Errorf("session %s still exists after DeleteSessionsByUserID", hash)
		}
	}
}

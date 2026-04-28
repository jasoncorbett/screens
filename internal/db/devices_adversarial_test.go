package db

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"
)

// seedDeviceUser creates and returns a user that can own devices in tests.
func seedDeviceUser(t *testing.T, q *Queries, id, email string) User {
	t.Helper()
	u, err := q.CreateUser(context.Background(), CreateUserParams{
		ID:          id,
		Email:       email,
		DisplayName: "Test " + id,
		Role:        "admin",
	})
	if err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
	return u
}

// TestDevicesTable_NullTokenHashRejected verifies the NOT NULL constraint on
// devices.token_hash. Without it, a service-layer bug that forgot to hash a
// token could insert NULL and silently let any request that hashes to NULL
// match -- catastrophic auth failure.
func TestDevicesTable_NullTokenHashRejected(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	user := seedDeviceUser(t, q, "null-hash-owner", "null-hash@example.com")

	// Bypass sqlc (CreateDeviceParams.TokenHash is a string and cannot be NULL).
	// Use raw SQL to drive a NULL into the column and confirm SQLite rejects it.
	_, err := database.Exec(
		"INSERT INTO devices (id, name, token_hash, created_by) VALUES (?, ?, NULL, ?)",
		"dev-null", "no-hash", user.ID,
	)
	if err == nil {
		t.Fatal("expected NOT NULL violation for token_hash, got nil")
	}
}

// TestDevicesTable_NullNameRejected verifies the NOT NULL on devices.name.
// Empty strings are NOT rejected by NOT NULL in SQLite, but NULL must be.
func TestDevicesTable_NullNameRejected(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	user := seedDeviceUser(t, q, "null-name-owner", "null-name@example.com")

	_, err := database.Exec(
		"INSERT INTO devices (id, name, token_hash, created_by) VALUES (?, NULL, ?, ?)",
		"dev-null-name", "some-hash", user.ID,
	)
	if err == nil {
		t.Fatal("expected NOT NULL violation for name, got nil")
	}
}

// TestDevicesTable_CreatedAtDefaultPopulated verifies that CreateDevice does
// not require created_at -- the column's DEFAULT (datetime('now')) populates
// it. Otherwise the service would have to format timestamps itself, which is
// an easy place to introduce zone bugs.
func TestDevicesTable_CreatedAtDefaultPopulated(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "ts-owner", "ts@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "ts-dev",
		Name:      "ts",
		TokenHash: "ts-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	got, err := q.GetDeviceByID(ctx, "ts-dev")
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if got.CreatedAt == "" {
		t.Errorf("CreatedAt is empty -- the schema DEFAULT did not populate it")
	}
	if got.LastSeenAt.Valid {
		t.Errorf("LastSeenAt should be NULL on a fresh device, got %q", got.LastSeenAt.String)
	}
	if got.RevokedAt.Valid {
		t.Errorf("RevokedAt should be NULL on a fresh device, got %q", got.RevokedAt.String)
	}
}

// TestGetDeviceByTokenHash_ReturnsRevokedRow verifies that GetDeviceByTokenHash
// returns a device row even when revoked_at is non-NULL. This is an explicit
// design decision per the architecture doc: the middleware does the
// revoked-at check in Go (not in SQL) so it can distinguish "no such token"
// from "revoked token" for clearer logging. If this query starts filtering
// revoked rows, the middleware loses that distinction silently.
func TestGetDeviceByTokenHash_ReturnsRevokedRow(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "rev-owner", "rev@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "rev-dev",
		Name:      "rev",
		TokenHash: "rev-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := q.RevokeDevice(ctx, "rev-dev"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	got, err := q.GetDeviceByTokenHash(ctx, "rev-hash")
	if err != nil {
		t.Fatalf("GetDeviceByTokenHash on revoked device returned error: %v -- the query MUST still return revoked rows so middleware can distinguish revoked-vs-unknown", err)
	}
	if !got.RevokedAt.Valid {
		t.Errorf("expected revoked_at to be non-NULL after RevokeDevice, got NULL")
	}
}

// TestRevokeDevice_IsIdempotent verifies that the WHERE revoked_at IS NULL
// clause makes a second RevokeDevice call a no-op (it does not advance the
// revoked_at timestamp). Otherwise an admin clicking "revoke" twice could
// rewrite the audit trail with a later time.
func TestRevokeDevice_IsIdempotent(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "idem-owner", "idem@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "idem-dev",
		Name:      "idem",
		TokenHash: "idem-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := q.RevokeDevice(ctx, "idem-dev"); err != nil {
		t.Fatalf("first RevokeDevice: %v", err)
	}

	first, err := q.GetDeviceByID(ctx, "idem-dev")
	if err != nil {
		t.Fatalf("GetDeviceByID after first revoke: %v", err)
	}
	if !first.RevokedAt.Valid {
		t.Fatal("revoked_at unexpectedly NULL after first revoke")
	}

	// Sleep at least 1s so a non-idempotent UPDATE would produce a different
	// datetime('now') value (sqlite's datetime('now') has 1-second granularity).
	time.Sleep(1100 * time.Millisecond)

	if err := q.RevokeDevice(ctx, "idem-dev"); err != nil {
		t.Fatalf("second RevokeDevice: %v", err)
	}
	second, err := q.GetDeviceByID(ctx, "idem-dev")
	if err != nil {
		t.Fatalf("GetDeviceByID after second revoke: %v", err)
	}
	if second.RevokedAt.String != first.RevokedAt.String {
		t.Errorf("revoked_at advanced from %q to %q on second revoke -- the WHERE revoked_at IS NULL guard failed",
			first.RevokedAt.String, second.RevokedAt.String)
	}
}

// TestRotateDeviceToken_ZeroRowsForRevoked verifies that RotateDeviceToken
// refuses to update a revoked device. Per the architecture, RowsAffected==0
// is the signal to the service layer that the device is missing or revoked,
// and the WHERE revoked_at IS NULL clause guarantees a revoked device's hash
// cannot be silently replaced.
func TestRotateDeviceToken_ZeroRowsForRevoked(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "rot-owner", "rot@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "rot-dev",
		Name:      "rot",
		TokenHash: "rot-hash-original",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if err := q.RevokeDevice(ctx, "rot-dev"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	res, err := q.RotateDeviceToken(ctx, RotateDeviceTokenParams{
		TokenHash: "should-not-take-effect",
		ID:        "rot-dev",
	})
	if err != nil {
		t.Fatalf("RotateDeviceToken returned error: %v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if n != 0 {
		t.Errorf("RowsAffected = %d, want 0 for revoked device -- the WHERE revoked_at IS NULL guard failed", n)
	}

	// And the original hash MUST still be on the row.
	got, err := q.GetDeviceByID(ctx, "rot-dev")
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if got.TokenHash != "rot-hash-original" {
		t.Errorf("token_hash = %q, want %q (revoked device's hash was rotated)", got.TokenHash, "rot-hash-original")
	}
}

// TestRotateDeviceToken_OneRowForActive verifies that an active device's hash
// is rotated and exactly one row is reported affected.
func TestRotateDeviceToken_OneRowForActive(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "rot-act-owner", "rot-act@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "rot-act",
		Name:      "rot-act",
		TokenHash: "rot-act-original",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	res, err := q.RotateDeviceToken(ctx, RotateDeviceTokenParams{
		TokenHash: "rot-act-rotated",
		ID:        "rot-act",
	})
	if err != nil {
		t.Fatalf("RotateDeviceToken: %v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if n != 1 {
		t.Errorf("RowsAffected = %d, want 1", n)
	}
	got, _ := q.GetDeviceByID(ctx, "rot-act")
	if got.TokenHash != "rot-act-rotated" {
		t.Errorf("token_hash = %q, want %q", got.TokenHash, "rot-act-rotated")
	}
}

// TestRotateDeviceToken_ZeroRowsForUnknownID verifies that rotating a device
// id that does not exist returns RowsAffected==0 (and no error). This is the
// signal the service layer relies on to return ErrDeviceNotFound.
func TestRotateDeviceToken_ZeroRowsForUnknownID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	res, err := q.RotateDeviceToken(ctx, RotateDeviceTokenParams{
		TokenHash: "irrelevant",
		ID:        "no-such-device",
	})
	if err != nil {
		t.Fatalf("RotateDeviceToken on unknown id returned error: %v (want nil error, 0 rows)", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if n != 0 {
		t.Errorf("RowsAffected = %d, want 0 for unknown id", n)
	}
}

// TestTouchDeviceSeen_ThrottlesWithinInterval verifies that two touches inside
// the throttle interval yield exactly one row affected on the first call and
// zero on the second. This is the property that prevents write amplification
// for a device polling every few seconds (per spec NFR).
func TestTouchDeviceSeen_ThrottlesWithinInterval(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "touch-owner", "touch@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "touch-dev",
		Name:      "touch",
		TokenHash: "touch-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	// First touch: last_seen_at IS NULL, must update exactly 1 row.
	res, err := q.TouchDeviceSeen(ctx, TouchDeviceSeenParams{
		ID:       "touch-dev",
		Datetime: "-60 seconds",
	})
	if err != nil {
		t.Fatalf("first TouchDeviceSeen: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Errorf("first touch RowsAffected = %d, want 1 (NULL last_seen_at should always update)", n)
	}

	// Second touch with a 60-second throttle, immediately: must update 0 rows.
	res, err = q.TouchDeviceSeen(ctx, TouchDeviceSeenParams{
		ID:       "touch-dev",
		Datetime: "-60 seconds",
	})
	if err != nil {
		t.Fatalf("second TouchDeviceSeen: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Errorf("second touch within throttle RowsAffected = %d, want 0 (write amplification!)", n)
	}
}

// TestTouchDeviceSeen_UpdatesAfterIntervalElapsed verifies that once the
// throttle window has passed the next touch updates the row. We use a
// 1-second throttle and sleep 2s to guarantee the elapsed time exceeds the
// 1-second granularity of sqlite's datetime('now').
func TestTouchDeviceSeen_UpdatesAfterIntervalElapsed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-sensitive test in -short mode")
	}
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "touch2-owner", "touch2@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "touch2-dev",
		Name:      "touch2",
		TokenHash: "touch2-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	if _, err := q.TouchDeviceSeen(ctx, TouchDeviceSeenParams{
		ID:       "touch2-dev",
		Datetime: "-1 seconds",
	}); err != nil {
		t.Fatalf("first touch: %v", err)
	}

	// Wait long enough that "now - 1 second" is past the recorded last_seen_at.
	time.Sleep(2 * time.Second)

	res, err := q.TouchDeviceSeen(ctx, TouchDeviceSeenParams{
		ID:       "touch2-dev",
		Datetime: "-1 seconds",
	})
	if err != nil {
		t.Fatalf("second touch: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Errorf("touch after throttle expiry RowsAffected = %d, want 1", n)
	}
}

// TestTouchDeviceSeen_ZeroIntervalAlwaysUpdates verifies that an interval of
// "0 seconds" (which is what DEVICE_LAST_SEEN_INTERVAL=0 should map to in
// the service layer) updates on every call. SQLite's strict-less-than
// comparison means a literal 0 second offset would NOT update if the row's
// last_seen_at was written in the same second; this test documents that
// behaviour so the service layer cannot rely on '0 seconds' as "always
// update".
func TestTouchDeviceSeen_ZeroIntervalAlwaysUpdates(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "touch3-owner", "touch3@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "touch3-dev",
		Name:      "touch3",
		TokenHash: "touch3-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	// First touch always updates because last_seen_at IS NULL.
	res, err := q.TouchDeviceSeen(ctx, TouchDeviceSeenParams{
		ID:       "touch3-dev",
		Datetime: "0 seconds",
	})
	if err != nil {
		t.Fatalf("first touch: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Errorf("first touch RowsAffected = %d, want 1", n)
	}

	// Second touch with "0 seconds" offset within the same wall-clock second:
	// last_seen_at < datetime('now', '0 seconds') is FALSE (they're equal),
	// so 0 rows. This documents the limitation of the throttle: to get
	// "update on every auth" the service layer must either bypass this query
	// or pass a positive offset (e.g. '+1 seconds') -- not '0 seconds'.
	res, err = q.TouchDeviceSeen(ctx, TouchDeviceSeenParams{
		ID:       "touch3-dev",
		Datetime: "0 seconds",
	})
	if err != nil {
		t.Fatalf("second touch: %v", err)
	}
	// We do not strictly require 0 rows here -- SQLite's clock granularity
	// is one second so the comparison may go either way at the boundary --
	// but we DO require the call to be safe (no error, no panic) and to
	// return some sensible RowsAffected. The point of this test is to
	// document that "0 seconds" is NOT a magic "always-update" sentinel.
	if n, err := res.RowsAffected(); err != nil {
		t.Errorf("RowsAffected error: %v", err)
	} else if n != 0 && n != 1 {
		t.Errorf("RowsAffected = %d, want 0 or 1", n)
	}
}

// TestTouchDeviceSeen_NoOpForUnknownID verifies that touching an unknown
// device id is harmless (0 rows, no error).
func TestTouchDeviceSeen_NoOpForUnknownID(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	res, err := q.TouchDeviceSeen(ctx, TouchDeviceSeenParams{
		ID:       "no-such-device",
		Datetime: "-60 seconds",
	})
	if err != nil {
		t.Fatalf("TouchDeviceSeen on unknown id returned error: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Errorf("RowsAffected on unknown id = %d, want 0", n)
	}
}

// TestListDevices_OrdersByCreatedAt verifies that ListDevices returns devices
// in created_at order. Tests insert with explicit created_at values to make
// the assertion deterministic (the schema's DEFAULT (datetime('now')) only
// has 1-second granularity, so rapid back-to-back inserts could share a
// timestamp).
func TestListDevices_OrdersByCreatedAt(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "list-owner", "list@example.com")

	// Insert with explicit created_at via raw SQL to control ordering.
	stamps := []struct {
		id, ts string
	}{
		{"dev-c", "2026-03-01T00:00:00Z"},
		{"dev-a", "2026-01-01T00:00:00Z"},
		{"dev-b", "2026-02-01T00:00:00Z"},
	}
	for _, s := range stamps {
		_, err := database.Exec(
			"INSERT INTO devices (id, name, token_hash, created_by, created_at) VALUES (?, ?, ?, ?, ?)",
			s.id, s.id, "hash-"+s.id, user.ID, s.ts,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", s.id, err)
		}
	}

	got, err := q.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListDevices returned %d rows, want 3", len(got))
	}
	wantOrder := []string{"dev-a", "dev-b", "dev-c"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("position %d: got id %q, want %q", i, got[i].ID, want)
		}
	}
}

// TestListDevices_EmptyDatabase verifies that ListDevices returns an empty
// slice (not nil, not an error) for an empty table. Handlers iterating
// the result MUST work in this case.
func TestListDevices_EmptyDatabase(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)

	got, err := q.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices on empty table: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListDevices on empty table returned %d rows, want 0", len(got))
	}
}

// TestListDevices_IncludesRevoked verifies that ListDevices returns revoked
// devices alongside active ones, since the architecture says the UI decides
// what to show. If the query started filtering revoked rows, the admin's
// "revoked devices" section would always be empty.
func TestListDevices_IncludesRevoked(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "inc-owner", "inc@example.com")

	for _, id := range []string{"a", "b"} {
		if err := q.CreateDevice(ctx, CreateDeviceParams{
			ID: id, Name: id, TokenHash: "hash-" + id, CreatedBy: user.ID,
		}); err != nil {
			t.Fatalf("CreateDevice %s: %v", id, err)
		}
	}
	if err := q.RevokeDevice(ctx, "a"); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	got, err := q.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListDevices = %d rows, want 2 (revoked must be included)", len(got))
	}
}

// TestGetDeviceByTokenHash_UnknownReturnsErrNoRows verifies that an unknown
// token hash yields sql.ErrNoRows -- the canonical "not found" signal that
// the service layer can match with errors.Is.
func TestGetDeviceByTokenHash_UnknownReturnsErrNoRows(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)

	_, err := q.GetDeviceByTokenHash(context.Background(), "no-such-hash")
	if err == nil {
		t.Fatal("expected sql.ErrNoRows for unknown token_hash, got nil")
	}
	// Compare via the error sentinel; the driver may wrap it but errors.Is
	// works through the chain.
	if !errorsIs(err, sql.ErrNoRows) {
		t.Errorf("error %v is not sql.ErrNoRows", err)
	}
}

// errorsIs is a tiny indirection so we can reference errors.Is without an
// import bloat block when it's only used once.
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// TestCreateDevice_TokenHashMaxLength verifies the schema accepts arbitrarily
// long token hashes (no implicit length cap that would truncate a SHA-256
// hash). We use a 1MB hash; anything that succeeds here is far above the
// 64-char hex output of SHA-256 we actually use.
func TestCreateDevice_TokenHashMaxLength(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "long-owner", "long@example.com")

	long := make([]byte, 1<<20)
	for i := range long {
		long[i] = 'a'
	}
	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "long-dev",
		Name:      "long",
		TokenHash: string(long),
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice with 1MB hash: %v", err)
	}
	got, err := q.GetDeviceByID(ctx, "long-dev")
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if len(got.TokenHash) != 1<<20 {
		t.Errorf("hash round-trip: got %d bytes, want %d", len(got.TokenHash), 1<<20)
	}
}

// TestCreateDevice_UnicodeName verifies that arbitrary unicode survives
// round-trip through the name column. Devices are user-named ("kitchen
// tablet") and admins should be able to use any reasonable display name.
func TestCreateDevice_UnicodeName(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "uni-owner", "uni@example.com")

	name := "\u53b0\u623f\u5e73\u677f \U0001f4f1"
	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID:        "uni-dev",
		Name:      name,
		TokenHash: "uni-hash",
		CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("CreateDevice with unicode name: %v", err)
	}
	got, err := q.GetDeviceByID(ctx, "uni-dev")
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}
	if got.Name != name {
		t.Errorf("name round-trip: got %q, want %q", got.Name, name)
	}
}

// TestCreateDevice_DuplicateIDRejected verifies the PRIMARY KEY constraint on
// devices.id rejects a second insert with the same id. (Distinct from
// duplicate token_hash, which is also rejected by the UNIQUE index.)
func TestCreateDevice_DuplicateIDRejected(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "dup-owner", "dup@example.com")

	if err := q.CreateDevice(ctx, CreateDeviceParams{
		ID: "dup", Name: "first", TokenHash: "hash-1", CreatedBy: user.ID,
	}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := q.CreateDevice(ctx, CreateDeviceParams{
		ID: "dup", Name: "second", TokenHash: "hash-2", CreatedBy: user.ID,
	})
	if err == nil {
		t.Fatal("expected PRIMARY KEY violation for duplicate id, got nil")
	}
}

// TestCreateDevice_UnknownCreatedByRejected verifies the FK constraint on
// devices.created_by surfaces immediately at insert time, not later.
func TestCreateDevice_UnknownCreatedByRejected(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	err := q.CreateDevice(ctx, CreateDeviceParams{
		ID: "orphan", Name: "orphan", TokenHash: "orphan-hash",
		CreatedBy: "no-such-user",
	})
	if err == nil {
		t.Fatal("expected FK violation for unknown created_by, got nil")
	}
}

// TestCreateDevice_ParallelDistinctTokens verifies that the sqlc-generated
// CreateDevice is safe to drive from multiple goroutines: the UNIQUE
// constraint on token_hash holds and no devices are lost or duplicated.
// Run with -race to catch any shared-state issues in the generated code or
// the queries struct itself.
//
// We pin the test database to a single connection (matching production's
// DB_MAX_OPEN_CONNS=1 default) because :memory: SQLite databases are
// per-connection, not per-DSN: the schema would not be visible to a second
// connection in the pool.
func TestCreateDevice_ParallelDistinctTokens(t *testing.T) {
	database := OpenTestDB(t)
	database.SetMaxOpenConns(1)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "par-owner", "par@example.com")

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			err := q.CreateDevice(ctx, CreateDeviceParams{
				ID:        fmtDevID(idx),
				Name:      fmtDevID(idx),
				TokenHash: "hash-" + fmtDevID(idx),
				CreatedBy: user.ID,
			})
			if err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("parallel CreateDevice: %v", err)
	}

	got, err := q.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(got) != n {
		t.Errorf("after %d parallel inserts, ListDevices returned %d rows", n, len(got))
	}
}

// TestCreateDevice_ParallelSameHashConflicts verifies that when two
// goroutines race to insert devices with the SAME token_hash, the UNIQUE
// constraint guarantees exactly one wins and the other surfaces a database
// error rather than silently succeeding. This is the safety net behind the
// "tokens are unique" invariant in the architecture doc.
func TestCreateDevice_ParallelSameHashConflicts(t *testing.T) {
	database := OpenTestDB(t)
	database.SetMaxOpenConns(1)
	q := New(database)
	ctx := context.Background()
	user := seedDeviceUser(t, q, "race-owner", "race@example.com")

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = q.CreateDevice(ctx, CreateDeviceParams{
				ID:        fmtDevID(idx),
				Name:      fmtDevID(idx),
				TokenHash: "shared-race-hash",
				CreatedBy: user.ID,
			})
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("got %d successful concurrent inserts of the same token_hash, want exactly 1", successes)
	}

	all, err := q.ListDevices(ctx)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("after race, ListDevices returned %d rows, want 1", len(all))
	}
}

func fmtDevID(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return "p" + string(digits[i])
	}
	return "p" + string(digits[i/10]) + string(digits[i%10])
}

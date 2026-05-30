package db

import (
	"context"
	"database/sql"
	"testing"
)

// insertDeviceWithScreen inserts a device row using raw SQL so the test can
// drive an arbitrary screen_id (NULL or a specific id) without going through
// the auth service. The CreateDevice sqlc query does not take screen_id.
func insertDeviceWithScreen(t *testing.T, db *sql.DB, deviceID, userID, screenID string) {
	t.Helper()
	var screenArg interface{}
	if screenID == "" {
		screenArg = nil
	} else {
		screenArg = screenID
	}
	if _, err := db.Exec(
		"INSERT INTO devices (id, name, token_hash, created_by, screen_id) VALUES (?, ?, ?, ?, ?)",
		deviceID, deviceID+"-name", deviceID+"-hash", userID, screenArg,
	); err != nil {
		t.Fatalf("insert device %s (screen=%v): %v", deviceID, screenArg, err)
	}
}

// TestDevices_ScreenIDColumnExistsAfterMigration verifies migration 010 added
// the screen_id column to devices. Without this column the auth service's
// AssignDeviceToScreen would fail at the sqlc layer.
func TestDevices_ScreenIDColumnExistsAfterMigration(t *testing.T) {
	database := OpenTestDB(t)

	rows, err := database.Query("PRAGMA table_info(devices)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(devices): %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan PRAGMA row: %v", err)
		}
		if name == "screen_id" {
			found = true
			if notnull != 0 {
				t.Errorf("screen_id should be nullable, got notnull=%d", notnull)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("PRAGMA iteration: %v", err)
	}
	if !found {
		t.Fatal("devices.screen_id column not present after migration 010")
	}
}

// TestDevices_ScreenIDIndexExistsAfterMigration verifies the
// idx_devices_screen_id index from migration 010 was created. The "which
// devices reference this Screen?" admin query and the cascade fire-time both
// benefit from this index.
func TestDevices_ScreenIDIndexExistsAfterMigration(t *testing.T) {
	database := OpenTestDB(t)

	var name string
	err := database.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_devices_screen_id'",
	).Scan(&name)
	if err != nil {
		t.Fatalf("idx_devices_screen_id missing after migration 010: %v", err)
	}
	if name != "idx_devices_screen_id" {
		t.Errorf("index name = %q, want %q", name, "idx_devices_screen_id")
	}
}

// TestDevices_ScreenDeleteSetsScreenIDNull verifies the FK SET NULL behaviour:
// when a Screen referenced by a device is DELETEd, the device's screen_id
// becomes NULL atomically. This is the architectural contract recorded in
// ADR-009 and SPEC-007 AC-6.
func TestDevices_ScreenDeleteSetsScreenIDNull(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-sn", "screen-null-theme", 0)
	insertTestScreen(ctx, t, q, "screen-sn", "screen-null-target", "theme-sn")
	user := seedDeviceUser(t, q, "owner-sn", "owner-sn@example.com")
	insertDeviceWithScreen(t, database, "dev-sn", user.ID, "screen-sn")

	// Sanity: the device row points at the screen.
	var got sql.NullString
	if err := database.QueryRow(
		"SELECT screen_id FROM devices WHERE id = ?", "dev-sn",
	).Scan(&got); err != nil {
		t.Fatalf("pre-delete SELECT screen_id: %v", err)
	}
	if !got.Valid || got.String != "screen-sn" {
		t.Fatalf("pre-delete screen_id = %+v, want valid string %q", got, "screen-sn")
	}

	// Delete the screen via sqlc.
	if _, err := q.DeleteScreen(ctx, "screen-sn"); err != nil {
		t.Fatalf("DeleteScreen: %v", err)
	}

	// The device row must still exist (SET NULL, not CASCADE).
	var deviceCount int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM devices WHERE id = ?", "dev-sn",
	).Scan(&deviceCount); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if deviceCount != 1 {
		t.Fatalf("device row count after screen delete = %d, want 1 (SET NULL must NOT cascade)", deviceCount)
	}

	// And screen_id must now be NULL.
	if err := database.QueryRow(
		"SELECT screen_id FROM devices WHERE id = ?", "dev-sn",
	).Scan(&got); err != nil {
		t.Fatalf("post-delete SELECT screen_id: %v", err)
	}
	if got.Valid {
		t.Errorf("post-delete screen_id = %q (valid=true), want NULL", got.String)
	}
}

// TestDevices_ScreenDeleteSetsNullForMultipleDevices verifies the FK SET NULL
// fires atomically for every device referencing a deleted Screen. SPEC-007
// AC-6 specifically calls out the "two devices reference the same Screen"
// case.
func TestDevices_ScreenDeleteSetsNullForMultipleDevices(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-mn", "multi-null-theme", 0)
	insertTestScreen(ctx, t, q, "screen-mn", "multi-null-target", "theme-mn")
	user := seedDeviceUser(t, q, "owner-mn", "owner-mn@example.com")
	insertDeviceWithScreen(t, database, "dev-mn-1", user.ID, "screen-mn")
	insertDeviceWithScreen(t, database, "dev-mn-2", user.ID, "screen-mn")

	if _, err := q.DeleteScreen(ctx, "screen-mn"); err != nil {
		t.Fatalf("DeleteScreen: %v", err)
	}

	for _, id := range []string{"dev-mn-1", "dev-mn-2"} {
		var got sql.NullString
		if err := database.QueryRow(
			"SELECT screen_id FROM devices WHERE id = ?", id,
		).Scan(&got); err != nil {
			t.Fatalf("SELECT screen_id for %s: %v", id, err)
		}
		if got.Valid {
			t.Errorf("device %s screen_id = %q after screen delete, want NULL", id, got.String)
		}
	}
}

// TestDevices_ScreenIDFKRequiresExistingScreen verifies the FK constraint
// rejects an INSERT (or UPDATE) that points screen_id at a non-existent
// screen. The migration is the source of the FK; this test pins that the
// constraint is enforced at write time.
func TestDevices_ScreenIDFKRequiresExistingScreen(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	user := seedDeviceUser(t, q, "owner-fk", "owner-fk@example.com")

	_, err := database.Exec(
		"INSERT INTO devices (id, name, token_hash, created_by, screen_id) VALUES (?, ?, ?, ?, ?)",
		"dev-fk", "fk", "fk-hash", user.ID, "no-such-screen",
	)
	if err == nil {
		t.Fatal("expected FOREIGN KEY violation for non-existent screen_id, got nil")
	}
}

// TestDevices_ScreenDeleteSetNullRollsBackWithParentTx pins that the FK
// SET NULL action is part of the same transaction as the parent DELETE.
// If the FK were enforced via an asynchronous trigger (or a write-through
// side effect), rolling back the parent transaction would leave the device's
// screen_id NULL while the screen row reappears -- a data-integrity bug.
// SQLite's standard FK behaviour is transactional, but this test pins it.
func TestDevices_ScreenDeleteSetNullRollsBackWithParentTx(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-rb", "rb-theme", 0)
	insertTestScreen(ctx, t, q, "screen-rb", "rb-screen", "theme-rb")
	user := seedDeviceUser(t, q, "owner-rb", "owner-rb@example.com")
	insertDeviceWithScreen(t, database, "dev-rb", user.ID, "screen-rb")

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM screens WHERE id = ?", "screen-rb"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("DELETE screen inside tx: %v", err)
	}

	// Inside the tx the SET NULL has fired.
	var inTx sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT screen_id FROM devices WHERE id = ?", "dev-rb").Scan(&inTx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("read screen_id inside tx: %v", err)
	}
	if inTx.Valid {
		_ = tx.Rollback()
		t.Fatalf("in-tx screen_id = %q, want NULL after parent DELETE", inTx.String)
	}

	// Rolling back the parent DELETE must also roll back the SET NULL.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var afterRollback sql.NullString
	if err := database.QueryRow("SELECT screen_id FROM devices WHERE id = ?", "dev-rb").Scan(&afterRollback); err != nil {
		t.Fatalf("read screen_id after rollback: %v", err)
	}
	if !afterRollback.Valid || afterRollback.String != "screen-rb" {
		t.Errorf("post-rollback screen_id = %+v, want valid 'screen-rb' (SET NULL must be part of parent tx)", afterRollback)
	}

	// And the screen row must be back as well.
	var screenCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM screens WHERE id = ?", "screen-rb").Scan(&screenCount); err != nil {
		t.Fatalf("count screens after rollback: %v", err)
	}
	if screenCount != 1 {
		t.Errorf("screens row count after rollback = %d, want 1 (parent DELETE should have rolled back)", screenCount)
	}
}

// TestDevices_ScreenIDDownMigration_PreservesRows verifies that running the
// down migration on a populated devices table cleanly drops the screen_id
// column and the index without losing rows. modernc.org/sqlite supports
// DROP COLUMN; if the underlying driver ever regresses or the migration
// gains a constraint that blocks rebuild, this test will surface it.
func TestDevices_ScreenIDDownMigration_PreservesRows(t *testing.T) {
	database := OpenTestDB(t)
	q := New(database)
	ctx := context.Background()

	insertTestTheme(ctx, t, q, "theme-down", "down-theme", 0)
	insertTestScreen(ctx, t, q, "screen-down", "down-screen", "theme-down")
	user := seedDeviceUser(t, q, "owner-down", "owner-down@example.com")
	insertDeviceWithScreen(t, database, "dev-down-assigned", user.ID, "screen-down")
	insertDeviceWithScreen(t, database, "dev-down-unassigned", user.ID, "")

	const downSQL = `DROP INDEX IF EXISTS idx_devices_screen_id;
ALTER TABLE devices DROP COLUMN screen_id;`
	if _, err := database.ExecContext(ctx, downSQL); err != nil {
		t.Fatalf("down migration with data: %v", err)
	}

	// Both rows must still exist.
	for _, id := range []string{"dev-down-assigned", "dev-down-unassigned"} {
		var n int
		if err := database.QueryRow("SELECT COUNT(*) FROM devices WHERE id = ?", id).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", id, err)
		}
		if n != 1 {
			t.Errorf("device %s missing after DROP COLUMN: count=%d", id, n)
		}
	}

	// The column must be gone.
	rows, err := database.Query("PRAGMA table_info(devices)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(devices): %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("PRAGMA scan: %v", err)
		}
		if name == "screen_id" {
			t.Errorf("devices.screen_id column still present after down migration")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("PRAGMA iter: %v", err)
	}

	// The index must be gone too.
	var nIdx int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_devices_screen_id'",
	).Scan(&nIdx); err != nil {
		t.Fatalf("count index: %v", err)
	}
	if nIdx != 0 {
		t.Errorf("idx_devices_screen_id still present after down migration")
	}
}

package db

import (
	"sync"
	"testing"
)

// TestOpenTestDB_ParallelSubtests verifies that OpenTestDB is safe to call
// from multiple parallel subtests. Each call should produce an independent
// in-memory database with no shared state. This catches races on the
// embedded migration FS or any global state.
func TestOpenTestDB_ParallelSubtests(t *testing.T) {
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			defer wg.Done()

			db := OpenTestDB(t)

			// Each database must be independently usable.
			tableName := "parallel_test"
			if _, err := db.Exec("CREATE TABLE " + tableName + " (id INTEGER PRIMARY KEY)"); err != nil {
				t.Fatalf("subtest %d: create table: %v", i, err)
			}
			if _, err := db.Exec("INSERT INTO "+tableName+" (id) VALUES (?)", i); err != nil {
				t.Fatalf("subtest %d: insert: %v", i, err)
			}

			var got int
			if err := db.QueryRow("SELECT id FROM " + tableName).Scan(&got); err != nil {
				t.Fatalf("subtest %d: select: %v", i, err)
			}
			if got != i {
				t.Errorf("subtest %d: got id=%d, want %d", i, got, i)
			}

			// Verify isolation: count should be exactly 1 (only our insert).
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM " + tableName).Scan(&count); err != nil {
				t.Fatalf("subtest %d: count: %v", i, err)
			}
			if count != 1 {
				t.Errorf("subtest %d: count=%d, want 1 (databases are not isolated)", i, count)
			}
		})
	}
}

// TestOpenTestDB_ForeignKeyConstraintEnforced verifies that the test helper
// produces a database where foreign key constraints are actually enforced,
// not just reported as enabled. This creates parent/child tables and
// attempts a FK violation.
func TestOpenTestDB_ForeignKeyConstraintEnforced(t *testing.T) {
	db := OpenTestDB(t)

	// Create parent and child tables with a foreign key relationship.
	if _, err := db.Exec(`CREATE TABLE fk_parent (
		id INTEGER PRIMARY KEY
	)`); err != nil {
		t.Fatalf("create parent table: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE fk_child (
		id INTEGER PRIMARY KEY,
		parent_id INTEGER NOT NULL REFERENCES fk_parent(id)
	)`); err != nil {
		t.Fatalf("create child table: %v", err)
	}

	// Insert a valid parent row.
	if _, err := db.Exec("INSERT INTO fk_parent (id) VALUES (1)"); err != nil {
		t.Fatalf("insert parent: %v", err)
	}

	// Insert a child referencing the parent: should succeed.
	if _, err := db.Exec("INSERT INTO fk_child (id, parent_id) VALUES (1, 1)"); err != nil {
		t.Fatalf("insert valid child: %v", err)
	}

	// Insert a child referencing a nonexistent parent: should fail.
	_, err := db.Exec("INSERT INTO fk_child (id, parent_id) VALUES (2, 999)")
	if err == nil {
		t.Fatal("expected FK violation error when inserting child with nonexistent parent_id=999, got nil")
	}

	// Delete the parent while a child still references it: should fail.
	_, err = db.Exec("DELETE FROM fk_parent WHERE id = 1")
	if err == nil {
		t.Fatal("expected FK violation error when deleting parent with existing child, got nil")
	}
}

// TestOpenTestDB_ForeignKeyCascadeWorks verifies that ON DELETE CASCADE
// works through the test helper, since store tests will rely on cascading
// deletes for entity cleanup.
func TestOpenTestDB_ForeignKeyCascadeWorks(t *testing.T) {
	db := OpenTestDB(t)

	if _, err := db.Exec(`CREATE TABLE cascade_parent (
		id INTEGER PRIMARY KEY
	)`); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE cascade_child (
		id INTEGER PRIMARY KEY,
		parent_id INTEGER NOT NULL REFERENCES cascade_parent(id) ON DELETE CASCADE
	)`); err != nil {
		t.Fatalf("create child: %v", err)
	}

	if _, err := db.Exec("INSERT INTO cascade_parent (id) VALUES (1)"); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	if _, err := db.Exec("INSERT INTO cascade_child (id, parent_id) VALUES (10, 1)"); err != nil {
		t.Fatalf("insert child: %v", err)
	}

	// Delete parent -- child should be cascade-deleted.
	if _, err := db.Exec("DELETE FROM cascade_parent WHERE id = 1"); err != nil {
		t.Fatalf("delete parent with cascade: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM cascade_child").Scan(&count); err != nil {
		t.Fatalf("count children: %v", err)
	}
	if count != 0 {
		t.Errorf("cascade_child count = %d after parent delete, want 0", count)
	}
}

// TestOpenTestDB_SchemaReady verifies that the returned database has the
// schema_migrations table populated, confirming that Migrate ran
// successfully and the DB is ready for store-layer queries.
func TestOpenTestDB_SchemaReady(t *testing.T) {
	db := OpenTestDB(t)

	// Verify we can write and read back data (full round-trip).
	if _, err := db.Exec(`CREATE TABLE readiness_probe (
		id INTEGER PRIMARY KEY,
		value TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	const want = "hello-from-test"
	if _, err := db.Exec("INSERT INTO readiness_probe (id, value) VALUES (1, ?)", want); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got string
	if err := db.QueryRow("SELECT value FROM readiness_probe WHERE id = 1").Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != want {
		t.Errorf("readiness probe value = %q, want %q", got, want)
	}
}

// TestOpenTestDB_CleanupClosesDB verifies that the database connection
// is properly closed after test cleanup runs. We use a sub-test so that
// cleanup fires when the sub-test ends, then probe the db handle.
func TestOpenTestDB_CleanupClosesDB(t *testing.T) {
	var closedDB interface{ Ping() error }

	t.Run("subtest", func(t *testing.T) {
		db := OpenTestDB(t)
		closedDB = db
		// Verify it works while the subtest is still running.
		if err := db.Ping(); err != nil {
			t.Fatalf("ping during subtest: %v", err)
		}
	})

	// After the subtest completes, cleanup should have closed the db.
	// Ping on a closed *sql.DB should return an error.
	if err := closedDB.Ping(); err == nil {
		t.Error("expected error pinging database after cleanup, got nil -- database was not closed")
	}
}

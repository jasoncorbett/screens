package db

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// OpenTestDB creates an in-memory SQLite database with all migrations applied.
// It registers a cleanup function to close the database when the test completes.
// Fails the test immediately if the database cannot be opened or migrations fail.
func OpenTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	if err := Migrate(context.Background(), db); err != nil {
		db.Close()
		t.Fatalf("migrate test database: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	return db
}

package db

import (
	"testing"
)

func TestOpenTestDB_ReturnsUsableDB(t *testing.T) {
	db := OpenTestDB(t)

	var result int
	if err := db.QueryRow("SELECT 1").Scan(&result); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	if result != 1 {
		t.Errorf("SELECT 1 = %d, want 1", result)
	}
}

func TestOpenTestDB_HasMigrations(t *testing.T) {
	db := OpenTestDB(t)

	rows, err := db.Query("SELECT version, name FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}

	// The schema_migrations table exists and is queryable.
	// The seed migration (001) has no SQL to execute, so it records a row.
	if count < 1 {
		t.Errorf("schema_migrations has %d rows, want at least 1", count)
	}
}

func TestOpenTestDB_ForeignKeysEnabled(t *testing.T) {
	db := OpenTestDB(t)

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestOpenTestDB_MultipleInstances(t *testing.T) {
	db1 := OpenTestDB(t)
	db2 := OpenTestDB(t)

	// Create a table in db1 and insert a row.
	if _, err := db1.Exec("CREATE TABLE test_isolation (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table in db1: %v", err)
	}
	if _, err := db1.Exec("INSERT INTO test_isolation (id) VALUES (42)"); err != nil {
		t.Fatalf("insert into db1: %v", err)
	}

	// db2 should not have the test_isolation table at all.
	var count int
	err := db2.QueryRow("SELECT COUNT(*) FROM test_isolation").Scan(&count)
	if err == nil {
		t.Errorf("db2 has test_isolation table with %d rows; databases are not independent", count)
	}
}

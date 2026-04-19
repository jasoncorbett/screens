package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestOpen_InMemory(t *testing.T) {
	t.Parallel()

	cfg := DBConfig{
		Path:         ":memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}

	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open(:memory:) error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Verify foreign_keys pragma is enabled.
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	// WAL mode on :memory: reports "memory", so test with a temp file.
	dir := t.TempDir()
	fileCfg := DBConfig{
		Path:         filepath.Join(dir, "test.db"),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}
	fileDB, err := Open(fileCfg)
	if err != nil {
		t.Fatalf("Open(temp file) error: %v", err)
	}
	t.Cleanup(func() { fileDB.Close() })

	var mode string
	if err := fileDB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode pragma: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestOpen_TempFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "screens.db")

	cfg := DBConfig{
		Path:         dbPath,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}

	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("database file not created: %v", err)
	}
}

func TestMigrateFS_CreatesSchemaTable(t *testing.T) {
	t.Parallel()

	cfg := DBConfig{
		Path:         ":memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Use an empty FS (no .sql files).
	emptyFS := fstest.MapFS{}

	if err := MigrateFS(context.Background(), db, emptyFS); err != nil {
		t.Fatalf("MigrateFS error: %v", err)
	}

	// Verify schema_migrations table exists.
	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&name)
	if err != nil {
		t.Fatalf("schema_migrations table not created: %v", err)
	}
	if name != "schema_migrations" {
		t.Errorf("table name = %q, want %q", name, "schema_migrations")
	}
}

func TestMigrateFS_AppliesMigrations(t *testing.T) {
	t.Parallel()

	cfg := DBConfig{
		Path:         ":memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	testFS := fstest.MapFS{
		"001_create_users.sql": &fstest.MapFile{
			Data: []byte("-- +up\nCREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\n\n-- +down\nDROP TABLE IF EXISTS users;\n"),
		},
		"002_create_posts.sql": &fstest.MapFile{
			Data: []byte("-- +up\nCREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, title TEXT NOT NULL);\n\n-- +down\nDROP TABLE IF EXISTS posts;\n"),
		},
	}

	if err := MigrateFS(context.Background(), db, testFS); err != nil {
		t.Fatalf("MigrateFS error: %v", err)
	}

	// Verify both tables exist.
	for _, table := range []string{"users", "posts"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not created: %v", table, err)
		}
	}

	// Verify both migrations recorded.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 2 {
		t.Errorf("schema_migrations count = %d, want 2", count)
	}
}

func TestMigrateFS_SkipsApplied(t *testing.T) {
	t.Parallel()

	cfg := DBConfig{
		Path:         ":memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	testFS := fstest.MapFS{
		"001_create_users.sql": &fstest.MapFile{
			Data: []byte("-- +up\nCREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL);\n\n-- +down\nDROP TABLE IF EXISTS users;\n"),
		},
	}

	// First run.
	if err := MigrateFS(context.Background(), db, testFS); err != nil {
		t.Fatalf("first MigrateFS error: %v", err)
	}

	// Second run should not error (idempotent).
	if err := MigrateFS(context.Background(), db, testFS); err != nil {
		t.Fatalf("second MigrateFS error: %v", err)
	}

	// Verify only one record.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_migrations count = %d, want 1", count)
	}
}

func TestMigrateFS_InvalidSQL(t *testing.T) {
	t.Parallel()

	cfg := DBConfig{
		Path:         ":memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	testFS := fstest.MapFS{
		"001_bad.sql": &fstest.MapFile{
			Data: []byte("-- +up\nCREATE TABL invalid_syntax;\n\n-- +down\n"),
		},
	}

	err = MigrateFS(context.Background(), db, testFS)
	if err == nil {
		t.Fatal("expected error for invalid SQL, got nil")
	}

	// Verify migration was NOT recorded.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 0 {
		t.Errorf("schema_migrations count = %d, want 0 (failed migration should not be recorded)", count)
	}
}

func TestMigrateFS_OrderByVersion(t *testing.T) {
	t.Parallel()

	cfg := DBConfig{
		Path:         ":memory:",
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}
	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Versions out of filename sort order: 002 before 010.
	testFS := fstest.MapFS{
		"010_tenth.sql": &fstest.MapFile{
			Data: []byte("-- +up\nCREATE TABLE tenth (id INTEGER PRIMARY KEY);\n\n-- +down\nDROP TABLE IF EXISTS tenth;\n"),
		},
		"002_second.sql": &fstest.MapFile{
			Data: []byte("-- +up\nCREATE TABLE second (id INTEGER PRIMARY KEY);\n\n-- +down\nDROP TABLE IF EXISTS second;\n"),
		},
	}

	if err := MigrateFS(context.Background(), db, testFS); err != nil {
		t.Fatalf("MigrateFS error: %v", err)
	}

	// Verify versions were applied in numeric order by checking
	// the schema_migrations table order.
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY rowid")
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()

	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}
	if versions[0] != 2 || versions[1] != 10 {
		t.Errorf("versions = %v, want [2 10]", versions)
	}
}

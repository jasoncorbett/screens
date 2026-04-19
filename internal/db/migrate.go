package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies all pending migrations from the embedded filesystem.
func Migrate(ctx context.Context, db *sql.DB) error {
	return MigrateFS(ctx, db, migrationFS)
}

// MigrateFS applies all pending migrations from the given filesystem.
// It self-bootstraps the schema_migrations table, then applies pending
// migrations in version order within individual transactions.
func MigrateFS(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	migrations, err := readMigrations(fsys)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	applied := 0
	for _, m := range migrations {
		ok, err := isApplied(ctx, db, m.version)
		if err != nil {
			return fmt.Errorf("check migration %d (%s): %w", m.version, m.name, err)
		}
		if ok {
			continue
		}

		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		slog.Info("migration applied", "version", m.version, "name", m.name)
		applied++
	}

	if applied == 0 {
		slog.Info("migrations up to date")
	}
	return nil
}

type migration struct {
	version int
	name    string
	upSQL   string
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	return err
}

func readMigrations(fsys fs.FS) ([]migration, error) {
	var migrations []migration

	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".sql" {
			return nil
		}

		name := filepath.Base(path)
		version, err := parseVersion(name)
		if err != nil {
			return fmt.Errorf("parse version from %s: %w", name, err)
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		upSQL := parseUpSection(string(data))

		migrations = append(migrations, migration{
			version: version,
			name:    name,
			upSQL:   upSQL,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

func parseVersion(filename string) (int, error) {
	parts := strings.SplitN(filename, "_", 2)
	if len(parts) < 2 {
		return 0, fmt.Errorf("invalid migration filename: %s", filename)
	}
	return strconv.Atoi(parts[0])
}

func parseUpSection(content string) string {
	upIdx := strings.Index(content, "-- +up")
	if upIdx == -1 {
		return content
	}

	after := content[upIdx+len("-- +up"):]

	downIdx := strings.Index(after, "-- +down")
	if downIdx == -1 {
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(after[:downIdx])
}

func isApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.upSQL); err != nil {
		return fmt.Errorf("execute SQL: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name) VALUES (?, ?)", m.version, m.name); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	return tx.Commit()
}

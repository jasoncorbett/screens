package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

// DBConfig holds database connection settings.
// Passed from config.Config.DB — do not read env vars here.
type DBConfig struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Open creates a new database connection pool with SQLite pragmas applied.
// The caller is responsible for calling Close on the returned *sql.DB.
func Open(cfg DBConfig) (*sql.DB, error) {
	dsn := cfg.Path + "?_pragma=journal_mode(wal)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	slog.Info("database opened", "path", cfg.Path)
	return db, nil
}

// Close closes the database connection pool.
func Close(db *sql.DB) error {
	slog.Info("database closed")
	return db.Close()
}

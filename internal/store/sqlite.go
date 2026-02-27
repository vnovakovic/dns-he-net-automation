// Package store manages the SQLite database connection and schema migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/pressly/goose/v3"

	// Register the modernc.org/sqlite driver as "sqlite" (NOT "sqlite3" -- that is mattn/go-sqlite3).
	// IMPORTANT: driver name is "sqlite", goose dialect is still DialectSQLite3 (that is the SQL dialect name).
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

// Open initializes a SQLite database at dbPath with WAL mode, busy_timeout, and foreign_keys
// pragmas (REL-01), then runs all pending goose migrations (OPS-06).
//
// For in-memory databases, pass ":memory:". File permissions are set to 0600 (SEC-03).
// Returns a ready-to-use *sql.DB or an error if initialization or migrations fail.
func Open(dbPath string) (*sql.DB, error) {
	// Ensure the database file exists with restricted permissions (SEC-03).
	// This is skipped for in-memory databases.
	if dbPath != ":memory:" {
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				return nil, fmt.Errorf("create db file: %w", err)
			}
			if err := f.Close(); err != nil {
				return nil, fmt.Errorf("close db file after create: %w", err)
			}
		}
	}

	// Build DSN with required pragmas (REL-01).
	// modernc.org/sqlite uses _pragma=name(value) syntax -- NOT _journal_mode= (that is mattn/go-sqlite3).
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
		dbPath,
	)

	// Open with driver "sqlite" (modernc.org/sqlite), NOT "sqlite3" (mattn/go-sqlite3).
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Verify the connection is alive.
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Create a sub-filesystem rooted at the migrations directory for goose.
	migrationFS, err := fs.Sub(embedMigrations, "migrations")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create migration sub-fs: %w", err)
	}

	// Create goose provider using the embedded migrations (OPS-06).
	// goose.DialectSQLite3 is the SQL dialect name -- not the Go driver name.
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationFS)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create goose provider: %w", err)
	}

	// Run all pending migrations.
	results, err := provider.Up(context.Background())
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	for _, r := range results {
		slog.Info("migration applied",
			"path", r.Source.Path,
			"version", r.Source.Version,
			"duration", r.Duration,
		)
	}

	return db, nil
}

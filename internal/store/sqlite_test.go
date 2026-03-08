package store_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vnovakovic/dns-he-net-automation/internal/store"
)

// TestOpen_InMemory verifies that an in-memory database opens successfully,
// runs migrations, and creates the expected tables.
func TestOpen_InMemory(t *testing.T) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()

	// accounts table must exist after migrations.
	var tableName string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='accounts'",
	).Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "accounts", tableName)

	// schema_info table must exist after migrations.
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='schema_info'",
	).Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "schema_info", tableName)

	// schema_info must have version = 1.
	var version string
	err = db.QueryRow("SELECT value FROM schema_info WHERE key='version'").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, "1", version)
}

// TestOpen_WALMode verifies that the database is opened in WAL journal mode.
// NOTE: WAL mode is not supported for :memory: databases (SQLite returns "memory" for those).
// A temporary file database must be used to verify WAL mode.
func TestOpen_WALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal_test.db")

	db, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	var mode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	require.NoError(t, err)
	assert.Equal(t, "wal", mode, "database must be in WAL mode")
}

// TestOpen_ForeignKeys verifies that foreign keys are enabled.
func TestOpen_ForeignKeys(t *testing.T) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()

	var fkEnabled int
	err = db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	require.NoError(t, err)
	assert.Equal(t, 1, fkEnabled, "foreign keys must be enabled")
}

// TestOpen_FilePermissions verifies that the database file is created with 0600 permissions.
// This test is skipped on Windows as file permission bits behave differently.
func TestOpen_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	info, err := os.Stat(dbPath)
	require.NoError(t, err)

	// Permissions should be 0600 (owner read/write only).
	perm := info.Mode().Perm()
	assert.Equal(t, os.FileMode(0600), perm, "database file must have 0600 permissions")
}

// TestOpen_TempFile verifies that a file-based database opens and runs migrations successfully.
func TestOpen_TempFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := store.Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// File must exist.
	_, err = os.Stat(dbPath)
	require.NoError(t, err, "database file must exist after Open()")

	// Migrations must have run.
	var tableName string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='accounts'",
	).Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "accounts", tableName)
}

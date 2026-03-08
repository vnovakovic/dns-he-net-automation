package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

const (
	// keyAdminPasswordHash is the server_config key for the bcrypt-hashed admin password.
	keyAdminPasswordHash = "admin_password_hash"

	// defaultAdminPassword is the password seeded into the DB on first start when
	// ADMIN_PASSWORD env var is not set. Operators should change this after the first login.
	//
	// WHY "admin123" (not a random value):
	//   A random default would require the operator to discover it before logging in.
	//   A well-known default is acceptable because the admin UI is only accessible from
	//   trusted networks; the operator is expected to change it immediately after setup.
	//   The installer/docker-compose documentation should highlight this requirement.
	defaultAdminPassword = "admin123"
)

// GetAdminPasswordHash returns the bcrypt hash stored in server_config.
// Returns sql.ErrNoRows if no admin password has been set yet.
func GetAdminPasswordHash(ctx context.Context, db *sql.DB) (string, error) {
	var hash string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM server_config WHERE key = ?`,
		keyAdminPasswordHash,
	).Scan(&hash)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// SetAdminPasswordHash upserts the bcrypt hash for the admin password.
func SetAdminPasswordHash(ctx context.Context, db *sql.DB, hash string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO server_config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		keyAdminPasswordHash, hash,
	)
	return err
}

// EnsureAdminPassword resolves the effective admin password hash using the following priority:
//
//  1. If plaintext is non-empty (ADMIN_PASSWORD env var is set):
//     bcrypt-hash it and upsert to the DB. This makes the env var the authoritative source
//     and persists the change so the env var can be cleared after the override is committed.
//
//  2. If plaintext is empty:
//     Read the existing hash from the DB. If not present (first startup on a fresh DB),
//     seed the default password "admin123" as a bcrypt hash and return it.
//
// Returns the bcrypt hash to use for all admin password comparisons.
//
// WHY hash + upsert on every startup when env var is set (not just on change):
//   The server cannot know whether the env value changed between restarts without
//   comparing it against the stored hash — which requires a bcrypt comparison (slow).
//   Unconditionally re-hashing and upserting is O(1ms) vs O(100ms) for bcrypt, and
//   ensures the DB always reflects the current env value without special-casing.
//
// WHY this function lives in the store package (not main or admin):
//   The logic touches the DB and belongs near other DB helpers. main.go calls it
//   once at startup and passes the resulting hash into the router — the auth layer
//   never needs to know how the hash was obtained.
func EnsureAdminPassword(ctx context.Context, db *sql.DB, plaintext string) (string, error) {
	if plaintext != "" {
		// Env var is set — bcrypt hash it and force-update the DB.
		// Cost 12 is the current recommended bcrypt work factor (~250ms on modern hardware).
		hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), 12)
		if err != nil {
			return "", fmt.Errorf("bcrypt admin password: %w", err)
		}
		if err := SetAdminPasswordHash(ctx, db, string(hash)); err != nil {
			return "", fmt.Errorf("store admin password hash: %w", err)
		}
		return string(hash), nil
	}

	// Env var is empty — try the DB.
	hash, err := GetAdminPasswordHash(ctx, db)
	if err == nil {
		return hash, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read admin password hash: %w", err)
	}

	// First startup: no hash in DB → seed the default password.
	defaultHash, err := bcrypt.GenerateFromPassword([]byte(defaultAdminPassword), 12)
	if err != nil {
		return "", fmt.Errorf("bcrypt default admin password: %w", err)
	}
	if err := SetAdminPasswordHash(ctx, db, string(defaultHash)); err != nil {
		return "", fmt.Errorf("store default admin password hash: %w", err)
	}
	return string(defaultHash), nil
}

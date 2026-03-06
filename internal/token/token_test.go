package token_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vnovakov/dns-he-net-automation/internal/store"
	"github.com/vnovakov/dns-he-net-automation/internal/token"
)

// testSecret is the HMAC-SHA256 secret used across all tests.
var testSecret = []byte("test-secret-that-is-at-least-32-chars-long!")

// openTestDB opens a fresh file-based SQLite DB in the test's temp directory.
// Using t.TempDir() (not :memory:) because WAL mode requires a real file (01-01 decision).
// store.Open runs all goose migrations including 002_tokens.sql.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(dbPath)
	require.NoError(t, err, "failed to open test database")
	t.Cleanup(func() { db.Close() })
	return db
}

// insertTestAccount inserts a test account into the DB so foreign key constraints pass.
// After migration 010, accounts have both an id (UUID) and a name (user-chosen label).
// In tests, we use the same string for both id and name to keep assertion strings
// (like "dns-he-net.acct-1.admin--") unchanged from before the migration.
func insertTestAccount(t *testing.T, db *sql.DB, accountID string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO accounts (id, name, username) VALUES (?, ?, ?)`,
		accountID, accountID, "test-user-"+accountID,
	)
	require.NoError(t, err, "failed to insert test account")
}

// TestIssueToken_Success verifies that issuing a token:
// - returns a non-empty JWT string starting with "eyJ"
// - returns a non-empty UUID jti
// - stores the token_hash (SHA-256 of rawToken) in the DB
// - stores revoked_at as NULL (token is active)
func TestIssueToken_Success(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-1")

	rawToken, jti, err := token.IssueToken(ctx, db, "acct-1", "acct-1", "admin", "my-label", "", "", 30, testSecret, nil)
	require.NoError(t, err)

	// Token format: dns-he-net.{accountName}.{role}--{jti}.{jwt}
	// In tests, accountName == accountID ("acct-1"), so the prefix is unchanged.
	assert.True(t, strings.HasPrefix(rawToken, "dns-he-net.acct-1.admin--"), "rawToken should start with readable prefix")
	assert.True(t, strings.Contains(rawToken, "--"+jti+"."), "rawToken should contain the JTI after --")
	assert.True(t, strings.Contains(rawToken, ".eyJ"), "rawToken should contain a JWT")
	assert.NotEmpty(t, jti, "jti should be a non-empty UUID")

	// Verify DB state: token_hash matches sha256 of rawToken, revoked_at is NULL.
	h := sha256.Sum256([]byte(rawToken))
	expectedHash := hex.EncodeToString(h[:])

	var storedHash string
	var revokedAt sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT token_hash, revoked_at FROM tokens WHERE jti = ?`, jti,
	).Scan(&storedHash, &revokedAt)
	require.NoError(t, err)

	assert.Equal(t, expectedHash, storedHash, "stored token_hash should be sha256 of rawToken")
	assert.False(t, revokedAt.Valid, "revoked_at should be NULL for a newly issued token")
}

// TestIssueToken_InvalidRole verifies that issuing a token with an invalid role returns an error.
func TestIssueToken_InvalidRole(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-2")

	_, _, err := token.IssueToken(ctx, db, "acct-2", "acct-2", "superadmin", "", "", "", 30, testSecret, nil)
	assert.Error(t, err, "IssueToken should return error for invalid role")
	assert.Contains(t, err.Error(), "invalid role")
}

// TestValidateToken_Valid verifies that a freshly issued token validates correctly
// and the returned claims contain the correct AccountID and Role.
func TestValidateToken_Valid(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-3")

	rawToken, _, err := token.IssueToken(ctx, db, "acct-3", "acct-3", "viewer", "read-only", "", "", 30, testSecret, nil)
	require.NoError(t, err)

	claims, err := token.ValidateToken(ctx, db, rawToken, testSecret)
	require.NoError(t, err)
	require.NotNil(t, claims)

	assert.Equal(t, "acct-3", claims.AccountID)
	assert.Equal(t, "viewer", claims.Role)
}

// TestValidateToken_Revoked verifies that after revoking a token, ValidateToken returns an error.
func TestValidateToken_Revoked(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-4")

	rawToken, jti, err := token.IssueToken(ctx, db, "acct-4", "acct-4", "admin", "", "", "", 30, testSecret, nil)
	require.NoError(t, err)

	err = token.RevokeToken(ctx, db, "acct-4", jti)
	require.NoError(t, err)

	_, err = token.ValidateToken(ctx, db, rawToken, testSecret)
	assert.Error(t, err, "ValidateToken should return error for revoked token")
	assert.Contains(t, err.Error(), "revoked")
}

// TestValidateToken_Expired verifies that a token with a past ExpiresAt is rejected.
// We directly INSERT a pre-signed JWT with a past expiry to avoid waiting.
func TestValidateToken_Expired(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-5")

	// Build a JWT with ExpiresAt in the past.
	pastTime := time.Now().Add(-1 * time.Hour)
	jtiVal := "expired-jti-test-12345"
	claims := token.Claims{
		AccountID: "acct-5",
		Role:      "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jtiVal,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(pastTime),
		},
	}
	rawToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(testSecret)
	require.NoError(t, err)

	tokenHash := sha256.Sum256([]byte(rawToken))
	tokenHashHex := hex.EncodeToString(tokenHash[:])

	// Insert the expired token directly so it exists in the DB.
	_, err = db.ExecContext(ctx,
		`INSERT INTO tokens (jti, account_id, role, token_hash, expires_at) VALUES (?, ?, ?, ?, ?)`,
		jtiVal, "acct-5", "admin", tokenHashHex, pastTime,
	)
	require.NoError(t, err)

	_, err = token.ValidateToken(ctx, db, rawToken, testSecret)
	assert.Error(t, err, "ValidateToken should return error for expired token")
}

// TestValidateToken_WrongAlgorithm verifies that a JWT using the "none" algorithm is rejected.
// This tests the WithValidMethods(["HS256"]) protection against algorithm confusion attacks.
func TestValidateToken_WrongAlgorithm(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-6")

	// Craft a JWT with "alg": "none" by manually constructing the header.
	// Header: {"alg":"none","typ":"JWT"} encoded as base64url (no padding).
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"account_id":"acct-6","role":"admin","jti":"none-alg-jti"}`))
	// "none" algorithm: signature is empty string.
	noneToken := header + "." + payload + "."

	_, err := token.ValidateToken(ctx, db, noneToken, testSecret)
	assert.Error(t, err, "ValidateToken should reject token with 'none' algorithm")
}

// TestRevokeToken_Success verifies that RevokeToken sets revoked_at in the database.
func TestRevokeToken_Success(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-7")

	_, jti, err := token.IssueToken(ctx, db, "acct-7", "acct-7", "admin", "revoke-test", "", "", 30, testSecret, nil)
	require.NoError(t, err)

	err = token.RevokeToken(ctx, db, "acct-7", jti)
	require.NoError(t, err)

	// Verify revoked_at is now set in the DB.
	var revokedAt sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT revoked_at FROM tokens WHERE jti = ?`, jti,
	).Scan(&revokedAt)
	require.NoError(t, err)
	assert.True(t, revokedAt.Valid, "revoked_at should be set after RevokeToken")
}

// TestRevokeToken_NotFound verifies that revoking a non-existent jti returns sql.ErrNoRows.
func TestRevokeToken_NotFound(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-8")

	err := token.RevokeToken(ctx, db, "acct-8", "non-existent-jti")
	assert.ErrorIs(t, err, sql.ErrNoRows, "RevokeToken should return sql.ErrNoRows for unknown jti")
}

// TestListTokens verifies that ListTokens returns all tokens for an account ordered by
// created_at DESC, and that the returned records do not contain token_hash or raw token.
//
// SQLite CURRENT_TIMESTAMP has 1-second resolution, so two tokens inserted in rapid
// succession may share the same created_at. To test ORDER BY created_at DESC reliably,
// we INSERT the "older" token directly with an explicit past timestamp, then issue the
// "newer" token via IssueToken (which uses CURRENT_TIMESTAMP).
func TestListTokens(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-9")

	// Insert an "older" token directly with an explicit past created_at.
	oldJTI := "list-test-old-jti"
	oldRawToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, token.Claims{
		AccountID: "acct-9",
		Role:      "admin",
		Label:     "first-token",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        oldJTI,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().AddDate(0, 0, 30)),
		},
	}).SignedString(testSecret)
	require.NoError(t, err)
	oldHash := sha256.Sum256([]byte(oldRawToken))
	oldHashHex := hex.EncodeToString(oldHash[:])
	_, err = db.ExecContext(ctx,
		`INSERT INTO tokens (jti, account_id, role, label, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?, datetime('now', '-1 hour'), datetime('now', '+30 days'))`,
		oldJTI, "acct-9", "admin", "first-token", oldHashHex,
	)
	require.NoError(t, err)

	// Issue the "newer" token via IssueToken — created_at defaults to CURRENT_TIMESTAMP.
	_, newJTI, err := token.IssueToken(ctx, db, "acct-9", "acct-9", "viewer", "second-token", "", "", 60, testSecret, nil)
	require.NoError(t, err)

	records, err := token.ListTokens(ctx, db, "acct-9", false)
	require.NoError(t, err)
	require.Len(t, records, 2, "should return 2 token records")

	// Verify ordered by created_at DESC: newJTI (newer) should come first.
	assert.Equal(t, newJTI, records[0].JTI, "most recent token should be first")
	assert.Equal(t, oldJTI, records[1].JTI, "older token should be second")

	// Verify fields are populated correctly for the most recent record.
	assert.Equal(t, "acct-9", records[0].AccountID)
	assert.Equal(t, "viewer", records[0].Role)
	require.NotNil(t, records[0].Label)
	assert.Equal(t, "second-token", *records[0].Label)

	// Verify token_hash is NOT present in the returned struct (TOKEN-06, SEC-02).
	// TokenRecord has no token_hash field — this is enforced by the struct definition.
	_ = records[0].JTI        // jti present
	_ = records[0].AccountID  // account_id present
	_ = records[0].Role       // role present
	_ = records[0].Label      // label present
	_ = records[0].CreatedAt  // created_at present
	// ExpiresAt and RevokedAt are optional pointers — verify they are set for non-null values.
	assert.NotNil(t, records[0].ExpiresAt, "expires_at should be set for token with expiry")
	assert.Nil(t, records[0].RevokedAt, "revoked_at should be nil for active token")
}

// TestListTokens_Empty verifies that ListTokens returns an empty slice (not nil/error)
// when no tokens exist for an account.
func TestListTokens_Empty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestAccount(t, db, "acct-10")

	records, err := token.ListTokens(ctx, db, "acct-10", false)
	require.NoError(t, err)
	assert.NotNil(t, records, "ListTokens should return empty slice, not nil")
	assert.Len(t, records, 0)
}

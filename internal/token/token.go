// Package token provides JWT bearer token issuance, validation, revocation,
// and listing for the dns-he-net-automation API authentication layer.
//
// Security properties:
//   - Only the SHA-256 hash of the signed JWT is stored in the database (TOKEN-02, SEC-02).
//   - Raw tokens are returned once at issuance and never persisted.
//   - Algorithm confusion attacks are blocked via WithValidMethods(["HS256"]) (SEC-02).
//   - Revocation is checked on every ValidateToken call via jti + token_hash lookup.
package token

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the JWT payload for dns-he-net-automation bearer tokens.
// Embeds jwt.RegisteredClaims which provides: ID (jti), ExpiresAt, IssuedAt.
type Claims struct {
	AccountID string `json:"account_id"`
	Role      string `json:"role"` // "admin" or "viewer"
	Label     string `json:"label,omitempty"`
	jwt.RegisteredClaims
}

// TokenRecord is the safe public representation of a token returned by ListTokens.
// It never exposes token_hash or the raw token value (TOKEN-06, SEC-02).
type TokenRecord struct {
	JTI       string     `json:"jti"`
	AccountID string     `json:"account_id"`
	Role      string     `json:"role"`
	Label     *string    `json:"label,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// hashToken computes the SHA-256 hash of rawToken and returns it as a hex string.
// This is the value stored in the database — the raw token is never persisted (TOKEN-02).
func hashToken(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}

// IssueToken creates a new signed JWT bearer token for the given account, stores only its
// SHA-256 hash in the database, and returns the raw token string along with the jti.
//
// Parameters:
//   - accountID: the account this token belongs to (must exist in the accounts table)
//   - role: must be "admin" or "viewer" (TOKEN-03)
//   - label: optional human-readable label (may be empty string)
//   - expiresInDays: number of days until expiry; 0 means no expiry (TOKEN-04)
//   - secret: HMAC-SHA256 signing key (should be at least 32 bytes)
//
// The returned rawToken is shown to the user once. It is NOT stored in the database.
// Only the SHA-256 hash of rawToken is stored (SEC-02).
func IssueToken(ctx context.Context, db *sql.DB, accountID, role, label string, expiresInDays int, secret []byte) (rawToken string, jti string, err error) {
	// Validate role against the allowed set (TOKEN-03).
	if role != "admin" && role != "viewer" {
		return "", "", fmt.Errorf("invalid role %q: must be \"admin\" or \"viewer\"", role)
	}

	// Compute optional expiry (TOKEN-04: nil means unlimited).
	var expiresAt *time.Time
	if expiresInDays > 0 {
		t := time.Now().AddDate(0, 0, expiresInDays)
		expiresAt = &t
	}

	// Generate a unique token ID (jti) for revocation lookups.
	jti = uuid.New().String()

	// Build registered claims.
	registered := jwt.RegisteredClaims{
		ID:       jti,
		IssuedAt: jwt.NewNumericDate(time.Now()),
	}
	if expiresAt != nil {
		registered.ExpiresAt = jwt.NewNumericDate(*expiresAt)
	}

	// Build the full claims payload.
	claims := Claims{
		AccountID: accountID,
		Role:      role,
		Label:     label,
		RegisteredClaims: registered,
	}

	// Sign with HS256. The returned string is the raw token displayed once to the user.
	rawToken, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", "", fmt.Errorf("sign jwt: %w", err)
	}

	// Compute the SHA-256 hash — this is the only token value persisted in the DB (SEC-02).
	tokenHash := hashToken(rawToken)

	// Prepare nullable values for DB storage.
	var labelVal interface{}
	if label != "" {
		labelVal = label
	}
	var expiresAtVal interface{}
	if expiresAt != nil {
		expiresAtVal = expiresAt
	}

	// Store the hash (never the raw token) in the tokens table.
	_, err = db.ExecContext(ctx,
		`INSERT INTO tokens (jti, account_id, role, label, token_hash, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		jti, accountID, role, labelVal, tokenHash, expiresAtVal,
	)
	if err != nil {
		return "", "", fmt.Errorf("store token: %w", err)
	}

	return rawToken, jti, nil
}

// ValidateToken parses and validates a raw JWT bearer token.
//
// Validation steps:
//  1. Parse with HS256-only restriction (blocks algorithm confusion attacks).
//  2. Verify the token is structurally valid and not expired.
//  3. Perform a revocation check via jti + token_hash lookup in the database.
//
// Returns the Claims on success, or an error if the token is invalid, expired,
// not found in the database, or has been revoked.
func ValidateToken(ctx context.Context, db *sql.DB, rawToken string, secret []byte) (*Claims, error) {
	var claims Claims

	// keyFunc validates the signing method and returns the secret.
	// The WithValidMethods option is the primary defense against algorithm switching attacks (SEC-02).
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}

	// Parse and validate the JWT. WithValidMethods enforces HS256 at the parser level.
	token, err := jwt.ParseWithClaims(rawToken, &claims, keyFunc, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, fmt.Errorf("parse jwt: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Revocation check: compute hash and query the database.
	tokenHash := hashToken(rawToken)

	var revokedAt sql.NullTime
	err = db.QueryRowContext(ctx,
		`SELECT revoked_at FROM tokens WHERE jti = ? AND token_hash = ?`,
		claims.ID, tokenHash,
	).Scan(&revokedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("token not found")
	}
	if err != nil {
		return nil, fmt.Errorf("revocation check: %w", err)
	}

	if revokedAt.Valid {
		return nil, fmt.Errorf("token revoked")
	}

	return &claims, nil
}

// RevokeToken marks the token identified by jti (for the given accountID) as revoked
// by setting revoked_at to the current timestamp.
//
// Returns sql.ErrNoRows if no matching active token is found (caller maps to 404).
// The accountID check ensures an account cannot revoke another account's tokens.
func RevokeToken(ctx context.Context, db *sql.DB, accountID, jti string) error {
	result, err := db.ExecContext(ctx,
		`UPDATE tokens SET revoked_at = CURRENT_TIMESTAMP WHERE jti = ? AND account_id = ?`,
		jti, accountID,
	)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// ListTokens returns all token records for the given accountID, ordered by created_at DESC.
// The response never includes token_hash or raw token values (TOKEN-06, SEC-02).
// Returns an empty slice (not an error) when no tokens exist for the account.
func ListTokens(ctx context.Context, db *sql.DB, accountID string) ([]TokenRecord, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT jti, account_id, role, label, created_at, expires_at, revoked_at
		 FROM tokens
		 WHERE account_id = ?
		 ORDER BY created_at DESC`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer rows.Close()

	var records []TokenRecord
	for rows.Next() {
		var rec TokenRecord
		var label sql.NullString
		var expiresAt sql.NullTime
		var revokedAt sql.NullTime

		if err := rows.Scan(
			&rec.JTI,
			&rec.AccountID,
			&rec.Role,
			&label,
			&rec.CreatedAt,
			&expiresAt,
			&revokedAt,
		); err != nil {
			return nil, fmt.Errorf("scan token row: %w", err)
		}

		if label.Valid {
			rec.Label = &label.String
		}
		if expiresAt.Valid {
			rec.ExpiresAt = &expiresAt.Time
		}
		if revokedAt.Valid {
			rec.RevokedAt = &revokedAt.Time
		}

		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate token rows: %w", err)
	}

	if records == nil {
		records = []TokenRecord{}
	}

	return records, nil
}

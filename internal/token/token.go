// Package token provides JWT bearer token issuance, validation, revocation,
// and listing for the dns-he-net-automation API authentication layer.
//
// Security properties:
//   - Only the SHA-256 hash of the signed JWT is stored in the database by default (TOKEN-02, SEC-02).
//   - Raw tokens are returned once at issuance and never persisted UNLESS TOKEN_RECOVERY_ENABLED=true.
//   - When recovery is enabled, the raw token is stored encrypted (AES-256-GCM) in token_value.
//     See RecoveryKey() and encryptToken() for the key derivation and encryption details.
//   - Algorithm confusion attacks are blocked via WithValidMethods(["HS256"]) (SEC-02).
//   - Revocation is checked on every ValidateToken call via jti + token_hash lookup.
package token

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ─── Token recovery helpers ────────────────────────────────────────────────────

// RecoveryKey derives the AES-256 encryption key used to encrypt/decrypt stored token values.
//
// WHY domain-separated from JWT_SECRET:
//   JWT_SECRET is an HMAC-SHA256 signing key. Using it directly as an AES key would
//   violate key-separation: compromising one domain (JWT signing) would also compromise
//   the other (token storage). The prefix "dns-he-net-token-recovery-v1|" ensures the
//   derived bytes are distinct even if the same raw secret is used for both purposes.
//
// WHY SHA-256 (not PBKDF2/scrypt):
//   The input is already a high-entropy secret (JWT_SECRET must be ≥32 bytes). A
//   password-hardening KDF is unnecessary when the source material is not a human password.
//   SHA-256 gives us the required 32-byte AES-256 key deterministically and cheaply.
//
// DEPENDENCY: the same derivation is used at issuance (encryptToken) and at reveal
//   (decryptToken). Changing this function — even just the prefix string — will make
//   all previously stored token_value ciphertexts permanently unreadable.
func RecoveryKey(jwtSecret []byte) [32]byte {
	return sha256.Sum256(append([]byte("dns-he-net-token-recovery-v1|"), jwtSecret...))
}

// encryptToken encrypts rawToken with AES-256-GCM using the provided 32-byte key.
// The output is base64-encoded "nonce || ciphertext" suitable for TEXT DB storage.
//
// WHY AES-256-GCM (not AES-CBC or ChaCha20):
//   GCM provides authenticated encryption — tampering with the stored ciphertext is
//   detected at decryption time (returns an error rather than silently producing garbage).
//   This prevents an attacker with DB write access from substituting a ciphertext that
//   decrypts to an arbitrary token.
//
// WHY random nonce per issuance (not a fixed nonce):
//   A fixed nonce with the same key would allow an attacker who observes multiple
//   ciphertexts to XOR them and recover the keystream (two-time pad attack). A fresh
//   12-byte random nonce per encryption makes each ciphertext independently secure.
func encryptToken(rawToken string, key [32]byte) (string, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	// Seal appends the ciphertext+tag to nonce, giving us [nonce|ciphertext|tag].
	sealed := gcm.Seal(nonce, nonce, []byte(rawToken), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptToken is the inverse of encryptToken. Returns the plaintext raw token or an error
// if the ciphertext is corrupt or was tampered with (GCM authentication failure).
func decryptToken(encoded string, key [32]byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("new gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", fmt.Errorf("ciphertext too short")
	}
	plaintext, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

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
//
// Recoverable is true when token_value IS NOT NULL in the DB AND TOKEN_RECOVERY_ENABLED=true.
// It is used by the admin UI to decide whether to show the "Show token" button on each row.
// When false (either the flag is off, or the token was issued before the flag was turned on),
// the reveal endpoint will return 403 even if called directly.
type TokenRecord struct {
	JTI         string     `json:"jti"`
	AccountID   string     `json:"account_id"`
	Role        string     `json:"role"`
	Label       *string    `json:"label,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	Recoverable bool       `json:"-"` // true only when feature is on AND token_value stored
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
//   - recoveryKey: when non-nil, the raw token is encrypted with AES-256-GCM and stored
//     in token_value so it can be recovered later via RevealToken. When nil, token_value
//     is left NULL and the token can never be retrieved after issuance (TOKEN-02, SEC-02).
//     Pass nil unless TOKEN_RECOVERY_ENABLED=true. Use RecoveryKey(jwtSecret) to derive.
//
// The returned rawToken is shown to the user once. It is NOT stored in plaintext — only
// the SHA-256 hash is always persisted; the encrypted form is stored only when recoveryKey != nil.
func IssueToken(ctx context.Context, db *sql.DB, accountID, role, label string, expiresInDays int, secret []byte, recoveryKey *[32]byte) (rawToken string, jti string, err error) {
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
	jwtString, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		return "", "", fmt.Errorf("sign jwt: %w", err)
	}

	// Prefix the JWT with its JTI so the token is self-identifying.
	//
	// WHY prefix with JTI (not rely on JWT payload decoding):
	//   The JTI is buried inside the base64-encoded JWT payload. An operator holding a
	//   token string has no quick way to know which DB row to revoke without base64-decoding
	//   and JSON-parsing the middle segment. Prefixing makes it trivial:
	//     token = "6f2647d8-ff3d-4922-bac5-0aca3a6f11e2.eyJhbGci..."
	//   The operator reads the first segment up to the first dot — that is the JTI to revoke.
	//
	// WHY "." as the separator (same as JWT internal separator):
	//   UUID v4 contains only hex digits and hyphens — no dots. The first dot in the
	//   prefixed token is therefore always the JTI/JWT boundary. SplitN(token, ".", 2)
	//   in ValidateToken safely strips the prefix without ambiguity.
	//
	// HASH covers the full prefixed token (not just the JWT):
	//   Both issuance (here) and validation (ValidateToken) hash the same full string,
	//   so the token_hash DB column continues to bind the token to its DB row correctly.
	rawToken = jti + "." + jwtString

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

	// Encrypt and store the raw token when the recovery feature is enabled.
	// When recoveryKey is nil, tokenValueVal stays nil → token_value column is NULL.
	// The token cannot be retrieved after this point when nil (TOKEN-02, SEC-02).
	var tokenValueVal interface{}
	if recoveryKey != nil {
		encrypted, encErr := encryptToken(rawToken, *recoveryKey)
		if encErr != nil {
			return "", "", fmt.Errorf("encrypt token for recovery: %w", encErr)
		}
		tokenValueVal = encrypted
	}

	// Store the hash and optionally the encrypted token in the tokens table.
	// token_hash is always stored (revocation check hot path).
	// token_value is stored only when recovery is enabled (see recoveryKey above).
	_, err = db.ExecContext(ctx,
		`INSERT INTO tokens (jti, account_id, role, label, token_hash, expires_at, token_value) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		jti, accountID, role, labelVal, tokenHash, expiresAtVal, tokenValueVal,
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

	// Strip the JTI prefix before JWT parsing.
	//
	// Format: {jti}.{header}.{payload}.{signature}
	// SplitN with n=2 splits on the first dot only — UUID contains no dots, so this is
	// unambiguous. The jwtString is passed to jwt.ParseWithClaims; rawToken (full prefixed)
	// is used for the token_hash DB lookup so it matches what was stored at issuance.
	parts := strings.SplitN(rawToken, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}
	jwtString := parts[1]

	// keyFunc validates the signing method and returns the secret.
	// The WithValidMethods option is the primary defense against algorithm switching attacks (SEC-02).
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}

	// Parse and validate the JWT (without the JTI prefix).
	// WithValidMethods enforces HS256 at the parser level.
	token, err := jwt.ParseWithClaims(jwtString, &claims, keyFunc, jwt.WithValidMethods([]string{"HS256"}))
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

// RevokeByJTI marks a token as revoked by its JTI alone, without an accountID scope check.
//
// WHY a separate function from RevokeToken:
//   RevokeToken(ctx, db, accountID, jti) scopes the UPDATE to a specific account — preventing
//   one account from revoking another account's tokens via the REST API. The admin UI does not
//   have the accountID in the revoke URL (DELETE /admin/tokens/{tokenID}), and admin access
//   already implies full authority over all tokens. The accountID constraint would require an
//   extra DB query to look up the account before revoking, adding unnecessary complexity.
//
// WHY not call GET /api/v1/tokens + DELETE /api/v1/... over HTTP:
//   Admin UI is in-process. Making HTTP calls to itself adds network round-trips and requires
//   token management for the admin session itself. Direct DB access is correct here.
//   (RESEARCH.md anti-pattern: admin UI must not HTTP-call API)
//
// SQL: UPDATE tokens SET revoked_at = CURRENT_TIMESTAMP WHERE jti = ? AND revoked_at IS NULL
// Returns sql.ErrNoRows if the JTI is not found or already revoked.
func RevokeByJTI(ctx context.Context, db *sql.DB, jti string) error {
	result, err := db.ExecContext(ctx,
		`UPDATE tokens SET revoked_at = CURRENT_TIMESTAMP WHERE jti = ? AND revoked_at IS NULL`,
		jti,
	)
	if err != nil {
		return fmt.Errorf("revoke by jti: %w", err)
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
//
// recoveryEnabled must be true (matching TOKEN_RECOVERY_ENABLED config) for the Recoverable
// field to be populated. When false, Recoverable is always false regardless of DB state,
// ensuring the "Show" button never appears when the feature is disabled at the config level.
// This means toggling TOKEN_RECOVERY_ENABLED=false immediately hides the Show button without
// any DB changes — existing token_value rows are preserved but inaccessible.
func ListTokens(ctx context.Context, db *sql.DB, accountID string, recoveryEnabled bool) ([]TokenRecord, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT jti, account_id, role, label, created_at, expires_at, revoked_at,
		        (token_value IS NOT NULL) AS has_stored_token
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
		var hasStoredToken bool

		if err := rows.Scan(
			&rec.JTI,
			&rec.AccountID,
			&rec.Role,
			&label,
			&rec.CreatedAt,
			&expiresAt,
			&revokedAt,
			&hasStoredToken,
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
		// Recoverable is only true when the feature flag is on AND a ciphertext exists.
		// If the flag is off, we never expose the Show button even if ciphertexts are present.
		rec.Recoverable = recoveryEnabled && hasStoredToken

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

// RevealToken fetches and decrypts the stored token value for the given JTI.
// Returns the raw token string, or an error if not found, already revoked, or decryption fails.
//
// This function is only called by the /admin/tokens/{jti}/reveal endpoint, which first
// verifies the caller's portal password before invoking this function.
// Never call this function without prior password verification.
//
// Returns sql.ErrNoRows if the JTI does not exist or token_value is NULL (token was issued
// before recovery was enabled, or the feature was off at issuance time).
func RevealToken(ctx context.Context, db *sql.DB, jti string, key [32]byte) (string, error) {
	var tokenValue sql.NullString
	var revokedAt sql.NullTime

	err := db.QueryRowContext(ctx,
		`SELECT token_value, revoked_at FROM tokens WHERE jti = ?`,
		jti,
	).Scan(&tokenValue, &revokedAt)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	if revokedAt.Valid {
		return "", fmt.Errorf("token is revoked")
	}
	if !tokenValue.Valid {
		// Token was issued before recovery was enabled or feature was off at issuance time.
		return "", sql.ErrNoRows
	}
	raw, err := decryptToken(tokenValue.String, key)
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	return raw, nil
}

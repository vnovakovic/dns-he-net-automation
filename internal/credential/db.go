package credential

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// DBProvider retrieves HE.net credentials from the accounts SQLite table.
//
// WHY a DB-backed provider (vs env var or Vault only):
//   Self-hosted operators managing a single instance found HE_ACCOUNTS JSON
//   friction-heavy — every server restart required the full JSON array.
//   Storing credentials in SQLite (0600 file, SEC-03) makes the admin UI the
//   primary credential management surface. Vault is still recommended for
//   multi-host or secret-rotation deployments.
//
// WHY plaintext storage (not encrypted):
//   The DB file is created with 0600 permissions (SEC-03, store.Open).
//   The threat model for a self-hosted tool assumes the filesystem is trusted.
//   Encrypting at-rest adds key management complexity with little benefit
//   when the decryption key must also live on the same host.
//   For higher security, use the Vault provider instead.
//
// SECURITY (SEC-03): Password values are NEVER logged or included in errors.
type DBProvider struct {
	db *sql.DB
}

// Compile-time interface satisfaction check.
var _ Provider = (*DBProvider)(nil)

// NewDBProvider creates a DBProvider backed by the given database.
// The accounts table must exist (ensured by store.Open migrations).
func NewDBProvider(db *sql.DB) *DBProvider {
	return &DBProvider{db: db}
}

// GetCredential returns the HE.net credential for the given account ID.
// Returns an error if the account is not found or its password is empty
// (empty password means the account was registered without credentials —
// the operator must update the password in the admin UI).
func (p *DBProvider) GetCredential(ctx context.Context, accountID string) (*Credential, error) {
	var c Credential
	err := p.db.QueryRowContext(ctx,
		`SELECT id, username, password FROM accounts WHERE id = ?`, accountID,
	).Scan(&c.AccountID, &c.Username, &c.Password)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("account %q not found — register it in the admin UI first", accountID)
	}
	if err != nil {
		return nil, fmt.Errorf("query credential for account %q: %w", accountID, err)
	}
	// SECURITY (SEC-03): Do NOT include the password value in this error message.
	if c.Password == "" {
		return nil, fmt.Errorf("account %q has no password configured — set it in the admin UI (Accounts page)", accountID)
	}
	return &c, nil
}

// ListAccountIDs returns all account IDs from the DB sorted alphabetically.
func (p *DBProvider) ListAccountIDs(ctx context.Context) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id FROM accounts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list account IDs from DB: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan account ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Strings(ids)
	return ids, nil
}

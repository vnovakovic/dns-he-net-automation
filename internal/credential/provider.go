// Package credential defines the interface for retrieving HE.net account credentials.
// Phase 1 uses EnvProvider (reads HE_ACCOUNTS JSON env var).
// Phase 4 will add VaultProvider (reads from HashiCorp Vault KV v2).
package credential

import "context"

// Credential holds dns.he.net account credentials for a single HE account.
// SECURITY (SEC-03): Never log or include the Password field in error messages.
type Credential struct {
	AccountID string
	Username  string
	Password  string
}

// Provider abstracts credential retrieval to allow swapping backends.
// Phase 1: EnvProvider (reads HE_ACCOUNTS JSON env var)
// Phase 4: VaultProvider (reads from HashiCorp Vault KV v2)
type Provider interface {
	// GetCredential returns credentials for the given account ID.
	// Returns an error if the account does not exist or credentials cannot be retrieved.
	GetCredential(ctx context.Context, accountID string) (*Credential, error)

	// ListAccountIDs returns all known account IDs in a deterministic (sorted) order.
	ListAccountIDs(ctx context.Context) ([]string, error)
}

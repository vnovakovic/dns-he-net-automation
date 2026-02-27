package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// envAccount is the JSON deserialization target for one entry in the HE_ACCOUNTS array.
// It is unexported because callers interact with Credential, not the raw JSON shape.
type envAccount struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// EnvProvider reads credentials from a JSON string (typically from the HE_ACCOUNTS env var).
// It validates all accounts on construction and stores them in an in-memory map.
//
// SECURITY (SEC-03): Password values are NEVER included in error messages.
type EnvProvider struct {
	accounts map[string]*Credential
}

// Compile-time interface satisfaction check.
var _ Provider = (*EnvProvider)(nil)

// NewEnvProvider parses and validates a JSON array of HE account credentials.
//
// jsonStr must be a non-empty JSON array. Each element must have non-empty id,
// username, and password fields. Duplicate IDs are rejected.
//
// SECURITY (SEC-03): Password values are never included in returned error messages.
func NewEnvProvider(jsonStr string) (*EnvProvider, error) {
	if jsonStr == "" {
		return nil, fmt.Errorf("credential JSON string is empty")
	}

	var raw []envAccount
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("parse credential JSON: %w", err)
	}

	if len(raw) == 0 {
		return nil, fmt.Errorf("credential JSON array is empty: at least one account required")
	}

	accounts := make(map[string]*Credential, len(raw))
	for i, a := range raw {
		if a.ID == "" {
			return nil, fmt.Errorf("account at index %d: id field is empty", i)
		}
		if a.Username == "" {
			return nil, fmt.Errorf("account at index %d (id=%q): username field is empty", i, a.ID)
		}
		// SECURITY (SEC-03): Do NOT include a.Password value in this error message.
		if a.Password == "" {
			return nil, fmt.Errorf("account at index %d (id=%q): password field is empty", i, a.ID)
		}

		if _, exists := accounts[a.ID]; exists {
			return nil, fmt.Errorf("duplicate account id %q", a.ID)
		}

		accounts[a.ID] = &Credential{
			AccountID: a.ID,
			Username:  a.Username,
			Password:  a.Password,
		}
	}

	return &EnvProvider{accounts: accounts}, nil
}

// GetCredential returns credentials for the given account ID.
// Returns an error if the account is not found.
func (p *EnvProvider) GetCredential(_ context.Context, accountID string) (*Credential, error) {
	cred, ok := p.accounts[accountID]
	if !ok {
		return nil, fmt.Errorf("account %q not found", accountID)
	}
	return cred, nil
}

// ListAccountIDs returns all known account IDs sorted alphabetically.
// Sorted output ensures deterministic ordering across iterations.
func (p *EnvProvider) ListAccountIDs(_ context.Context) ([]string, error) {
	ids := make([]string, 0, len(p.accounts))
	for id := range p.accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

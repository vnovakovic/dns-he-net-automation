package credential

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hashicorp/vault/api"
	approle "github.com/hashicorp/vault/api/auth/approle"
)

// VaultConfig holds the Vault-related fields from the application Config.
// It is a subset of internal/config.Config and is passed to NewVaultProvider
// to avoid a circular import between credential and config packages.
type VaultConfig struct {
	VaultAddr            string
	VaultAuthMethod      string
	VaultToken           string
	VaultAppRoleRoleID   string
	VaultAppRoleSecretID string
	VaultMountPath       string
	VaultSecretPathTmpl  string
	VaultCredentialTTLSec int
}

// cachedCred is the internal cache entry type for a fetched credential.
type cachedCred struct {
	cred      *Credential
	fetchedAt time.Time
}

// VaultProvider implements credential.Provider by fetching credentials from
// HashiCorp Vault KV v2. Credentials are fetched lazily on first request per
// account and cached in memory with a configurable TTL (VAULT-02, VAULT-03).
//
// On Vault outage, stale cached credentials are served with a warning log so
// that active sessions continue to function (VAULT-05).
//
// SECURITY (SEC-03): Credential values (username, password) are never logged.
type VaultProvider struct {
	client    *api.Client
	mountPath string
	pathTmpl  string
	ttl       time.Duration
	mu        sync.RWMutex
	cache     map[string]*cachedCred
}

// Compile-time interface satisfaction check.
var _ Provider = (*VaultProvider)(nil)

// NewVaultProvider creates and authenticates a VaultProvider using the supplied
// VaultConfig. It supports two auth methods selectable via cfg.VaultAuthMethod:
//   - "token"   — set VAULT_TOKEN; uses client.SetToken (VAULT-06)
//   - "approle" — set VAULT_APPROLE_ROLE_ID + VAULT_APPROLE_SECRET_ID (VAULT-06)
//
// Returns an error if the Vault client cannot be created or if AppRole login fails.
func NewVaultProvider(cfg *VaultConfig) (*VaultProvider, error) {
	vaultCfg := api.DefaultConfig()
	vaultCfg.Address = cfg.VaultAddr

	client, err := api.NewClient(vaultCfg)
	if err != nil {
		return nil, fmt.Errorf("vault client init: %w", err)
	}

	switch cfg.VaultAuthMethod {
	case "token", "":
		// VAULT-06: token auth -- simplest method, suitable for dev and trusted environments.
		// SECURITY: cfg.VaultToken is a credential; never log its value.
		client.SetToken(cfg.VaultToken)

	case "approle":
		// VAULT-06: AppRole auth -- platform-agnostic, suitable for Docker/bare-metal.
		// SECURITY: cfg.VaultAppRoleSecretID is a credential; never log its value.
		secretID := &approle.SecretID{FromString: cfg.VaultAppRoleSecretID}
		appRoleAuth, err := approle.NewAppRoleAuth(cfg.VaultAppRoleRoleID, secretID)
		if err != nil {
			return nil, fmt.Errorf("vault approle init: %w", err)
		}
		authInfo, err := client.Auth().Login(context.Background(), appRoleAuth)
		if err != nil {
			return nil, fmt.Errorf("vault approle login: %w", err)
		}
		// Token is set on the client automatically after Login.
		_ = authInfo

	default:
		return nil, fmt.Errorf("unsupported vault auth method %q: must be \"token\" or \"approle\"", cfg.VaultAuthMethod)
	}

	ttl := time.Duration(cfg.VaultCredentialTTLSec) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Minute // safe default
	}

	return &VaultProvider{
		client:    client,
		mountPath: cfg.VaultMountPath,
		pathTmpl:  cfg.VaultSecretPathTmpl,
		ttl:       ttl,
		cache:     make(map[string]*cachedCred),
	}, nil
}

// GetCredential returns credentials for the given accountID using a lazy fetch
// with TTL cache and stale-cache fallback on Vault outage (VAULT-02, VAULT-03, VAULT-05).
//
// Caching strategy:
//  1. RLock: return from cache if entry exists and has not expired.
//  2. RUnlock: fetch from Vault KV v2 at pathTmpl % accountID.
//  3. On Vault error: serve stale cache entry if one exists (VAULT-05); otherwise error.
//  4. On success: Lock, double-check cache (another goroutine may have populated it),
//     update cache, Unlock. (Research Pitfall 3: double-checked locking)
//
// SECURITY (SEC-03): password and username values are never included in log output.
func (p *VaultProvider) GetCredential(ctx context.Context, accountID string) (*Credential, error) {
	// Step 1: Check cache under read lock.
	p.mu.RLock()
	if cached, ok := p.cache[accountID]; ok && time.Since(cached.fetchedAt) < p.ttl {
		p.mu.RUnlock()
		return cached.cred, nil
	}
	p.mu.RUnlock()

	// Step 2: Cache miss -- fetch from Vault KV v2.
	// Using client.KVv2(mountPath).Get() handles the "data/" prefix automatically
	// (Research Pitfall 1: never use client.Logical().Read() on KV v2 mounts).
	path := fmt.Sprintf(p.pathTmpl, accountID)
	secret, err := p.client.KVv2(p.mountPath).Get(ctx, path)
	if err != nil {
		// Step 3: Vault unreachable -- serve stale cache entry if available (VAULT-05).
		p.mu.RLock()
		if cached, ok := p.cache[accountID]; ok {
			stale := cached.cred
			p.mu.RUnlock()
			slog.WarnContext(ctx, "vault unreachable, serving stale credential",
				"account", accountID,
				"stale_age", time.Since(cached.fetchedAt).Round(time.Second))
			return stale, nil
		}
		p.mu.RUnlock()
		return nil, fmt.Errorf("vault fetch for account %q: %w", accountID, err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("no secret found in vault at path for account %q", accountID)
	}

	// Step 4: Extract credentials.
	// SECURITY (SEC-03): username/password values must not appear in any log statement.
	username, _ := secret.Data["username"].(string)
	password, _ := secret.Data["password"].(string)

	cred := &Credential{
		AccountID: accountID,
		Username:  username,
		Password:  password,
	}

	// Step 5: Update cache under write lock.
	// Double-check: another goroutine may have populated the cache since our RLock.
	// (Research Pitfall 3: double-checked locking prevents thundering herd)
	p.mu.Lock()
	if existing, ok := p.cache[accountID]; ok && time.Since(existing.fetchedAt) < p.ttl {
		// Another goroutine won the race; use their result to avoid redundant writes.
		cached := existing.cred
		p.mu.Unlock()
		return cached, nil
	}
	p.cache[accountID] = &cachedCred{cred: cred, fetchedAt: time.Now()}
	p.mu.Unlock()

	return cred, nil
}

// Client returns the underlying Vault API client.
// Used by main.go to build the vaultHealthFn closure for the health endpoint.
func (p *VaultProvider) Client() *api.Client {
	return p.client
}

// ListAccountIDs is a stub for VaultProvider.
//
// VaultProvider cannot enumerate accounts: Vault KV list requires a separate
// "list" permission on the mount path and is not part of the Provider interface
// contract. Account IDs come from the SQLite accounts table -- use the database
// directly to list accounts rather than relying on Vault key enumeration.
//
// Returns an empty slice with no error.
func (p *VaultProvider) ListAccountIDs(_ context.Context) ([]string, error) {
	return []string{}, nil
}

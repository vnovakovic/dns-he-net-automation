---
phase: 04-production-hardening
plan: "01"
subsystem: credential
tags: [vault, hashicorp, kv-v2, approle, token-auth, credential-cache, ttl, config]

requires:
  - phase: 01-foundation-browser-core
    provides: credential.Provider interface (provider.go, env.go) designed for this swap

provides:
  - VaultProvider implementing credential.Provider with lazy fetch, TTL cache, stale fallback
  - Config struct extended with 15 new Phase 4 env vars (Vault + resilience + screenshot + jitter)
  - HEAccountsJSON made optional (no required/notEmpty) — Vault-only config supported

affects:
  - 04-02-resilience (uses Config.CircuitBreakerMaxFailures, Config.CircuitBreakerTimeoutSec)
  - 04-03-ratelimit-screenshot (uses Config.RateLimitPerTokenRPM, Config.ScreenshotDir)
  - main.go wiring (selects EnvProvider vs VaultProvider based on Config.VaultAddr)

tech-stack:
  added:
    - github.com/hashicorp/vault/api v1.22.0
    - github.com/hashicorp/vault/api/auth/approle v0.11.0
  patterns:
    - Double-checked locking (RLock check -> fetch -> Lock re-check) for cache miss without thundering herd
    - Stale cache fallback with slog.WarnContext on Vault outage (VAULT-05)
    - VaultConfig struct as parameter object avoids circular import between credential and config packages

key-files:
  created:
    - internal/credential/vault.go
  modified:
    - internal/config/config.go
    - internal/config/config_test.go

key-decisions:
  - "VaultConfig is a separate struct in credential package — avoids circular import (config imports credential, credential cannot import config)"
  - "HEAccountsJSON made optional (env tag only, no required/notEmpty) — service can run Vault-only; EnvProvider remains for migration period"
  - "ListAccountIDs returns empty slice stub — Vault KV list requires separate permission and account IDs come from SQLite, not Vault key enumeration"
  - "client.KVv2(mount).Get() always used — never client.Logical().Read() — to avoid KV v2 path prefix bug"
  - "Double-checked locking for cache: RLock read, RUnlock, fetch, Lock write with re-check — prevents N parallel Vault fetches on simultaneous cache miss"

patterns-established:
  - "Pattern 1: Always use client.KVv2(mountPath).Get(ctx, path) for Vault KV v2 reads — never raw Logical().Read()"
  - "Pattern 2: Stale cache on outage — serve from cache with WarnContext, not error, when Vault is unreachable and cache hit exists"
  - "Pattern 3: SECURITY — password and username values must never appear in any slog call (SEC-03)"

requirements-completed: [VAULT-01, VAULT-02, VAULT-03, VAULT-05, VAULT-06]

duration: 4min
completed: 2026-02-28
---

# Phase 4 Plan 01: VaultProvider + Config Extension Summary

**HashiCorp Vault KV v2 credential provider with lazy fetch, TTL cache, stale fallback, and token/AppRole dual auth — plus 15 new Phase 4 env vars in Config**

## Performance

- **Duration:** 4 min
- **Started:** 2026-02-28T11:57:26Z
- **Completed:** 2026-02-28T12:01:15Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- VaultProvider implements credential.Provider: lazy fetch from Vault KV v2, in-memory TTL cache (default 5 min), stale cache served with slog.WarnContext on Vault outage
- Dual auth method support: token auth (`client.SetToken`) and AppRole auth (`approle.NewAppRoleAuth + client.Auth().Login`)
- Config struct extended with 15 new env vars covering Vault (8 fields), resilience (4 fields), screenshot (1 field), and jitter (1 field) — plus HEAccountsJSON made optional
- Config tests updated: `TestLoad_MissingRequired` now validates JWT_SECRET (still required), `TestLoad_Defaults` asserts all new field defaults

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend Config with Vault and Phase 4 env vars** - `bc4c4fb` (feat)
2. **Task 2: Implement VaultProvider** - `5bd630c` (feat)

**Plan metadata:** (created in this step)

## Files Created/Modified

- `internal/credential/vault.go` - VaultProvider implementing credential.Provider; lazy fetch + TTL cache + stale fallback + token/AppRole auth
- `internal/config/config.go` - Added 15 new Phase 4 env vars; HEAccountsJSON made optional
- `internal/config/config_test.go` - Updated MissingRequired test (JWT_SECRET); extended Defaults test with new field assertions
- `go.mod` / `go.sum` - Added vault/api v1.22.0 and vault/api/auth/approle v0.11.0

## Decisions Made

- **VaultConfig as parameter object:** VaultProvider constructor accepts `*VaultConfig` (credential-package local struct) rather than `*config.Config` to avoid a circular import — config imports credential, so credential cannot import config.
- **HEAccountsJSON optional:** Removed `required,notEmpty` tags so service can start with Vault-only configuration. EnvProvider remains supported for backward compatibility during migration.
- **ListAccountIDs stub:** Returns `[]string{}, nil`. Vault KV list requires a separate `list` ACL permission and is not part of the Provider interface contract. Account IDs are managed in SQLite accounts table.
- **Double-checked locking:** RLock for cache read, RUnlock, fetch from Vault, Lock for cache write with re-check before writing. Prevents thundering herd when multiple goroutines simultaneously detect cache miss for the same account.
- **Always KVv2 helper:** `client.KVv2(mountPath).Get()` never `client.Logical().Read()` — the KV v2 path includes a `data/` sub-prefix that the helper handles automatically; raw Logical reads silently return nil on KV v2 mounts.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None. Both Vault dependencies resolved cleanly. Full project `go build ./...` passed.

## User Setup Required

None — no external service configuration required for this plan. VaultProvider is only activated when `VAULT_ADDR` is set in the environment.

## Next Phase Readiness

- VaultProvider ready for wiring into main.go credential provider selection logic
- Config has all 15 Phase 4 env vars with correct defaults — plans 04-02 through 04-04 can read from Config directly
- HEAccountsJSON optional allows Vault-only deployments in production without breaking existing env-var deployments

---
*Phase: 04-production-hardening*
*Completed: 2026-02-28*

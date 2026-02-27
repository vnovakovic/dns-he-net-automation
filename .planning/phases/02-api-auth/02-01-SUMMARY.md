---
phase: 02-api-auth
plan: 01
subsystem: auth
tags: [jwt, sqlite, golang-jwt, sha256, token-revocation, bearer-token]

# Dependency graph
requires:
  - phase: 01-foundation-browser-core
    provides: store.Open with goose migrations, sqlite modernc driver, config env pattern
provides:
  - JWT bearer token issuance (IssueToken) with HS256 signing and SHA-256 hash storage
  - Token validation (ValidateToken) with algorithm-confusion protection and revocation check
  - Token revocation (RevokeToken) via jti+account_id scoped UPDATE
  - Token listing (ListTokens) returning TokenRecord — never token_hash or raw token
  - tokens table migration (002_tokens.sql) with jti PK, account_id FK ON DELETE CASCADE
  - JWTSecret field in Config struct loaded from JWT_SECRET env var (required+notEmpty)
affects: [02-api-auth/02-02, 02-api-auth/02-03, 02-api-auth/02-04]

# Tech tracking
tech-stack:
  added: [github.com/golang-jwt/jwt/v5@v5.3.1]
  patterns:
    - Only SHA-256 hash of JWT stored in DB — raw token returned once at issuance and never persisted
    - WithValidMethods(["HS256"]) on every ParseWithClaims call — primary defense vs algorithm confusion
    - keyFunc double-checks signing method type assertion as defense-in-depth
    - jti+token_hash dual-key revocation lookup — prevents token substitution attacks
    - sql.NullString/sql.NullTime for nullable columns; pointer fields in TokenRecord for JSON omitempty
    - File-based SQLite in tests (t.TempDir) not :memory: — WAL mode requires real file (01-01 decision)

key-files:
  created:
    - internal/token/token.go
    - internal/token/token_test.go
    - internal/store/migrations/002_tokens.sql
  modified:
    - internal/config/config.go
    - go.mod
    - go.sum

key-decisions:
  - "TestListTokens uses direct INSERT with explicit datetime('-1 hour') for older token — SQLite CURRENT_TIMESTAMP has 1-second resolution, making two rapid IssueToken calls produce same created_at and non-deterministic ORDER BY DESC"
  - "hashToken is an unexported helper shared by both IssueToken and ValidateToken — single source of truth for the SHA-256 hex encoding algorithm"
  - "ListTokens returns empty slice (not nil) when no rows — Go convention for empty collections in JSON APIs"
  - "RevokeToken scopes UPDATE to account_id as well as jti — prevents cross-account token revocation"

patterns-established:
  - "SHA-256 token hashing pattern: sha256.Sum256([]byte(raw)); hex.EncodeToString(h[:])"
  - "JWT signing restriction: always pass jwt.WithValidMethods([]string{\"HS256\"}) to ParseWithClaims"
  - "Nullable DB columns use sql.NullString/sql.NullTime on scan; TokenRecord uses *string/*time.Time pointers"

requirements-completed: [TOKEN-01, TOKEN-02, TOKEN-03, TOKEN-04, TOKEN-05, TOKEN-06, TOKEN-07, SEC-02]

# Metrics
duration: 4min
completed: 2026-02-27
---

# Phase 2 Plan 01: Token Package Summary

**HS256 JWT bearer token system with SHA-256-only DB storage, jti revocation, algorithm-confusion protection, and SQLite tokens table migration**

## Performance

- **Duration:** 4 min
- **Started:** 2026-02-27T23:50:43Z
- **Completed:** 2026-02-27T23:54:25Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments
- JWT bearer token system fully implemented: IssueToken signs HS256, stores only SHA-256 hash; ValidateToken rejects expired/revoked/wrong-algorithm tokens; RevokeToken sets revoked_at; ListTokens never leaks token_hash
- SQLite tokens table migration (002_tokens.sql) with jti PK, account_id FK ON DELETE CASCADE, role CHECK constraint, token_hash UNIQUE
- 10 tests all pass using file-based temp SQLite with full goose migrations including 002_tokens.sql

## Task Commits

Each task was committed atomically:

1. **Task 1: Add JWTSecret to config and create tokens migration** - `d45ff99` (feat)
2. **Task 2: Token package — IssueToken, ValidateToken, RevokeToken, ListTokens** - `19cefef` (feat)

**Plan metadata:** _(to be added)_

## Files Created/Modified
- `internal/config/config.go` - Added JWTSecret field with env:"JWT_SECRET,required,notEmpty"
- `internal/store/migrations/002_tokens.sql` - Goose Up/Down migration for tokens table
- `internal/token/token.go` - Claims, TokenRecord, IssueToken, ValidateToken, RevokeToken, ListTokens
- `internal/token/token_test.go` - 10 tests covering all functions, expiry, revocation, alg confusion
- `go.mod` - Added github.com/golang-jwt/jwt/v5 v5.3.1
- `go.sum` - Updated checksums

## Decisions Made
- **TestListTokens ordering fix:** SQLite `CURRENT_TIMESTAMP` has 1-second granularity. Two tokens issued within the same second get identical `created_at`, making `ORDER BY created_at DESC` non-deterministic in tests. Fixed by inserting the "older" token directly with `datetime('now', '-1 hour')` and issuing the "newer" token via `IssueToken` (using CURRENT_TIMESTAMP), ensuring reliable DESC ordering.
- **RevokeToken scoped to account_id:** The UPDATE includes `AND account_id = ?` to prevent one account from revoking another account's tokens. Returns `sql.ErrNoRows` (maps to 404) when no row matches.
- **ListTokens returns `[]TokenRecord{}` not nil:** Empty slice for JSON API callers — marshals to `[]` not `null`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed non-deterministic TestListTokens ordering due to CURRENT_TIMESTAMP resolution**
- **Found during:** Task 2 (first test run)
- **Issue:** SQLite CURRENT_TIMESTAMP has 1-second precision. Two consecutive IssueToken calls in a single test produce identical created_at values, making ORDER BY created_at DESC return either token first. Test asserted jti2 (newer) first but received jti1.
- **Fix:** Replaced second IssueToken call with a direct DB INSERT using `datetime('now', '-1 hour')` for the older token, ensuring a guaranteed 1-hour gap between the two rows. The newer token is still issued via IssueToken.
- **Files modified:** internal/token/token_test.go
- **Verification:** TestListTokens passes reliably (no timing dependency)
- **Committed in:** 19cefef (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — bug in test logic)
**Impact on plan:** Test-only fix, no production code change. Token ordering behavior is correct; only the test method for verifying it needed adjustment.

## Issues Encountered
None beyond the TestListTokens ordering fix documented above.

## User Setup Required
None — JWT_SECRET env var requirement is documented in config.go comments. The service will refuse to start if JWT_SECRET is absent or empty (required+notEmpty tags).

## Next Phase Readiness
- Token layer is complete and tested. Phase 2 plans that build HTTP handlers can import `internal/token` and call IssueToken/ValidateToken/RevokeToken/ListTokens directly.
- JWT_SECRET must be added to the deployment environment before the API layer is usable.
- The tokens table will be created automatically by goose when the service starts with a fresh or migrated DB.

---
*Phase: 02-api-auth*
*Completed: 2026-02-27*

## Self-Check: PASSED

| Item | Status |
|------|--------|
| internal/token/token.go | FOUND |
| internal/token/token_test.go | FOUND |
| internal/store/migrations/002_tokens.sql | FOUND |
| internal/config/config.go | FOUND |
| .planning/phases/02-api-auth/02-01-SUMMARY.md | FOUND |
| Commit d45ff99 | FOUND |
| Commit 19cefef | FOUND |

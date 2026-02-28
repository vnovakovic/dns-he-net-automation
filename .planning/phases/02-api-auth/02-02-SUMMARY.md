---
phase: 02-api-auth
plan: 02
subsystem: api
tags: [chi, http-router, jwt-middleware, rbac, account-crud, token-management, bootstrap-cli, graceful-shutdown]

# Dependency graph
requires:
  - phase: 02-api-auth/02-01
    provides: token.IssueToken, ValidateToken, RevokeToken, ListTokens, Claims struct, tokens table migration
  - phase: 01-foundation-browser-core
    provides: store.Open with goose migrations, browser.SessionManager, credential.Provider
provides:
  - chi HTTP router (NewRouter) with BearerAuth + RequireAdmin middleware stack
  - JSON error helper WriteError with {"error","code"} shape (API-04)
  - BearerAuth middleware: JWT parse via token.ValidateToken + typed context key + slog structured log
  - RequireAdmin middleware: role claim check from context, 403 for non-admin
  - Account CRUD handlers: CreateAccount (201), ListAccounts (200), GetAccount (200/404), DeleteAccount (204/404)
  - Token management handlers: IssueToken (201, raw JWT once), ListTokens (200), RevokeToken (204/404)
  - Account isolation enforced on all per-account endpoints (ACCT-04)
  - Input validation on all mutation endpoints: pattern ^[a-zA-Z0-9_-]{1,64}$ (SEC-04)
  - HTTP server with graceful 30-second shutdown on SIGTERM/SIGINT
  - Bootstrap CLI subcommand: ./server token create --account <id> --role admin|viewer
affects: [02-api-auth/02-03, 02-api-auth/02-04]

# Tech tracking
tech-stack:
  added: [github.com/go-chi/chi/v5@v5.2.5]
  patterns:
    - Typed context key (type contextKey string) prevents cross-package context collisions
    - BearerAuth delegates entirely to token.ValidateToken — no duplicate JWT parsing logic
    - WriteError sets Content-Type before WriteHeader — avoids double-header writes
    - RequireAdmin must follow BearerAuth in chain — reads claims from context, not re-parses token
    - errors.Is(err, http.ErrServerClosed) guard required for graceful shutdown — prevents spurious fatal log
    - account isolation pattern: chi.URLParam(r, "accountID") == claims.AccountID enforced in every per-account handler
    - Bootstrap flag parsing: skip positional "create" arg before parsing --account/--role flags (flag.Parse stops at non-flag args)
    - Bootstrap INSERT OR IGNORE for account: tokens FK to accounts requires row to exist; bootstrap auto-creates

key-files:
  created:
    - internal/api/response/errors.go
    - internal/api/middleware/auth.go
    - internal/api/middleware/rbac.go
    - internal/api/router.go
    - internal/api/handlers/accounts.go
    - internal/api/handlers/tokens.go
  modified:
    - cmd/server/main.go
    - go.mod
    - go.sum
    - internal/config/config_test.go

key-decisions:
  - "Bootstrap CLI skips 'create' positional arg: os.Args[2] == 'create' detected, parsing starts at os.Args[3:] — flag.Parse stops at first non-flag arg so positional must be skipped"
  - "Bootstrap INSERT OR IGNORE for account: tokens table has FK -> accounts; true bootstrap scenario needs account row before issuing first token; idempotent on repeated runs"
  - "chiMiddleware.Logger excluded from router: it uses log.Printf not slog — structured logging handled in BearerAuth via slog.InfoContext instead"
  - "Config tests fixed: JWT_SECRET added as required in 02-01 but TestLoad_Defaults/TestLoad_CustomValues were not updated — both tests now set JWT_SECRET via t.Setenv"

patterns-established:
  - "Error response pattern: WriteError(w, status, \"snake_case_code\", \"human message\") — always sets Content-Type before WriteHeader"
  - "Account isolation pattern: claims := middleware.ClaimsFromContext(r.Context()); if claims == nil || claims.AccountID != accountID { 403 }"
  - "Handler factory pattern: handlers return http.HandlerFunc closures capturing db/sm/secret dependencies"

requirements-completed: [TOKEN-01, TOKEN-03, TOKEN-04, TOKEN-05, TOKEN-06, TOKEN-07, ACCT-01, ACCT-02, ACCT-03, ACCT-04, API-01, API-02, API-03, API-04, API-07, SEC-01, SEC-04]

# Metrics
duration: 6min
completed: 2026-02-28
---

# Phase 2 Plan 02: HTTP API Layer Summary

**chi router with BearerAuth/RBAC middleware, full account CRUD + token management handlers, graceful HTTP server, and bootstrap CLI for first-token issuance**

## Performance

- **Duration:** 6 min
- **Started:** 2026-02-27T23:57:19Z
- **Completed:** 2026-02-28T00:02:44Z
- **Tasks:** 2
- **Files modified:** 10

## Accomplishments
- Full API surface: 7 endpoints (POST/GET accounts, GET/DELETE account, GET/POST/DELETE tokens) all protected by JWT bearer auth
- BearerAuth middleware integrates with token.ValidateToken from 02-01 — no JWT logic duplication; structured slog logs per request
- Account isolation enforced on every per-account endpoint: token for account A returns 403 on account B's resources
- HTTP server starts in goroutine, graceful shutdown with 30-second timeout on SIGTERM/SIGINT
- Bootstrap CLI `./server token create --account <id> --role admin` works without HTTP server; solves first-token chicken-and-egg

## Task Commits

Each task was committed atomically:

1. **Task 1: Error helper, auth middleware, RBAC middleware, chi router** - `b25e68e` (feat)
2. **Task 2: Account/token handlers, HTTP server wiring, bootstrap CLI** - `acd750a` (feat)
3. **Auto-fix: Config test JWT_SECRET** - `bcbab84` (fix)

**Plan metadata:** _(to be added by final commit)_

## Files Created/Modified
- `internal/api/response/errors.go` - WriteError helper, ErrorResponse struct with {"error","code"} JSON shape
- `internal/api/middleware/auth.go` - BearerAuth: typed context key, token.ValidateToken, slog structured log
- `internal/api/middleware/rbac.go` - RequireAdmin: reads claims from context, 403 for non-admin
- `internal/api/router.go` - NewRouter: chi router with RequestID/RealIP/Recoverer, all account+token routes
- `internal/api/handlers/accounts.go` - CreateAccount, ListAccounts, GetAccount, DeleteAccount with isolation+validation
- `internal/api/handlers/tokens.go` - IssueToken, ListTokens, RevokeToken with isolation+validation
- `cmd/server/main.go` - HTTP server goroutine, graceful shutdown, bootstrap 'token create' subcommand
- `go.mod` / `go.sum` - Added github.com/go-chi/chi/v5@v5.2.5
- `internal/config/config_test.go` - Set JWT_SECRET in TestLoad_Defaults and TestLoad_CustomValues

## Decisions Made
- **Bootstrap CLI flag parsing fix:** `flag.Parse` stops at the first non-flag argument. When invoked as `./server token create --account prod`, the `"create"` positional arg causes `--account` to be ignored. Fixed by detecting `os.Args[2] == "create"` and parsing from `os.Args[3:]`.
- **Bootstrap auto-creates account row:** The tokens table has a FOREIGN KEY constraint on `account_id` referencing accounts. A true bootstrap scenario (no accounts yet) requires the account row to exist before issuing the first token. Using `INSERT OR IGNORE` makes repeated bootstrap calls idempotent.
- **chiMiddleware.Logger excluded:** Chi's built-in Logger middleware uses `log.Printf`. All request logging is handled in BearerAuth via `slog.InfoContext` for structured JSON output consistency.
- **Config tests repaired:** JWT_SECRET was added as a required field in plan 02-01 but config_test.go was not updated. TestLoad_Defaults and TestLoad_CustomValues both failed without setting JWT_SECRET. Fixed via t.Setenv in both tests.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] flag.Parse stops at positional args — bootstrap --account flag ignored**
- **Found during:** Task 2 (smoke test of bootstrap CLI)
- **Issue:** `flag.NewFlagSet` stops parsing at the first non-flag argument. `os.Args` for `./server token create --account prod` is `[binary, "token", "create", "--account", "prod"]`. Passing `os.Args[2:]` = `["create", "--account", "prod"]` causes the parser to stop at `"create"`, never seeing `--account`. Result: `*accountID` was always empty, triggering the "error: --account is required" exit.
- **Fix:** Detect if `os.Args[2] == "create"` and parse from `os.Args[3:]` instead. Backward compatible for callers who omit the `"create"` verb.
- **Files modified:** cmd/server/main.go
- **Verification:** `./server token create --account prod --role admin` outputs JWT to stdout
- **Committed in:** acd750a (Task 2 commit)

**2. [Rule 2 - Missing Critical] Bootstrap fails with FK constraint when account row doesn't exist**
- **Found during:** Task 2 (smoke test of bootstrap CLI)
- **Issue:** The tokens table has `FOREIGN KEY (account_id) REFERENCES accounts(id)`. A true bootstrap scenario has an empty DB — account "prod" doesn't exist yet, so `token.IssueToken` fails with "FOREIGN KEY constraint failed".
- **Fix:** Added `INSERT OR IGNORE INTO accounts (id, username) VALUES (?, ?)` before `IssueToken` in the bootstrap subcommand. Uses the accountID as both id and username (can be corrected later via API). `INSERT OR IGNORE` makes repeated calls idempotent.
- **Files modified:** cmd/server/main.go
- **Verification:** Fresh DB bootstrap successfully issues token with automatic account creation
- **Committed in:** acd750a (Task 2 commit)

**3. [Rule 1 - Bug] Config tests failing — JWT_SECRET required field not set in test fixtures**
- **Found during:** Post-task verification (`go test ./internal/...`)
- **Issue:** JWT_SECRET was added as `required,notEmpty` in plan 02-01. TestLoad_Defaults and TestLoad_CustomValues only set HE_ACCOUNTS and other fields, not JWT_SECRET. Both tests fail with "required environment variable JWT_SECRET is not set". Pre-existing failure from 02-01 that wasn't caught because that plan only tested `./internal/token/...`.
- **Fix:** Added `t.Setenv("JWT_SECRET", "test-secret-at-least-32-chars-long")` to TestLoad_Defaults and `t.Setenv("JWT_SECRET", "custom-secret-at-least-32-chars-long")` to TestLoad_CustomValues.
- **Files modified:** internal/config/config_test.go
- **Verification:** `go test ./internal/config/...` passes (all 3 tests)
- **Committed in:** bcbab84 (separate fix commit)

---

**Total deviations:** 3 auto-fixed (2 Rule 1 bugs, 1 Rule 2 missing critical)
**Impact on plan:** All fixes essential for correctness. No scope creep. Bootstrap is fully functional.

## Issues Encountered
- Server startup takes ~7 seconds in smoke tests due to Playwright browser launch (expected — browser is part of service startup, not API). Tests that only need the HTTP layer should use a lighter test harness (plan 02-03).

## User Setup Required
None — all required env vars documented in config.go. Bootstrap usage: `HE_ACCOUNTS=dummy JWT_SECRET=<min-32-chars> DB_PATH=<path> ./server token create --account <id> --role admin`

## Next Phase Readiness
- Full API surface is live and auth-protected. Plan 02-03 can add healthz endpoint and integration tests against the running server.
- No per-account browser session close method exists on SessionManager yet — DeleteAccount has a TODO comment for Phase 3.
- The accounts table `username` field uses `INSERT OR IGNORE` in bootstrap with accountID as username placeholder; operators should update via PUT /api/v1/accounts/{id} if needed (Phase 3 or later).

---
*Phase: 02-api-auth*
*Completed: 2026-02-28*

## Self-Check: PASSED

| Item | Status |
|------|--------|
| internal/api/response/errors.go | FOUND |
| internal/api/middleware/auth.go | FOUND |
| internal/api/middleware/rbac.go | FOUND |
| internal/api/router.go | FOUND |
| internal/api/handlers/accounts.go | FOUND |
| internal/api/handlers/tokens.go | FOUND |
| cmd/server/main.go | FOUND |
| .planning/phases/02-api-auth/02-02-SUMMARY.md | FOUND |
| Commit b25e68e | FOUND |
| Commit acd750a | FOUND |
| Commit bcbab84 | FOUND |

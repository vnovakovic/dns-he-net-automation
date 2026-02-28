---
phase: 06-bind-import-export-admin-ui
plan: "03"
subsystem: ui
tags: [templ, htmx, admin-ui, sqlite, token-management, account-management]

# Dependency graph
requires:
  - phase: 06-bind-import-export-admin-ui
    plan: "02"
    provides: RegisterAdminRoutes FINAL signature with stub handlers, templ Layout + LoginPage components, AdminAuth middleware
  - phase: 02-api-auth
    provides: token.IssueToken, token.ListTokens, token.RevokeToken, TokenRecord struct, JWT infrastructure
provides:
  - token.RevokeByJTI — admin-scoped token revocation by JTI without accountID constraint
  - accounts.templ + accounts_templ.go — AccountsPage with inline htmx form, AccountRow with htmx delete
  - tokens.templ + tokens_templ.go — TokensPage (lazy load), TokensForAccount, TokenRow, IssueTokenForm, NewTokenResult
  - All 7 account/token admin handler stubs replaced with real DB/token implementations
affects: [06-04-admin-zones-sync-audit]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "DB-direct pattern for admin handlers: no store.* wrappers — inline ExecContext/QueryRowContext matching REST handler style"
    - "Lazy htmx load pattern: account sections start collapsed, tokens loaded on-demand via GET /admin/tokens/{accountID}"
    - "Shown-once token reveal: NewTokenResult shows raw JWT in htmx response only — never stored or shown again (SEC-02)"

key-files:
  created:
    - internal/api/admin/templates/accounts.templ
    - internal/api/admin/templates/accounts_templ.go
    - internal/api/admin/templates/tokens.templ
    - internal/api/admin/templates/tokens_templ.go
  modified:
    - internal/token/token.go
    - internal/api/admin/router.go

key-decisions:
  - "Admin handlers call DB directly — store package only provides Open(); inline DB queries mirror REST handler pattern (handlers/accounts.go)"
  - "token.RevokeByJTI added to token.go — separates admin (JTI-only) from user (accountID-scoped) revocation; prevents adding unnecessary DB join in admin path"
  - "Lazy token loading via htmx GET /admin/tokens/{accountID} — avoids N+1 token queries on page load for multi-account deployments"
  - "listAccountsFromDB extracted as shared helper — both handleAccountsPage and handleTokensPage need account list with same query"
  - "handleAccountDelete returns HTTP 200 with empty body (not 204) — htmx hx-swap=outerHTML replaces row with empty body effectively removing it"

requirements-completed: [UI-02, UI-03]

# Metrics
duration: 3min
completed: 2026-02-28
---

# Phase 6 Plan 03: Admin Accounts and Tokens UI Summary

**Account management UI (htmx inline Create/Remove) and token management UI (lazy-load per account, issue once with raw JWT reveal, JTI-only revoke) — all 7 stub handlers replaced, `go build ./...` exits 0**

## Performance

- **Duration:** 3 min
- **Started:** 2026-02-28T22:46:07Z
- **Completed:** 2026-02-28T22:49:00Z
- **Tasks:** 2
- **Files modified:** 6 (4 created, 2 modified)

## Accomplishments

- token.RevokeByJTI added to token.go — admin UI revokes by JTI alone without needing accountID (full authority implied by admin access)
- accounts.templ: AccountsPage with htmx inline form (hx-swap=beforeend on tbody) + AccountRow with hx-confirm delete guard
- tokens.templ: TokensPage with lazy htmx load, TokensForAccount with table + IssueTokenForm, TokenRow with revoke guard, NewTokenResult showing raw JWT once
- All 7 plan 02 stub handlers replaced (handleAccountsPage, handleAccountCreate, handleAccountDelete, handleTokensPage, handleTokensForAccount, handleTokenIssue, handleTokenRevoke)

## Task Commits

Each task was committed atomically:

1. **Task 1: Add token.RevokeByJTI, accounts templ components and handler implementation** - `3135383` (feat)
2. **Task 2: Tokens templ components and handler implementation** - `8d22ba9` (feat)

**Plan metadata:** *(see final commit below)*

## Files Created/Modified

- `internal/token/token.go` - Added RevokeByJTI function (JTI-only revocation for admin use)
- `internal/api/admin/router.go` - Replaced 7 stub handlers with real DB/token implementations; added listAccountsFromDB helper; added strings, model, token imports
- `internal/api/admin/templates/accounts.templ` - AccountsPage with htmx form + AccountRow with hx-confirm delete
- `internal/api/admin/templates/accounts_templ.go` - Templ-generated (committed per RESEARCH.md Pitfall 4)
- `internal/api/admin/templates/tokens.templ` - TokensPage, TokensForAccount, TokenRow, IssueTokenForm, NewTokenResult, labelOrDash, expiresStr
- `internal/api/admin/templates/tokens_templ.go` - Templ-generated (committed per RESEARCH.md Pitfall 4)

## Decisions Made

- Admin handlers use inline DB queries directly (no store.* wrappers) — the store package only exposes Open() for DB initialization; CRUD is done inline following the REST handlers pattern in internal/api/handlers/accounts.go
- token.RevokeByJTI is a separate function from token.RevokeToken — RevokeToken requires accountID for cross-account protection via REST API; admin has full authority so JTI-only is correct and avoids an extra DB lookup
- Lazy token loading via htmx GET per account — prevents loading all tokens for all accounts on initial page render
- handleAccountDelete returns 200 with empty body (not 204 No Content) — both work with htmx hx-swap=outerHTML, but 200+empty is explicit about htmx replacing the row element with nothing
- listAccountsFromDB extracted as a shared helper — both the accounts page and tokens page need the same account list query with the same column set

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Plan referenced store.ListAccounts, store.CreateAccount, store.DeleteAccount which do not exist**
- **Found during:** Task 1 (account handler implementation)
- **Issue:** The plan's code samples called `store.ListAccounts(db)`, `store.CreateAccount(db, id, username)`, `store.DeleteAccount(db, id)` — these functions do not exist. The store package only provides `Open()`. Account CRUD in the REST API is done via inline DB queries in internal/api/handlers/accounts.go.
- **Fix:** Implemented account handlers with direct DB queries (ExecContext/QueryRowContext) following the same pattern as the REST handlers. Added a `listAccountsFromDB` helper since two handlers (accounts page + tokens page) share the same query.
- **Files modified:** internal/api/admin/router.go
- **Verification:** go build ./... exits 0; handlers call DB directly; no unused imports
- **Committed in:** 3135383 (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - non-existent function references in plan code samples)
**Impact on plan:** The fix follows the correct established pattern (REST handler style). No scope creep. All functionality as specified is delivered.

## Issues Encountered

None beyond the auto-fixed deviation above.

## User Setup Required

None — no new environment variables or external service configuration required. The admin UI was already configured in plan 02 (ADMIN_USERNAME, ADMIN_PASSWORD, ADMIN_SESSION_KEY).

## Next Phase Readiness

- Plan 04 (admin zones + sync + audit page): ready — stub handlers handleZonesPage, handleSyncPage, handleSyncTrigger, handleAuditPage accept full parameter set; replace bodies only
- RegisterAdminRoutes signature unchanged — no main.go edits required for plan 04
- Accounts and tokens management fully functional — operators can register/remove accounts and issue/revoke tokens via UI

## Self-Check: PASSED

All files verified present on disk. All task commits verified in git log.

- internal/token/token.go: FOUND
- internal/api/admin/router.go: FOUND
- internal/api/admin/templates/accounts.templ: FOUND
- internal/api/admin/templates/accounts_templ.go: FOUND
- internal/api/admin/templates/tokens.templ: FOUND
- internal/api/admin/templates/tokens_templ.go: FOUND
- Task 1 commit 3135383: FOUND
- Task 2 commit 8d22ba9: FOUND

---
*Phase: 06-bind-import-export-admin-ui*
*Completed: 2026-02-28*

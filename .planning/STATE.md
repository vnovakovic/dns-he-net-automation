# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-02-26)

**Core value:** External systems can manage DNS records on dns.he.net via a REST API as if it were a first-class DNS provider, without any manual web interaction.
**Current focus:** Phase 2: HTTP API Layer

## Current Position

Phase: 2 of 6 (HTTP API Layer)
Plan: 1 of 5 in phase 2 (02-01 complete)
Status: In Progress
Last activity: 2026-02-27 -- Plan 02-01 complete, token package + migration

Progress: [████░░░░░░] 23%

## Performance Metrics

**Velocity:**
- Total plans completed: 3
- Average duration: 8 min
- Total execution time: 0.38 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-foundation-browser-core | 3/3 | 25 min | 8 min |
| 02-api-auth | 1/5 | 4 min | 4 min |

**Recent Trend:**
- Last 5 plans: 9 min, 7 min, 9 min, 4 min
- Trend: Consistent

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- [Roadmap]: Browser core is Phase 1 (highest risk, must de-risk before API work)
- [Roadmap]: Vault integration deferred to Phase 4 (use env-var credential interface in Phase 1-3)
- [Roadmap]: Six phases at standard depth -- derived from requirement dependencies, not imposed
- [01-01]: Use required+notEmpty tags on HE_ACCOUNTS (required alone only checks env var existence, notEmpty catches empty string)
- [01-01]: WAL mode test requires temp file database (in-memory databases always use "memory" journal mode)
- [01-01]: DB file created via os.OpenFile before sql.Open to ensure 0600 permissions set before SQLite creates WAL sidecars
- [01-01]: Upgraded go.mod to 1.25 (forced by goose v3.27.0 minimum requirement)
- [01-02]: Goroutine-based queue timeout (not TryLock): goroutine sends on non-buffered channel or receives from done channel on timeout/cancel, ensuring no goroutine leak or abandoned lock
- [01-02]: ensureHealthy is a stub in 01-02 -- creates context+page if nil, nil-launcher safe for unit tests; real login logic deferred to 01-03
- [01-02]: Integration tests in separate file with //go:build integration tag -- unit tests never require Chromium
- [01-02]: playwright-go was not actually in go.mod despite 01-01 SUMMARY claiming it was -- added here as blocking dependency
- [01-03]: WaitForLoadState requires PageWaitForLoadStateOptions{State: &loadState} struct not bare *playwright.LoadState -- playwright-go v0.5700.1 API
- [01-03]: Integration test build tags must be at file level (//go:build integration at top of file before package) -- not at function level or test level
- [02-01]: TestListTokens uses direct INSERT with datetime('-1 hour') for older token -- SQLite CURRENT_TIMESTAMP has 1-second resolution, two rapid IssueToken calls produce same created_at and non-deterministic ORDER BY DESC
- [02-01]: hashToken unexported helper shared by IssueToken and ValidateToken -- single source of truth for SHA-256 hex encoding
- [02-01]: RevokeToken scopes UPDATE to account_id as well as jti -- prevents cross-account token revocation

### Pending Todos

None.

### Blockers/Concerns

- [Phase 1]: HE session expiry timing is undocumented -- start with 30-minute assumption (CONTEXT.md), tune empirically
- RESOLVED [Phase 1]: dns.he.net HTML structure -- verified via Playwright MCP (all selectors in CONTEXT.md)

## Session Continuity

Last session: 2026-02-27
Stopped at: Completed 02-01-PLAN.md (JWT token package: IssueToken/ValidateToken/RevokeToken/ListTokens, 002_tokens.sql migration, JWTSecret in config)
Resume file: None

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-02-26)

**Core value:** External systems can manage DNS records on dns.he.net via a REST API as if it were a first-class DNS provider, without any manual web interaction.
**Current focus:** Phase 4: Production Hardening

## Current Position

Phase: 4 of 6 (Production Hardening)
Plan: 2 of 4 in phase 4 (04-02 complete)
Status: In Progress
Last activity: 2026-02-28 -- Plan 04-02 complete, resilience layer: WithRetry + BreakerRegistry + PerTokenRateLimit + GlobalRateLimit + SessionManager jitter

Progress: [█████████░] 65%

## Performance Metrics

**Velocity:**
- Total plans completed: 9
- Average duration: 5 min
- Total execution time: 0.97 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-foundation-browser-core | 3/3 | 25 min | 8 min |
| 02-api-auth | 3/5 | 13 min | 4 min |
| 03-dns-operations | 3/3 | 30 min | 10 min |
| 04-production-hardening | 2/4 | 9 min | 4 min |

**Recent Trend:**
- Last 5 plans: 6 min, 3 min, 12 min, 15 min, 4 min
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
- [02-02]: Bootstrap CLI skips "create" positional arg before parsing flags -- flag.Parse stops at first non-flag arg, os.Args[2] == "create" detected and parseArgs starts at os.Args[3:]
- [02-02]: Bootstrap INSERT OR IGNORE for account row -- tokens FK requires accounts row; bootstrap auto-creates with accountID as username, idempotent on repeated runs
- [02-02]: chiMiddleware.Logger excluded from router -- uses log.Printf not slog; structured request logging in BearerAuth via slog.InfoContext instead
- [02-03]: Custom panic recovery replaces chiMiddleware.Recoverer -- chi default returns plain text 500; inline middleware calls response.WriteError for JSON error contract (API-04)
- [02-03]: context.Background() for shutdownCtx -- signal context is already Done at shutdown time; using it as parent gives zero drain window; context.Background() provides full 30s
- [02-03]: Browser health returns "not connected" for nil launcher -- guards nil pointer dereference and correctly signals degraded status
- [03-01]: playwright-go v0.5700.1 Dialog API uses page.OnDialog(func(dialog playwright.Dialog)) not page.On("dialog", ...) -- typed method not generic event emitter
- [03-01]: Dialog.Accept("DELETE") variadic form serves as both Fill+Accept in one call -- no separate Fill method exists in this API version
- [03-01]: GetZoneID error return treated as zone-not-found for idempotency in CreateZone pre-check and DeleteZone verification
- [03-01]: ZoneResponse.FetchedAt set to handler start time (before WithAccount) for consistent timestamps across list items
- [03-02]: ParseRecordRow uses InnerText on individual td Locators (cells.Nth(idx).InnerText()) -- td cells hold text content not attribute values
- [03-02]: ListRecords skips locked rows with slog.Warn rather than returning error -- SOA and system rows cannot be managed and should not block list operations
- [03-02]: validateRecordFields extracted as shared helper to avoid duplicating MX/SRV validation between CreateRecord and UpdateRecord
- [03-02]: UpdateRecord calls ParseRecordRow after FillAndSubmit to return authoritative server-side record state rather than echoing request body
- [03-03]: validate package is self-contained — v1Types map replicated from handlers to avoid circular import; ValidateRecord is authoritative for all field constraints
- [03-03]: ?type filter uses strings.ToUpper for case-insensitive matching; ?name filter uses strings.EqualFold (DNS names are case-insensitive)
- [03-03]: WriteJSON added to response package following existing WriteError pattern; all handler success paths migrated
- [03-03]: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 cross-compilation verified — modernc.org/sqlite is pure Go, no CGO needed
- [04-02]: isTransientBrowserError excludes Vault credential errors — those are handled by stale cache (research Pitfall 5), not retry loops
- [04-02]: BreakerRegistry.Execute wraps ErrOpenState into descriptive message rather than exposing gobreaker error directly — cleaner handler layer mapping to 503
- [04-02]: PerTokenRateLimit falls back to RemoteAddr when no Bearer token — safe to register even before BearerAuth
- [04-02]: math/rand used for jitter (not crypto/rand) — jitter is rate-limiting anti-fingerprinting, not cryptographic randomness

### Pending Todos

None.

### Blockers/Concerns

- [Phase 1]: HE session expiry timing is undocumented -- start with 30-minute assumption (CONTEXT.md), tune empirically
- RESOLVED [Phase 1]: dns.he.net HTML structure -- verified via Playwright MCP (all selectors in CONTEXT.md)

## Session Continuity

Last session: 2026-02-28
Stopped at: Completed 04-02-PLAN.md (resilience layer: WithRetry + BreakerRegistry + PerTokenRateLimit + GlobalRateLimit + SessionManager jitter)
Resume file: None

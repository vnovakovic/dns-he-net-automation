# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-02-26)

**Core value:** External systems can manage DNS records on dns.he.net via a REST API as if it were a first-class DNS provider, without any manual web interaction.
**Current focus:** Phase 7: Fix browser automation CSS selector bugs and admin UI user registration

## Current Position

Phase: 7 of 7 (Fix browser automation CSS selector bugs and admin UI user registration)
Plan: 1 of 1 in phase 7 (07-01 complete — 3 CSS selector bugs + admin user registration htmx target bug)
Status: Complete
Last activity: 2026-03-03 -- Phase 7 complete: fixed EditExistingRecord numeric ID selector, SelectorRecordSubmit value constraint, handleBrowserError silent logging, and admin Users empty-table htmx target bug

Progress: [████████████████] 100% (1/1 plans in phase 7)

## Performance Metrics

**Velocity:**
- Total plans completed: 11
- Average duration: 5 min
- Total execution time: 1.1 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-foundation-browser-core | 3/3 | 25 min | 8 min |
| 02-api-auth | 3/5 | 13 min | 4 min |
| 03-dns-operations | 3/3 | 30 min | 10 min |
| 04-production-hardening | 4/4 | 18 min | 5 min |

**Recent Trend:**
- Last 5 plans: 12 min, 15 min, 4 min, 2 min, 7 min
- Trend: Consistent

| 05-observability-sync-engine | 5/5 (complete) | 3 min (P05) | - |

*Updated after each plan completion*
| Phase 06 P02 | 7 | 2 tasks | 13 files |
| Phase 06 P03 | 3 | 2 tasks | 6 files |

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
- [04-01]: VaultConfig is a separate struct in credential package — avoids circular import (config imports credential, credential cannot import config)
- [04-01]: HEAccountsJSON made optional (no required/notEmpty tags) — service can run Vault-only; EnvProvider remains for migration period
- [04-01]: ListAccountIDs returns empty slice stub in VaultProvider — Vault KV list requires separate permission; account IDs come from SQLite
- [04-01]: Always use client.KVv2(mount).Get() — never client.Logical().Read() — to avoid KV v2 data/ prefix issue
- [04-01]: Double-checked locking for cache: RLock read, RUnlock, fetch, Lock write with re-check — prevents thundering herd on simultaneous cache miss
- [04-02]: isTransientBrowserError excludes Vault credential errors — those are handled by stale cache (research Pitfall 5), not retry loops
- [04-02]: BreakerRegistry.Execute wraps ErrOpenState into descriptive message rather than exposing gobreaker error directly — cleaner handler layer mapping to 503
- [04-02]: PerTokenRateLimit falls back to RemoteAddr when no Bearer token — safe to register even before BearerAuth
- [04-02]: math/rand used for jitter (not crypto/rand) — jitter is rate-limiting anti-fingerprinting, not cryptographic randomness
- [Phase 04-03]: playwright-go v0.5700.1 Screenshot API is variadic PageScreenshotOptions value not pointer — page.Screenshot(opts) not page.Screenshot(&opts)
- [Phase 04-03]: screenshotDir added as final parameter to NewSessionManager — preserves ordering with prior maxOpDelay addition from Plan 02
- [Phase 04-03]: SaveDebugScreenshot has no test file — filesystem side-effect helper; verified via build, vet, and session.go integration
- [Phase 04-04]: VaultProvider.Client() accessor added — needed for vaultHealthFn closure in main.go; type assertion safe because only reached in cfg.VaultAddr != "" branch
- [Phase 04-04]: Dockerfile non-root USER server after playwright install — playwright install --with-deps requires root (apt-get)
- [Phase 04-04]: Test nil breakers safe for unit tests — all unit test paths return before breakers.Execute (validation/auth early exits)
- [Phase 05-01]: Custom registry (prometheus.NewRegistry()) not DefaultRegisterer — avoids test panics from duplicate metric registration; promauto.With(reg) scopes all metrics to isolated registry
- [Phase 05-01]: HTTP duration histogram buckets extended to 30s — DNS scraping takes 2-10s, default buckets miss tail latency
- [Phase 05-01]: SyncOpsTotal (dnshe_sync_operations_total) added as 8th metric — per-operation sync visibility beyond what HTTP middleware captures
- [Phase 05-01]: go mod tidy required after go get — transitive prometheus deps not checksummed by go get alone
- [Phase 05-03]: Audit write occurs after browser op (success or failure both recorded); audit failure is non-fatal (slog.ErrorContext only)
- [Phase 05-03]: error_msg uses any type for nullable mapping: nil for empty string, string value for error — avoids *string indirection
- [Phase 05-03]: Resource format is 'zone:<id>' or 'record:<id>' for programmatic parsing in audit_log
- [Phase 05-04]: Package name is reconcile (not sync) — sync collides with Go stdlib sync package
- [Phase 05-04]: RecordKey uses (Type,Name,Content) base to support multi-value A records for same hostname; SRV adds Priority+Weight+Port for port/weight disambiguation
- [Phase 05-04]: recordsEqual compares TTL, Priority, Weight, Port, Target, Dynamic — Content/Name/Type are in the key, ID intentionally differs between current (server-assigned) and desired (empty)
- [Phase 05-04]: DiffRecords Update slice carries cur.ID into desired record — browser UpdateRecord call requires the existing record ID
- [Phase 05-04]: Apply delete-before-add order avoids transient conflicts; make([]SyncResult, 0, ...) guarantees non-nil empty slice
- [Phase 05-05]: syncHTTPResponse defined in handlers/sync.go — had_errors is HTTP-layer concern, not reconcile package concern
- [Phase 05-05]: HTTP 200 always for sync; had_errors=true in body signals partial failure — avoids 207 Multi-Status complexity
- [Phase 05-05]: dry_run path skips audit.Write — no mutations occurred, nothing to record
- [Phase 05-05]: Each apply closure independently wraps breakers.Execute + WithRetry + WithAccount — mirrors existing handler patterns exactly
- [Phase 05-05]: DELETE /{zoneID} and Route("/{zoneID}", ...) coexist as sibling chi registrations — chi resolves by method without conflict
- [Phase 05-02]: PrometheusMiddleware uses chi.RouteContext RoutePattern() not r.URL.Path — avoids label cardinality explosion from zone/record IDs in path
- [Phase 05-02]: opType string parameter added to WithAccount — fine-grained per-operation labels (list_zones, create_record, etc.) for actionable dashboards
- [Phase 05-02]: QueueDepth Dec on all 3 WithAccount exit paths (acquire/timeout/cancel) — prevents permanent gauge drift
- [Phase 05-02]: ActiveSessions Inc in createBrowserSession after successful login; Dec in closeBrowserContext when ctx!=nil — exact session lifecycle tracking
- [Phase 06]: Admin auth completely separate from REST Bearer JWT — ADMIN_SESSION_KEY is an independent HMAC signing key; rotating JWT_SECRET does not invalidate admin sessions
- [Phase 06]: RegisterAdminRoutes FINAL signature defined in plan 02 — plans 03 and 04 replace stub handler bodies only; main.go updated exactly once
- [Phase 06]: templ _templ.go files committed to repo — go build works without templ CLI at build time (RESEARCH.md Pitfall 4)
- [Phase 06-01]: miekg/dns CNAME struct uses Target field (not Cname) — plan had wrong field name, auto-fixed in implementation
- [Phase 06-01]: Single browser session for zone name + records in ExportZone/ImportZone — avoids double queue acquisition vs two separate WithAccount calls
- [Phase 06-01]: Import is additive-only (plan.Delete = nil) — records absent from zone file are never deleted; full replacement deferred
- [Phase 06]: Admin handlers call DB directly — store package only provides Open(); inline DB queries mirror REST handler pattern
- [Phase 06]: token.RevokeByJTI separates admin JTI-only revocation from user account-scoped RevokeToken — admin has full authority, avoids extra DB join
- [Phase 06]: Lazy token loading via htmx GET /admin/tokens/{accountID} — avoids N+1 queries on page load for multi-account deployments
- [Phase 06]: handleSyncTrigger calls reconcile logic in-process — no HTTP round-trip to /api/v1/zones/{zoneID}/sync; avoids Bearer token management in admin layer
- [Phase 06]: audit.Entry extended with ID+CreatedAt for List() scan — Write() INSERT does not use these fields, so all existing Write() callers are backward-compatible
- [Phase 06-04]: tokenPrefix() helper avoids bare [:8] slice panic — production JTI tokens are always UUIDs but defensive code prevents panic on malformed DB rows
- [Phase 06-04]: ZonesPage shows accounts only (empty zonesByAccount map) — browser sessions per account would be too expensive for a read-only informational page
- [Phase 06-04]: fs.Sub re-roots embed FS at "static/" — without this, FileServer sees "admin.css" but FS root has "static/admin.css", causing 404 for all static assets

### Roadmap Evolution

- Phase 7 added: Fix browser automation CSS selector bugs and admin UI user registration

### Pending Todos

None.

### Blockers/Concerns

- [Phase 1]: HE session expiry timing is undocumented -- start with 30-minute assumption (CONTEXT.md), tune empirically
- RESOLVED [Phase 1]: dns.he.net HTML structure -- verified via Playwright MCP (all selectors in CONTEXT.md)

## Session Continuity

Last session: 2026-03-03
Stopped at: Completed 07-01-PLAN.md — Phase 7 fully complete (CSS selector bugs fixed, UpdateRecord working, admin user registration fixed)
Resume file: None

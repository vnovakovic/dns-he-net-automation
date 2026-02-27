# Project Research Summary

**Project:** dns-he-net-automation
**Domain:** Go REST API wrapping headless browser automation of dns.he.net
**Researched:** 2026-02-26
**Confidence:** MEDIUM-HIGH (stack HIGH, features MEDIUM, architecture HIGH, pitfalls MEDIUM-HIGH)

## Executive Summary

This project builds a REST API service that wraps dns.he.net's web interface via headless browser automation, exposing DNS record management over HTTP for consumption by Ansible, Terraform, and CI/CD pipelines. The fundamental challenge is that dns.he.net has no official API — every operation must be executed by a real browser navigating the web UI. This makes the project significantly more complex than a typical REST API: every "write" is a 2-10 second browser interaction, sessions are single-tenant-per-account, and the entire scraping layer can break silently when Hurricane Electric changes their HTML. The recommended stack is pure-Go (chi router, go-rod browser automation, modernc.org/sqlite, golang-jwt, hashicorp/vault/api, templ+htmx), which produces a single self-contained binary deployable in Docker with no CGo, no Node.js, and no external runtime dependencies beyond Chromium.

The architecture must be designed around one inescapable constraint: strict per-account session serialization. Concurrent browser operations against the same HE account will invalidate each other's sessions, producing silent failures. The correct model is one Chromium context per account (shared Chromium process with incognito contexts), a per-account mutex, and a request queue. This is not a performance optimization — it is a correctness requirement. The browser automation layer should be fully encapsulated in a page-object pattern so that when HE changes their HTML (which they will), only one package requires updates. All HE credentials must be stored in HashiCorp Vault and fetched at runtime; a 5-minute in-memory cache protects against brief Vault outages.

The primary risks are: (1) silent failures from session invalidation if concurrency control is incorrect, (2) Chromium process leaks causing gradual OOM in production, (3) HE UI changes breaking all operations simultaneously with no warning, and (4) rate limiting or IP blocking from HE if request pacing is too aggressive. These risks are mitigated by building the per-account mutex and session health-check infrastructure first, using Rod's Leakless launcher, implementing an abstraction layer for all CSS selectors, and adding configurable inter-operation delays with jitter. The MVP is a working authenticated API for the five most common record types (A, AAAA, CNAME, MX, TXT) with Vault integration and Docker deployment.

## Key Findings

### Recommended Stack

The stack is deliberately minimal: pure Go with no CGo, no framework lock-in, and no frontend build toolchain. Every dependency was verified against pkg.go.dev as of February 2026. The critical dependency is `go-rod/rod` v0.116.2 for browser automation — it is the right choice over chromedp (lower-level) and playwright-go (requires Node.js). The database driver is `modernc.org/sqlite` v1.46.1 (pure-Go SQLite, no CGo), which enables cross-compilation and clean Docker multi-stage builds. The final Docker image uses `chromedp/headless-shell` as the Chromium base, producing a ~150MB image with no extraneous dependencies.

**Core technologies:**
- **Go 1.24 (min 1.23):** Language runtime — slog stdlib available since 1.21, 1.23+ for module features
- **go-chi/chi v5.2.5:** HTTP router — stdlib-compatible, composable middleware, no custom context
- **go-rod/rod v0.116.2:** Browser automation — pure Go CDP client, clean page-object API for form automation
- **modernc.org/sqlite v1.46.1:** Database — CGo-free SQLite, enables cross-compilation and simpler Docker builds
- **golang-jwt/jwt v5.3.1:** Token signing — de facto standard Go JWT library (database-backed opaque tokens)
- **hashicorp/vault/api v1.22.0:** Secrets — official Vault client for KV v2 credential storage
- **a-h/templ v0.3.977 + htmx 2.0.x:** Admin UI — type-safe server-rendered templates, no JS build step
- **log/slog (stdlib):** Logging — structured JSON logging, zero external dependencies
- **pressly/goose v3:** Migrations — embedded SQL migrations for SQLite schema versioning

**What NOT to use:** GORM, Viper, Gin, Fiber, mattn/go-sqlite3 (CGo), playwright-go (Node.js), Redis (SQLite is the cache).

### Expected Features

The feature set is driven by the critical path: you cannot list records without authenticating, you cannot authenticate without a browser session, and you cannot issue API tokens without account management. Every feature in the dependency graph flows from this constraint. See `FEATURES.md` for full dependency graph and Terraform/Ansible compatibility requirements.

**Must have (table stakes):**
- Account management with Vault credential storage — foundational, everything else depends on it
- Browser session pool with health checks — the engine; no DNS operations are possible without it
- JWT/opaque bearer token issuance with admin/viewer roles — security boundary for all API calls
- Zone listing and record listing per zone — read operations required before any mutations
- Record CRUD for A, AAAA, CNAME, MX, TXT — the five most common types cover 90% of use cases
- Per-account request serialization — correctness requirement, not optional
- Health check endpoint (`GET /healthz`) — Docker health probes, monitoring integration
- Structured JSON logging — production observability baseline
- Docker deployment with Chromium — primary deployment target

**Should have (differentiators):**
- Rate limiting per token and globally — protects HE from aggressive automation, prevents abuse
- Automatic retry with exponential backoff and jitter — resilience to transient HE failures
- Audit log — compliance and debugging, token-level operation tracking
- Prometheus metrics endpoint — request counts, latency histograms, browser pool gauges
- Token expiry (time-limited tokens) — reduces blast radius of leaked credentials
- All remaining record types (SRV, CAA, NS, NAPTR, SSHFP, LOC, PTR) — completeness
- Idempotency enforcement (check-before-create) — Terraform and Ansible require idempotent operations
- OpenAPI spec — enables client SDK generation and Terraform provider development

**Power features (Phase 3):**
- Sync/reconcile (desired-state mode) — killer feature for Ansible/GitOps workflows
- BIND zone file import/export using `miekg/dns` — migration and backup
- Embedded management UI (templ + htmx) — admin panel for token and account management
- Batch operations API — async job IDs for bulk record mutations

**Defer to separate project:**
- Terraform provider (separate repo, separate release cycle, registry publishing)
- Ansible collection/module (community-drivable once the API contract is stable)
- TOTP/2FA support (PROJECT.md explicitly excludes; don't enable 2FA on automation accounts)
- Multi-provider DNS abstraction (OctoDNS already exists for this)

**Terraform compatibility from day one:** Stable resource IDs, full CRUD lifecycle, idempotent create (return 200+existing on conflict, not 409), 404 on deleted resources, consistent JSON response schema. Design for this before writing the first handler.

### Architecture Approach

The architecture is a layered system with a hard concurrency boundary at the browser session layer. The HTTP router dispatches to handlers, handlers call the service layer, and the service layer routes every operation through the Account Session Manager, which enforces per-account serialization before touching the browser. This layering is not optional: the session manager is the single most important architectural component. HE page interactions are encapsulated in a page-object layer (`internal/browser/pages/`) so selector changes affect one package only. SQLite holds tokens, account metadata, and audit logs; it is NOT used for DNS record caching (records are always scraped live).

**Major components:**
1. **HTTP Router + Auth Middleware** (`internal/api`) — chi router, JWT validation, rate limiting, request ID injection
2. **API Handlers** (`internal/api/handlers`) — input validation, response formatting, thin glue to service layer
3. **Service Layer** (`internal/service`) — business logic, orchestrates session manager, does not touch browser directly
4. **Account Session Manager** (`internal/browser`) — per-account mutex, session lifecycle (IDLE/EXECUTING/RESTARTING/FAILED), Vault credential fetch on re-login
5. **Browser Pool** (`internal/browser`) — single Chromium process, incognito contexts per account, page lifecycle management
6. **HE Page Objects** (`internal/browser/pages`) — all CSS selectors and form interactions isolated here; one file per HE screen
7. **Vault Client** (`internal/vault`) — KV v2 read/write with 5-minute credential cache, AppRole or token auth
8. **SQLite Store** (`internal/store`) — WAL mode, busy timeout, goose migrations, token and account CRUD
9. **Sync Engine** (`internal/sync`) — desired-state diff, ordered apply (deletes first), partial success handling
10. **Admin UI** (`internal/ui`) — templ templates, htmx for partial updates, token/account management only

### Critical Pitfalls

1. **Concurrent HE session invalidation** — Two browser instances logged into the same HE account simultaneously will invalidate each other's sessions, producing silent failures. Prevention: enforce a per-account `sync.Mutex` on every browser operation. This is an architectural requirement baked in from Phase 1, not a later optimization.

2. **Chromium process leaks causing OOM** — Rod browser sessions that are not explicitly closed on all code paths (including panics, timeouts, context cancellations) accumulate as zombie processes. Prevention: use Rod's `Leakless(true)` launcher, `defer page.Close()` at every page creation point, set a hard 30-second per-operation timeout, implement a watchdog goroutine for orphan detection.

3. **HE UI changes breaking operations silently** — dns.he.net can change their HTML at any time. Prevention: isolate all CSS selectors in the `internal/browser/pages` package, implement page identity assertions before each operation, run scheduled integration tests against a live test account. There is no workaround for this risk — only containment.

4. **HE rate limiting / IP blocking** — Rapid automated requests trigger rate limiting or IP bans. Prevention: configurable minimum delay between browser operations (start at 2-3 seconds), add random jitter (1.5-4 second range), implement circuit breaker that pauses all operations for an account when N consecutive failures are detected.

5. **Vault connectivity loss causing total outage** — If Vault is unreachable or sealed, no account can retrieve credentials, causing a complete service outage. Prevention: 5-minute in-memory credential cache, Vault lease renewal background goroutine, health endpoint distinguishing "unreachable" vs "sealed", degraded mode where existing sessions continue read operations.

## Implications for Roadmap

Research consistently points to the same build order: you cannot build up the stack without the browser session layer working first. The riskiest and most uncertain component is the HE page automation (actual browser scraping), and this should be de-risked in Phase 1 before any API surface is built. The architecture's build order from ARCHITECTURE.md and the feature critical path from FEATURES.md agree.

### Phase 1: Foundation + Browser Core
**Rationale:** Everything depends on browser automation working. This is the highest-risk component (HE UI structure is uncertain, session behavior needs empirical discovery) and must be validated before any API work begins. Build the infrastructure that everything else sits on.
**Delivers:** Working browser session that can log in, list zones, list records, and create/delete a record for one account. Manual test harness proving this works against live dns.he.net.
**Addresses:** Account management (Vault), session manager with per-account mutex, HE page objects (login, zone list, record CRUD for A/AAAA/CNAME/MX/TXT)
**Avoids:** Session invalidation on concurrent access (Pitfall 1), Chromium process leaks (Pitfall 2), navigation timing issues (Pitfall 8), form hidden fields (Pitfall 13)
**Research flag:** NEEDS RESEARCH PHASE — dns.he.net's actual HTML structure, form field names, session behavior, and rate limiting thresholds are not verified. Inspect live site before coding selectors.

### Phase 2: API Layer + Auth + Production Hardening
**Rationale:** Once browser automation is proven, wrap it in a proper HTTP API with auth. Add production-grade resilience before shipping anything.
**Delivers:** Authenticated REST API with token issuance, zone listing, record CRUD (5 common types), rate limiting, retry/backoff, health check, structured logging, Docker image
**Uses:** chi router, golang-jwt, modernc.org/sqlite (WAL+busy_timeout), go-chi/httprate, log/slog
**Implements:** HTTP Router + Auth Middleware, API Handlers, SQLite Store, request queue per account
**Avoids:** SQLite write contention (Pitfall 10), token revocation semantics (Pitfall 6), Chromium in Docker sandbox issues (Pitfall 11), graceful shutdown with in-flight operations (Pitfall 16)
**Research flag:** Standard patterns — skip research phase. chi, JWT, SQLite WAL, Docker + Chromium flags are all well-documented.

### Phase 3: Vault Integration + Observability + Remaining Record Types
**Rationale:** Vault integration should be solid before adding complex features. Observability (metrics, audit log) should come before advanced features so you can see what's happening. Complete record type coverage rounds out the API.
**Delivers:** HashiCorp Vault KV v2 credential storage with caching and lease renewal, Prometheus metrics, audit log, token expiry, all remaining record types (SRV, CAA, NS, NAPTR, SSHFP, LOC, PTR), OpenAPI spec
**Uses:** hashicorp/vault/api v1.22.0, prometheus/client_golang
**Implements:** Vault Client with credential cache, audit log SQLite table, metrics instrumentation
**Avoids:** Vault connectivity loss causing outage (Pitfall 5), Vault path structure migration pain (Pitfall 15)
**Research flag:** Vault KV v2 patterns are well-documented. Skip research phase. Remaining HE record type form structures need empirical verification (minor research during implementation).

### Phase 4: Power Features (Sync + BIND + Admin UI)
**Rationale:** These are the differentiating features that make this tool genuinely useful for GitOps and IaC workflows. They depend on Phase 2-3 CRUD being solid. Sync engine requires full CRUD. BIND import/export requires full record type coverage.
**Delivers:** Sync/reconcile (desired-state) mode, BIND zone file import/export, embedded admin UI (account + token management), batch operations API
**Uses:** miekg/dns for zone file parsing (mandatory — do not write a custom parser), templ + htmx for UI
**Implements:** Sync Engine (diff + ordered apply), BIND parser/generator, Admin UI handlers and templates
**Avoids:** BIND parsing edge cases (Pitfall 7 — use miekg/dns), TXT record escaping bugs (Pitfall 14), multi-account browser resource scaling (Pitfall 12 — on-demand sessions, shared Chromium process)
**Research flag:** Sync engine diffing logic is standard pattern (skip research). BIND zone file format edge cases are handled by miekg/dns (skip research). templ+htmx integration patterns are MEDIUM confidence — may benefit from targeted research.

### Phase Ordering Rationale

- **Browser first, API second:** The browser automation is the highest-risk, most uncertain component. De-risk it before building anything that depends on it. A broken browser layer means a broken API.
- **Auth before features:** API tokens and account isolation must be proven before any DNS mutation endpoint is exposed, even in dev.
- **Vault after basic auth:** Start with environment-variable credentials in Phase 1-2 dev testing, switch to Vault in Phase 3. This keeps Phase 1 friction low while proving the integration properly.
- **Complex features last:** Sync engine and BIND import depend on full record type CRUD. Admin UI is nice-to-have and can be developed in parallel with Phase 4 features.
- **Idempotency is cross-cutting:** Design for it from Phase 2 (check-before-create), because retrofitting idempotency into a working API is painful and Terraform compatibility requires it.

### Research Flags

Phases needing deeper research during planning:
- **Phase 1 (browser scraping):** The actual DNS.he.net HTML structure, form field names, hidden CSRF tokens, session cookie behavior, and rate limiting thresholds are all unknown and must be empirically discovered. Use a test HE account and browser dev tools to map the UI before writing any selectors. This is the single most important research gap.

Phases with standard, well-documented patterns (skip research-phase):
- **Phase 2 (API + Auth):** chi router, JWT opaque tokens, SQLite WAL, Docker Chromium flags — all extensively documented
- **Phase 3 (Vault + Metrics):** Vault KV v2 patterns, Prometheus client_golang — official docs are comprehensive
- **Phase 4 (Sync + BIND):** miekg/dns handles zone file parsing; sync diffing is standard set-difference algorithm

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All core dependencies verified on pkg.go.dev with exact versions and publish dates as of Feb 2026. caarlos0/env version not individually verified (MEDIUM) but library is well-established. |
| Features | MEDIUM | Feature set derived from comparable DNS APIs (Cloudflare, DigitalOcean, Hetzner) and Terraform provider patterns. dns.he.net-specific form capabilities (which record types are supported, what fields exist) not verified against live site. |
| Architecture | HIGH | Session manager pattern and per-account mutex are architectural certainties given HE's session model. Rod-specific patterns based on library API analysis and Go concurrency best practices. templ+htmx integration is MEDIUM (newer library, patterns still evolving). |
| Pitfalls | MEDIUM-HIGH | Chromium process management, SQLite WAL, Vault operational pitfalls, and Docker Chromium sandbox issues are HIGH confidence (extensively documented). dns.he.net-specific behaviors (session expiry timing, rate limits, HTML structure) are MEDIUM confidence — need empirical discovery. |

**Overall confidence:** MEDIUM-HIGH

The stack and architecture are solid. The uncertainty is in the dns.he.net integration layer specifically — the site's internal structure, session behavior, and rate limits are not documented and must be discovered during Phase 1 implementation. Design the abstraction layer to isolate this uncertainty.

### Gaps to Address

- **dns.he.net HTML structure:** Before writing any page object code, manually inspect the live site's login form, zone list, and record edit forms. Map: form field names, hidden fields, CSS selectors for key elements, URL structure for zone and record pages. This single gap drives the most Phase 1 risk.
- **HE session expiry timing:** The duration of an HE session cookie is undocumented. Empirically discover this to set the proactive re-login threshold correctly. Start conservative (15 minutes) and tune.
- **HE rate limiting thresholds:** No documented limits. Start with 2-3 second inter-operation delays and tune downward carefully based on empirical testing.
- **HE record type form fields:** Each DNS record type (SRV, CAA, NAPTR, etc.) has different form fields on dns.he.net. Map each type's form structure during Phase 3 implementation.
- **templ build integration:** `templ generate` must run before `go build`. The Makefile and CI pipeline need to account for this code generation step. Minor but easy to forget.

## Sources

### Primary (HIGH confidence)
- pkg.go.dev — go-rod/rod v0.116.2 (Jul 2024), go-chi/chi v5.2.5 (Feb 5 2026), golang-jwt/jwt v5.3.1 (Jan 28 2026), modernc.org/sqlite v1.46.1 (Feb 18 2026, SQLite 3.51.2), hashicorp/vault/api v1.22.0 (Oct 2025), a-h/templ v0.3.977 (Dec 31 2025)
- Go stdlib documentation — log/slog (Go 1.21+), database/sql, embed, net/http
- Rod library documentation (go-rod.github.io) — browser automation patterns, concurrency model
- SQLite WAL mode documentation — concurrency characteristics, WAL vs journal mode
- HashiCorp Vault documentation — KV v2 API, AppRole auth, lease management
- Docker + Chromium guides (Puppeteer, Playwright, Rod) — sandbox flags, shm requirements

### Secondary (MEDIUM confidence)
- Comparable DNS APIs (Cloudflare, DigitalOcean, Hetzner, Route53) — feature expectations and REST design patterns
- Terraform provider SDK documentation — resource lifecycle requirements, ID stability, idempotency contract
- Ansible module development patterns — check mode, diff mode, idempotency requirements
- miekg/dns library — zone file parsing capabilities, used by CoreDNS and external-dns

### Tertiary (LOW confidence)
- dns.he.net web interface structure — inferred from training data, must be validated empirically before Phase 1 implementation
- HE session behavior and rate limits — undocumented, requires empirical discovery on live site

---
*Research completed: 2026-02-26*
*Ready for roadmap: yes*

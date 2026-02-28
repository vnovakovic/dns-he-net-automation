# Roadmap: dns-he-net-automation

## Overview

This roadmap delivers a Go REST API that wraps dns.he.net's web interface via headless browser automation, exposing DNS record management over HTTP for Ansible, Terraform, and CI/CD pipelines. The build order is dictated by a hard architectural constraint: everything depends on the browser automation layer working first. Phase 1 de-risks the highest-uncertainty component (HE page scraping) before any API surface is built. Phases 2-3 deliver a usable authenticated API with full DNS CRUD. Phases 4-5 harden for production (Vault, resilience, observability, sync). Phase 6 adds power features (BIND import/export, admin UI). Each phase delivers a coherent, independently verifiable capability.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [x] **Phase 1: Foundation + Browser Core** - Project scaffolding, Chromium lifecycle, HE login/session management, page-object layer, per-account mutex, SQLite schema
- [x] **Phase 2: API Layer + Authentication** - chi router, bearer token system, auth middleware, account/token management endpoints, health check, structured logging (completed 2026-02-28)
- [x] **Phase 3: DNS Operations** - Zone CRUD, record CRUD for all v1 types (A, AAAA, CNAME, MX, TXT, SRV, CAA, NS), idempotency, field validation (completed 2026-02-28)
- [x] **Phase 4: Production Hardening** - Vault credential storage, retry/backoff/circuit breaker, rate limiting, debug screenshots, Docker image, binary builds (completed 2026-02-28)
- [ ] **Phase 5: Observability + Sync Engine** - Prometheus metrics, audit log, sync/reconcile with dry-run support
- [ ] **Phase 6: BIND Import/Export + Admin UI** - BIND zone file import/export via miekg/dns, embedded templ+htmx admin panel

## Phase Details

### Phase 1: Foundation + Browser Core
**Goal**: A working browser automation engine that can log into dns.he.net, maintain sessions per account, and survive Chromium crashes -- verified against the live site with a test account
**Depends on**: Nothing (first phase)
**Requirements**: BROWSER-01, BROWSER-02, BROWSER-03, BROWSER-04, BROWSER-05, BROWSER-06, BROWSER-07, OPS-03, OPS-06, REL-01, REL-02, REL-03, SEC-03
**Success Criteria** (what must be TRUE):
  1. Service launches a headless Chromium instance via playwright-go, and no orphaned Chromium processes remain after shutdown
  2. Service can log into a dns.he.net account (credentials from env vars via a credential interface), navigate to the zone list page, and return zone data
  3. Two concurrent requests for the same account are serialized (second waits for first to complete) -- never two simultaneous browser operations per account
  4. A stale or crashed browser session is detected and automatically restarted with a fresh login before the next operation
  5. All CSS selectors and form interactions live in `internal/browser/pages/` -- no selectors exist in any other package
**Plans**: 3 plans

Plans:
- [x] 01-01-PLAN.md -- Go module, config, domain types, SQLite store with goose migrations, Dockerfile
- [x] 01-02-PLAN.md -- Credential provider interface, playwright-go launcher, session manager with per-account mutex
- [x] 01-03-PLAN.md -- HE page objects (login, zone list, record form for all 17 types), session health/recovery, live verification

### Phase 2: API Layer + Authentication
**Goal**: External clients can authenticate with bearer tokens and manage accounts/tokens via a REST API, with structured logging and graceful shutdown
**Depends on**: Phase 1
**Requirements**: TOKEN-01, TOKEN-02, TOKEN-03, TOKEN-04, TOKEN-05, TOKEN-06, TOKEN-07, ACCT-01, ACCT-02, ACCT-03, ACCT-04, API-01, API-02, API-03, API-04, API-07, OPS-01, OPS-02, OPS-04, SEC-01, SEC-02, SEC-04
**Success Criteria** (what must be TRUE):
  1. An operator can register a dns.he.net account, issue a bearer token scoped to that account with admin or viewer role, and use that token to authenticate API requests
  2. A viewer-role token is rejected when attempting write operations (POST/PUT/DELETE) -- only GET succeeds
  3. A token scoped to account A cannot access account B's resources under any circumstance
  4. Revoked or expired tokens are immediately rejected on the next request
  5. `GET /healthz` returns service status including browser pool and database connectivity, and the service shuts down gracefully on SIGTERM (drains in-flight operations, closes browsers, closes SQLite)
**Plans**: TBD

Plans:
- [x] 02-01: Bearer token system (issuance, hashing, validation, revocation) and SQLite token store
- [x] 02-02: chi router, auth middleware, account management endpoints, token management endpoints, bootstrap CLI
- [x] 02-03: Health endpoint, structured logging, graceful shutdown, error response contract

### Phase 3: DNS Operations
**Goal**: External clients can perform full DNS record and zone CRUD via the REST API, with idempotent operations suitable for Terraform and Ansible
**Depends on**: Phase 2
**Requirements**: ZONE-01, ZONE-02, ZONE-03, ZONE-04, REC-01, REC-02, REC-03, REC-04, REC-05, REC-06, REC-07, REC-08, REC-09, API-05, API-06, PERF-01, PERF-02, PERF-03, COMPAT-01, COMPAT-02, COMPAT-03
**Success Criteria** (what must be TRUE):
  1. `GET /api/v1/zones` returns all zones for the authenticated account with stable zone IDs, and `POST`/`DELETE` can add/remove zones on dns.he.net
  2. Full record CRUD works for all v1 types (A, AAAA, CNAME, MX, TXT, SRV, CAA, NS) with correct type-specific field validation (MX priority, SRV priority/weight/port, etc.)
  3. Record creation is idempotent (existing match by type+name+value returns 200, not 409) and record deletion is idempotent (already-deleted returns 204, not 404)
  4. Every record and zone response includes stable IDs, full field state, and consistent JSON schema suitable for Terraform state tracking
  5. API response time for read operations is under 10 seconds and for single write operations under 15 seconds including browser automation time
**Plans**: 3 plans

Plans:
- [x] 03-01-PLAN.md -- Zone page objects (AddZone, DeleteZone, GetZoneName) and zone API handlers (ListZones, CreateZone, DeleteZone)
- [x] 03-02-PLAN.md -- Record page objects (ParseRecordRow, ListRecords, FindRecord) and record API handlers (List, Get, Create, Update, Delete) with idempotency
- [x] 03-03-PLAN.md -- Field validation (internal/api/validate), ?type/?name query filtering, WriteJSON helper, Makefile build-linux cross-compilation

### Phase 4: Production Hardening
**Goal**: The service is production-ready with Vault credential storage, resilience against transient failures, rate limiting, and Docker deployment
**Depends on**: Phase 3
**Requirements**: VAULT-01, VAULT-02, VAULT-03, VAULT-04, VAULT-05, VAULT-06, BROWSER-08, BROWSER-09, RES-01, RES-02, RES-03, OBS-03, OPS-05
**Success Criteria** (what must be TRUE):
  1. dns.he.net credentials are stored in Vault KV v2 and fetched lazily at runtime with a 5-minute in-memory cache -- credentials never appear in SQLite, logs, or API responses
  2. If Vault is temporarily unreachable, existing cached credentials and active browser sessions continue to function (degraded mode)
  3. Transient browser failures (timeout, session expiry) are retried with exponential backoff (max 3 attempts), and a circuit breaker pauses an account after N consecutive failures
  4. Per-token and global rate limiting returns 429 with `Retry-After` header, and configurable inter-operation delay with jitter prevents HE rate limiting
  5. The service ships as a Docker image (ubuntu:noble + playwright chromium) and as standalone static binaries (amd64 + arm64), with failed browser operations producing debug screenshots
**Plans**: 4 plans

Plans:
- [ ] 04-01-PLAN.md -- VaultProvider (KV v2, TTL cache, stale fallback, token + AppRole auth) + Config extensions
- [ ] 04-02-PLAN.md -- Resilience layer (retry/backoff wrapper, per-account circuit breaker, rate limiting middleware, inter-op jitter)
- [ ] 04-03-PLAN.md -- Debug screenshots on failure (OBS-03) + fatal crash recovery path (BROWSER-09)
- [ ] 04-04-PLAN.md -- Wiring (credential provider selection, resilience in handlers, health Vault check, Docker polish, Makefile)

### Phase 5: Observability + Sync Engine
**Goal**: Operators have full visibility into service behavior via metrics and audit logs, and external systems can declare desired DNS state and have the service reconcile it
**Depends on**: Phase 4
**Requirements**: OBS-01, OBS-02, SYNC-01, SYNC-02, SYNC-03, SYNC-04, SYNC-05, SYNC-06
**Success Criteria** (what must be TRUE):
  1. `GET /metrics` exposes Prometheus-format metrics including request count/duration, browser operation count/duration, active sessions gauge, queue depth per account, and error counts
  2. An audit log in SQLite records every mutation (token_id, account_id, action, resource, result) and is queryable for post-mortem analysis
  3. `POST /api/v1/zones/{zone_id}/sync` accepts a desired-state record set, computes a diff (adds/updates/deletes), and applies changes in safe order (deletes first, then updates, then adds)
  4. Sync is idempotent (running twice with the same desired state produces no changes on the second run) and supports `dry_run=true` to preview the diff without applying
  5. Sync handles partial failure: if one operation fails, remaining operations still execute, and the response reports per-operation results
**Plans**: 5 plans

Plans:
- [ ] 05-01-PLAN.md -- Prometheus metrics registry package (custom registry, all metric vars, Handler())
- [ ] 05-02-PLAN.md -- HTTP + browser instrumentation (PrometheusMiddleware, SessionManager metrics, /metrics route, main.go wiring)
- [ ] 05-03-PLAN.md -- Audit log (003_audit_log.sql migration, audit package, Write() calls in all mutating handlers)
- [ ] 05-04-PLAN.md -- Sync diff algorithm TDD (reconcile package: DiffRecords, Apply, SyncPlan, SyncResult)
- [ ] 05-05-PLAN.md -- Sync HTTP handler and router registration (POST /sync, dry-run, partial success, audit, metrics)

### Phase 6: BIND Import/Export + Admin UI
**Goal**: Operators can import/export zones in standard BIND format for migration and backup, and manage accounts and tokens through an embedded web UI without curl
**Depends on**: Phase 5
**Requirements**: BIND-01, BIND-02, BIND-03, UI-01, UI-02, UI-03, UI-04, UI-05
**Success Criteria** (what must be TRUE):
  1. `GET /api/v1/zones/{zone_id}/export` returns the zone in standard BIND zone file format (RFC 1035 compatible), generated using `miekg/dns`
  2. `POST /api/v1/zones/{zone_id}/import` accepts a BIND zone file body, parses it, and uses the sync engine internally to diff-and-apply (existing matching records are left untouched)
  3. An embedded web UI at `/admin` allows listing/registering/removing accounts and issuing/listing/revoking tokens, protected by admin-level authentication
  4. The admin UI is built with `templ` + `htmx`, embedded in the Go binary with no separate frontend build step, and is optional for operation (all functionality available via REST API)
**Plans**: TBD

Plans:
- [ ] 06-01: BIND export and import using miekg/dns with sync engine integration
- [ ] 06-02: Admin UI (templ templates, htmx interactions, account/token management pages)

## Progress

**Execution Order:**
Phases execute in numeric order: 1 -> 2 -> 3 -> 4 -> 5 -> 6

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Foundation + Browser Core | 3/3 | Complete | 2026-02-27 |
| 2. API Layer + Authentication | 3/TBD | Complete    | 2026-02-28 |
| 3. DNS Operations | 3/3 | Complete    | 2026-02-28 |
| 4. Production Hardening | 4/4 | Complete   | 2026-02-28 |
| 5. Observability + Sync Engine | 0/5 | Not started | - |
| 6. BIND Import/Export + Admin UI | 0/2 | Not started | - |

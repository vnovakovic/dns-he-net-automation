# Requirements: dns-he-net-automation

**Defined:** 2026-02-27
**Core Value:** External systems can manage DNS records on dns.he.net via a REST API as if it were a first-class DNS provider, without any manual web interaction.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Browser Automation Core

- [x] **BROWSER-01**: Service can launch a headless Chromium instance via Rod with Leakless mode enabled, preventing orphaned processes
- [ ] **BROWSER-02**: Service can log into a dns.he.net account using credentials fetched from Vault at runtime
- [x] **BROWSER-03**: Browser sessions are isolated per dns.he.net account (separate incognito contexts or separate processes, no cookie cross-contamination)
- [x] **BROWSER-04**: All browser operations for a given account are serialized via a per-account mutex — concurrent requests queue rather than race
- [ ] **BROWSER-05**: Browser sessions detect stale/expired state and automatically re-authenticate before the next operation
- [x] **BROWSER-06**: Every browser operation has a configurable timeout (default 30s) — a hung page does not block the account queue indefinitely
- [ ] **BROWSER-07**: All CSS selectors and form interactions are encapsulated in page-object files under `internal/browser/pages/` — no selectors leak into handlers or service code
- [ ] **BROWSER-08**: Configurable inter-operation delay with jitter (default 2-3s range) to avoid triggering dns.he.net rate limiting or bot detection
- [ ] **BROWSER-09**: On fatal browser error (crash, unrecoverable state), the session is automatically restarted with a fresh Chromium context and re-login

### Vault Integration

- [ ] **VAULT-01**: dns.he.net account credentials (username + password) are stored in HashiCorp Vault KV v2 at a configurable mount path (default `secret/data/dns-he-net/{account-id}`)
- [ ] **VAULT-02**: Credentials are fetched lazily on first request to an account, not pre-fetched at startup
- [ ] **VAULT-03**: Fetched credentials are cached in-memory with a configurable TTL (default 5 minutes) to reduce Vault load
- [ ] **VAULT-04**: Service verifies Vault connectivity at startup and reports status via the health endpoint
- [ ] **VAULT-05**: If Vault is temporarily unreachable, existing cached credentials and active browser sessions continue to function (degraded mode)
- [ ] **VAULT-06**: Vault authentication supports both token auth and AppRole auth, selectable via configuration

### Account Management

- [x] **ACCT-01**: Operator can register a dns.he.net account in the system by providing an account name and Vault path — credentials are stored in Vault, only metadata in SQLite
- [x] **ACCT-02**: Operator can list all registered accounts (returns metadata only, never credentials)
- [x] **ACCT-03**: Operator can remove an account from the system, which closes its browser session and deletes its metadata from SQLite
- [x] **ACCT-04**: Account isolation is enforced: a token scoped to account A cannot see or modify account B's zones or records under any circumstance

### Token Management (Authentication)

- [x] **TOKEN-01**: Operator can issue a bearer token scoped to a specific account with a role of `admin` or `viewer`
- [x] **TOKEN-02**: Tokens are cryptographically random (32 bytes), displayed once at creation, and stored in SQLite as a SHA-256 hash — the plaintext is never persisted
- [x] **TOKEN-03**: Multiple tokens can be issued per account, each with an optional human-readable label
- [x] **TOKEN-04**: Tokens can have an optional expiry date (`expires_at`); expired tokens are rejected by the auth middleware
- [x] **TOKEN-05**: Tokens can be revoked by ID; revoked tokens are immediately rejected on the next request
- [x] **TOKEN-06**: Operator can list all tokens for an account (showing label, role, created/expires/revoked dates, but never the token value)
- [x] **TOKEN-07**: `viewer` role tokens can only perform read operations (GET); `admin` role tokens can perform all operations (GET, POST, PUT, DELETE)

### Zone Operations

- [ ] **ZONE-01**: `GET /api/v1/zones` returns the list of all zones managed by the authenticated account, scraped live from dns.he.net
- [ ] **ZONE-02**: Each zone in the response includes a stable zone identifier (as used by dns.he.net internally) and the domain name
- [ ] **ZONE-03**: `POST /api/v1/zones` adds a new zone (domain) to the account on dns.he.net
- [ ] **ZONE-04**: `DELETE /api/v1/zones/{zone_id}` removes a zone from the account on dns.he.net

### DNS Record CRUD

- [ ] **REC-01**: `GET /api/v1/zones/{zone_id}/records` returns all DNS records for a zone, scraped live from dns.he.net (no local cache)
- [ ] **REC-02**: Each record in the response includes the dns.he.net internal record ID, record type, name, value, TTL, and type-specific fields (priority for MX/SRV, weight/port for SRV)
- [ ] **REC-03**: `POST /api/v1/zones/{zone_id}/records` creates a new DNS record. Supported types in v1: A, AAAA, CNAME, MX, TXT, SRV, CAA, NS
- [ ] **REC-04**: `PUT /api/v1/zones/{zone_id}/records/{record_id}` updates an existing DNS record (modifies value, TTL, or type-specific fields)
- [ ] **REC-05**: `DELETE /api/v1/zones/{zone_id}/records/{record_id}` deletes a DNS record
- [ ] **REC-06**: `GET /api/v1/zones/{zone_id}/records/{record_id}` returns a single record by its ID
- [ ] **REC-07**: Record creation is idempotent: creating a record that already exists (matched by type + name + value) returns 200 with the existing record, not 409
- [ ] **REC-08**: Record deletion is idempotent: deleting an already-deleted record returns 204, not 404
- [ ] **REC-09**: All record types enforce correct field validation (e.g., MX requires priority, SRV requires priority + weight + port, TXT value is properly escaped)

### Sync and Reconcile

- [ ] **SYNC-01**: `POST /api/v1/zones/{zone_id}/sync` accepts a desired-state record set and computes a diff against the current live state
- [ ] **SYNC-02**: The diff produces three sets: records to add, records to update, records to delete
- [ ] **SYNC-03**: Changes are applied in safe order: deletes first, then updates, then adds — to avoid transient conflicts
- [ ] **SYNC-04**: Sync supports partial success: if one operation fails, remaining operations still execute, and the response reports per-operation results
- [ ] **SYNC-05**: Sync is idempotent: running sync twice with the same desired state produces no changes on the second run
- [ ] **SYNC-06**: Sync supports a `dry_run=true` query parameter that returns the diff/plan without applying any changes

### BIND Zone File Import/Export

- [ ] **BIND-01**: `GET /api/v1/zones/{zone_id}/export` returns the zone's records in standard BIND zone file format (RFC 1035 compatible), generated using `miekg/dns`
- [ ] **BIND-02**: `POST /api/v1/zones/{zone_id}/import` accepts a BIND zone file body, parses it using `miekg/dns`, and creates/updates records to match the file contents
- [ ] **BIND-03**: BIND import uses the sync engine internally (diff + apply), not blind create — existing matching records are left untouched

### Admin UI

- [ ] **UI-01**: An embedded web UI is served from the Go binary at `/admin`, built with `templ` templates and `htmx` for dynamic updates
- [ ] **UI-02**: The admin UI allows listing, registering, and removing dns.he.net accounts
- [ ] **UI-03**: The admin UI allows issuing, listing, and revoking bearer tokens per account
- [ ] **UI-04**: The admin UI is protected by admin-level authentication (same bearer token system or a bootstrap admin token)
- [ ] **UI-05**: The admin UI is optional for operation — all functionality is available via REST API; the UI is a convenience layer

### API Design Contract

- [x] **API-01**: All API endpoints are prefixed with `/api/v1/`
- [x] **API-02**: All request and response bodies are JSON with `Content-Type: application/json`
- [x] **API-03**: Proper HTTP status codes are returned: 200 OK, 201 Created, 204 No Content, 400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found, 409 Conflict, 429 Too Many Requests, 500 Internal Server Error
- [x] **API-04**: Error responses follow a consistent schema: `{"error": "<message>", "code": "<machine_readable_code>"}` with actionable messages (e.g., "Record A foo.example.com already exists", not "Internal Server Error")
- [ ] **API-05**: Every API response for a record or zone includes stable resource IDs suitable for Terraform state tracking
- [ ] **API-06**: `GET` endpoints for records support query parameter filtering by record type and/or name
- [x] **API-07**: All authenticated endpoints require `Authorization: Bearer <token>` header

### Operational

- [ ] **OPS-01**: `GET /healthz` returns 200 with a JSON body reporting: service status, browser pool status (sessions active/idle), Vault connectivity status, SQLite connectivity
- [ ] **OPS-02**: All log output uses Go `log/slog` in structured JSON format with request ID, account ID, operation type, and duration fields
- [x] **OPS-03**: Configuration is loaded from environment variables with an optional YAML/TOML config file; env vars take precedence (12-factor)
- [ ] **OPS-04**: Service handles SIGTERM/SIGINT gracefully: stops accepting new requests, drains in-flight browser operations (with timeout), closes all browser sessions, closes SQLite, then exits
- [ ] **OPS-05**: Service ships as a single static Go binary and as a Docker image based on `chromedp/headless-shell` (~150MB)
- [x] **OPS-06**: SQLite database schema is managed by embedded SQL migrations via `pressly/goose` v3, run automatically at startup

### Observability

- [ ] **OBS-01**: `GET /metrics` exposes Prometheus-format metrics including: request count/duration by endpoint, browser operation count/duration by type, active browser sessions gauge, request queue depth per account, error counts by type
- [ ] **OBS-02**: An audit log table in SQLite records: timestamp, token_id, account_id, action (create/update/delete/sync), resource identifier, result (success/failure)
- [ ] **OBS-03**: Browser operations that fail produce a debug screenshot saved to a configurable directory for post-mortem analysis

### Resilience

- [ ] **RES-01**: Transient browser operation failures (timeout, network error, session expiry) are retried with exponential backoff and jitter (max 3 attempts)
- [ ] **RES-02**: Per-token and global rate limiting returns 429 with `Retry-After` header when thresholds are exceeded
- [ ] **RES-03**: A circuit breaker pauses all operations for an account after N consecutive failures (configurable, default 5), with automatic recovery after a backoff period

## Non-Functional Requirements

### Performance

- [ ] **PERF-01**: API response time for read operations (zone list, record list) is under 10 seconds including browser scraping time
- [ ] **PERF-02**: API response time for single write operations (create/update/delete record) is under 15 seconds including browser automation
- [ ] **PERF-03**: Requests queued behind the per-account mutex receive a response (success or timeout) within 60 seconds

### Security

- [x] **SEC-01**: dns.he.net credentials never appear in SQLite, logs, API responses, or error messages — they exist only in Vault and transiently in memory
- [x] **SEC-02**: Bearer tokens are stored as SHA-256 hashes in SQLite; the plaintext token is returned only at creation time
- [x] **SEC-03**: The SQLite database file permissions are set to 0600 (owner read/write only)
- [x] **SEC-04**: All API input is validated and sanitized before use in browser form fields to prevent injection into dns.he.net forms

### Reliability

- [x] **REL-01**: SQLite uses WAL journal mode with `busy_timeout=5000` and `foreign_keys=ON` for concurrent read safety and data integrity
- [x] **REL-02**: Chromium processes are launched with `Leakless(true)` and a watchdog goroutine detects and kills orphaned processes
- [x] **REL-03**: The service can be restarted cleanly and resume operation — no persistent browser state is required across restarts

### Compatibility

- [ ] **COMPAT-01**: All record and zone responses include stable IDs, full field state, and consistent JSON schemas to support a future Terraform provider without API changes
- [ ] **COMPAT-02**: Record create is idempotent (returns existing on conflict) and delete is idempotent (204 on missing) to support Terraform and Ansible retry semantics
- [ ] **COMPAT-03**: The Go binary compiles on Linux amd64 and arm64; the Docker image targets Linux amd64

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Extended Record Types

- **V2-REC-01**: Support for NAPTR, SSHFP, LOC, and PTR record types (requires mapping each type's dns.he.net form fields)

### Batch Operations

- **V2-BATCH-01**: `POST /api/v1/zones/{zone_id}/batch` accepts an array of record operations and returns an async job ID
- **V2-BATCH-02**: `GET /api/v1/jobs/{job_id}` returns the status and results of a batch job

### OpenAPI Specification

- **V2-SPEC-01**: A machine-readable OpenAPI 3.0 spec is generated from or maintained alongside the API handlers
- **V2-SPEC-02**: The spec is served at `/api/v1/openapi.json` for client SDK generation

### Advanced Vault Features

- **V2-VAULT-01**: Background Vault lease renewal goroutine for long-running token auth
- **V2-VAULT-02**: Vault namespace support for enterprise deployments

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| TOTP/2FA for dns.he.net accounts | Massively increases browser automation complexity (screenshot TOTP, parse QR). Automation accounts should not have 2FA enabled. |
| Terraform provider | Separate project with its own repo, release cycle, and registry publishing. The API is designed to support it, but the provider itself is out of scope. |
| Ansible collection/module | Separate project, community-drivable once the API contract is stable. |
| OAuth2/OIDC for API clients | Overkill for server-to-server API. Bearer tokens are the industry standard for infrastructure APIs. |
| Real-time DNS propagation checking | Different concern (DNS monitoring). Requires querying resolvers worldwide. Use external tools. |
| Multi-provider DNS abstraction | This is an HE-specific wrapper, not a universal DNS platform. OctoDNS exists for multi-provider. |
| DNSSEC management | HE handles DNSSEC signing server-side. Exposing it via API adds complexity with minimal value. |
| Web UI for DNS record editing | The embedded UI is for token/account management only. DNS record editing duplicates HE's own interface. |
| Scheduled DNS changes | Cron-like scheduled mutations add state machine complexity. CI/CD pipelines handle scheduling. |
| DNS record templates/presets | Feature creep. Logic belongs in the IaC tool (Ansible playbook, Terraform config), not the API. |
| Email notifications | Not needed for an infrastructure API. Prometheus + Alertmanager handles alerting. |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| BROWSER-01 | Phase 1 | Complete (01-02) |
| BROWSER-02 | Phase 1 | Pending |
| BROWSER-03 | Phase 1 | Complete (01-02) |
| BROWSER-04 | Phase 1 | Complete (01-02) |
| BROWSER-05 | Phase 1 | Pending |
| BROWSER-06 | Phase 1 | Complete (01-02) |
| BROWSER-07 | Phase 1 | Pending |
| BROWSER-08 | Phase 4 | Pending |
| BROWSER-09 | Phase 4 | Pending |
| VAULT-01 | Phase 4 | Pending |
| VAULT-02 | Phase 4 | Pending |
| VAULT-03 | Phase 4 | Pending |
| VAULT-04 | Phase 4 | Pending |
| VAULT-05 | Phase 4 | Pending |
| VAULT-06 | Phase 4 | Pending |
| ACCT-01 | Phase 2 | Complete |
| ACCT-02 | Phase 2 | Complete |
| ACCT-03 | Phase 2 | Complete |
| ACCT-04 | Phase 2 | Complete |
| TOKEN-01 | Phase 2 | Complete |
| TOKEN-02 | Phase 2 | Complete |
| TOKEN-03 | Phase 2 | Complete |
| TOKEN-04 | Phase 2 | Complete |
| TOKEN-05 | Phase 2 | Complete |
| TOKEN-06 | Phase 2 | Complete |
| TOKEN-07 | Phase 2 | Complete |
| ZONE-01 | Phase 3 | Pending |
| ZONE-02 | Phase 3 | Pending |
| ZONE-03 | Phase 3 | Pending |
| ZONE-04 | Phase 3 | Pending |
| REC-01 | Phase 3 | Pending |
| REC-02 | Phase 3 | Pending |
| REC-03 | Phase 3 | Pending |
| REC-04 | Phase 3 | Pending |
| REC-05 | Phase 3 | Pending |
| REC-06 | Phase 3 | Pending |
| REC-07 | Phase 3 | Pending |
| REC-08 | Phase 3 | Pending |
| REC-09 | Phase 3 | Pending |
| SYNC-01 | Phase 5 | Pending |
| SYNC-02 | Phase 5 | Pending |
| SYNC-03 | Phase 5 | Pending |
| SYNC-04 | Phase 5 | Pending |
| SYNC-05 | Phase 5 | Pending |
| SYNC-06 | Phase 5 | Pending |
| BIND-01 | Phase 6 | Pending |
| BIND-02 | Phase 6 | Pending |
| BIND-03 | Phase 6 | Pending |
| UI-01 | Phase 6 | Pending |
| UI-02 | Phase 6 | Pending |
| UI-03 | Phase 6 | Pending |
| UI-04 | Phase 6 | Pending |
| UI-05 | Phase 6 | Pending |
| API-01 | Phase 2 | Complete |
| API-02 | Phase 2 | Complete |
| API-03 | Phase 2 | Complete |
| API-04 | Phase 2 | Complete |
| API-05 | Phase 3 | Pending |
| API-06 | Phase 3 | Pending |
| API-07 | Phase 2 | Complete |
| OPS-01 | Phase 2 | Pending |
| OPS-02 | Phase 2 | Pending |
| OPS-03 | Phase 1 | Complete (01-01) |
| OPS-04 | Phase 2 | Pending |
| OPS-05 | Phase 4 | Pending |
| OPS-06 | Phase 1 | Complete (01-01) |
| OBS-01 | Phase 5 | Pending |
| OBS-02 | Phase 5 | Pending |
| OBS-03 | Phase 4 | Pending |
| RES-01 | Phase 4 | Pending |
| RES-02 | Phase 4 | Pending |
| RES-03 | Phase 4 | Pending |
| PERF-01 | Phase 3 | Pending |
| PERF-02 | Phase 3 | Pending |
| PERF-03 | Phase 3 | Pending |
| SEC-01 | Phase 2 | Complete |
| SEC-02 | Phase 2 | Complete |
| SEC-03 | Phase 1 | Complete (01-01) |
| SEC-04 | Phase 2 | Complete |
| REL-01 | Phase 1 | Complete (01-01) |
| REL-02 | Phase 1 | Complete (01-02) |
| REL-03 | Phase 1 | Complete (01-02) |
| COMPAT-01 | Phase 3 | Pending |
| COMPAT-02 | Phase 3 | Pending |
| COMPAT-03 | Phase 3 | Pending |

**Coverage:**
- v1 requirements: 85 total (corrected from initial estimate of 76)
- Mapped to phases: 85
- Unmapped: 0

**Per-phase breakdown:**
- Phase 1: 13 requirements (BROWSER-01..07, OPS-03, OPS-06, REL-01..03, SEC-03)
- Phase 2: 22 requirements (TOKEN-01..07, ACCT-01..04, API-01..04/07, OPS-01/02/04, SEC-01/02/04)
- Phase 3: 21 requirements (ZONE-01..04, REC-01..09, API-05/06, PERF-01..03, COMPAT-01..03)
- Phase 4: 13 requirements (VAULT-01..06, BROWSER-08/09, RES-01..03, OBS-03, OPS-05)
- Phase 5: 8 requirements (OBS-01/02, SYNC-01..06)
- Phase 6: 8 requirements (BIND-01..03, UI-01..05)

---
*Requirements defined: 2026-02-27*
*Last updated: 2026-02-27 after plan 01-01 completion (OPS-03, OPS-06, REL-01, SEC-03 completed)*

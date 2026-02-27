# Feature Landscape

**Domain:** DNS Management REST API (dns.he.net headless browser wrapper)
**Researched:** 2026-02-26
**Confidence:** MEDIUM (based on training data knowledge of DNS APIs, Terraform provider patterns, and REST API design; no live source verification available)

## Table Stakes

Features users expect. Missing = product feels incomplete or integration breaks.

### DNS Record Operations

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| CRUD for all standard record types (A, AAAA, CNAME, MX, TXT, NS, SRV, CAA, SSHFP, LOC, NAPTR, PTR) | Any DNS API must support the full record type spectrum that the provider supports. Ansible/Terraform modules enumerate record types. | Medium | dns.he.net supports an unusually broad set of record types. Map each type to its HE form fields. Priority: A, AAAA, CNAME, MX, TXT first. |
| Zone listing (GET /zones) | Users need to enumerate what zones an account manages before operating on records. Every DNS API has this. | Low | Scrape the zone list page. |
| Zone record listing (GET /zones/{zone}/records) | Fundamental for reconciliation, Terraform state refresh, and Ansible facts gathering. | Low | Scrape the zone edit page for the record table. |
| Idempotent record operations | Terraform and Ansible both require idempotency. Creating a record that already exists must not error or duplicate. | Medium | Requires matching on (name, type, content) tuple before inserting. HE may not expose record IDs consistently -- need a stable identifier strategy. |
| Record identification by ID | Terraform manages resources by ID. Without stable IDs, `terraform plan` cannot track drift. | Medium | dns.he.net uses internal record IDs in form fields. Scrape these and expose as the canonical record identifier. Critical for Terraform provider. |
| TTL support | Every DNS record has a TTL. APIs that omit TTL control are unusable for production DNS. | Low | HE supports TTL per record. Map it 1:1. |
| Priority field for MX/SRV | MX without priority is broken. SRV without priority/weight/port is broken. | Low | Type-specific field mapping. |
| Batch/bulk operations | Ansible with 50+ records needs batch support or it takes forever with serial requests through a headless browser. | High | Not a single HE form action -- must be implemented as sequential browser operations with a queue. Expose as single API call that returns async job status. |
| Error handling with actionable messages | "500 Internal Server Error" is unacceptable. Users need "Record A foo.example.com already exists" or "dns.he.net session expired, retrying". | Medium | Map HE's form validation errors to structured API error responses. |

### Authentication and Authorization

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Bearer token authentication | Standard for API-to-API communication. Terraform/Ansible providers expect a token, not username/password. | Low | JWT or opaque token in Authorization header. PROJECT.md specifies JWT. |
| Multiple tokens per account | Operations teams need separate tokens for Terraform, Ansible, CI/CD, and dev usage. Revoking one must not break others. | Low | Token table in SQLite with account_id FK. |
| Token revocation | Security requirement. Compromised token must be immediately revocable without rotating all tokens. | Low | Delete from SQLite, reject on next request. |
| Role-based access (admin/viewer) | Viewer tokens for monitoring/audit scripts, admin tokens for mutations. Prevents accidental deletions by read-only integrations. | Medium | Middleware checks role claim against HTTP method. GET = viewer OK, POST/PUT/DELETE = admin only. |
| Account isolation | Token for account A must never see or modify account B's zones. | Medium | Every query and browser operation scoped by account_id. Critical security boundary. |

### API Design

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| RESTful resource URLs | `/accounts/{id}/zones/{zone}/records/{id}` -- standard REST. Terraform providers map 1:1 to REST resources. | Low | Design URLs to match Terraform resource model. |
| JSON request/response bodies | Universal standard. No XML, no form encoding for API consumers. | Low | Standard Go JSON marshaling. |
| Proper HTTP status codes | 200 OK, 201 Created, 204 No Content, 400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found, 409 Conflict, 429 Too Many Requests. | Low | Essential for Terraform provider error handling. |
| API versioning (v1 prefix) | Allows breaking changes in v2 without disrupting existing integrations. | Low | `/api/v1/...` prefix on all routes. |
| Health check endpoint | Docker health checks, Kubernetes liveness probes, monitoring systems all need `GET /healthz`. | Low | Return 200 + browser pool status + Vault connectivity status. |
| OpenAPI/Swagger spec | Terraform provider codegen, client SDK generation, and API documentation all benefit from a machine-readable spec. | Medium | Generate from Go struct tags or maintain manually. Enables auto-generated clients. |

### Operational

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Structured logging (JSON) | Production services need parseable logs for ELK/Loki/Grafana. | Low | Use `slog` (Go stdlib). |
| Graceful shutdown | Docker stop must drain in-flight browser operations, not kill mid-mutation. | Medium | Context cancellation propagated to Rod sessions. |
| Configuration via env vars and config file | 12-factor app. Docker Compose and Kubernetes both need env var config. | Low | Viper or similar. |
| Docker container support | Primary deployment target per PROJECT.md. | Low | Multi-stage Dockerfile with Chromium. |
| Standalone binary support | Some operators want to run directly on a VM without Docker. | Medium | Must bundle or detect Chromium. Rod can download Chromium automatically, but this adds complexity for air-gapped environments. |

## Differentiators

Features that set this product apart. Not universally expected, but high value for the specific use case.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Vault integration for HE credentials | dns.he.net passwords never touch disk or SQLite. Far better security posture than config file credentials. Unique among similar tools. | Medium | HashiCorp Vault KV v2. Must handle Vault token renewal, lease expiry. Fallback to env vars for dev/testing. |
| Sync/reconcile mode (desired-state) | Declare desired DNS state, API computes diff and applies only changes. Like `terraform apply` but at the API level. Enables GitOps workflows without Terraform. | High | Requires full zone snapshot, diff algorithm, ordered application (deletes before creates to avoid conflicts). This is the killer feature for Ansible playbooks. |
| BIND zone file import/export | Import existing zones from BIND format (standard). Export for backup/migration. Bridges the gap between traditional DNS management and API-driven. | Medium | Parse BIND zone file format (well-documented RFC 1035). Generate from record list. Enables migration from other providers. |
| Embedded management UI (templ + htmx) | Single binary includes admin UI. No separate frontend deployment. Operators can manage tokens and view accounts without curl/API calls. | Medium | templ + htmx is lightweight. Pages: account list, account detail, token CRUD, token permissions display. |
| Browser session pooling with health checks | Maintain warm browser sessions per HE account. Detect stale/expired sessions and refresh proactively. Dramatically reduces latency for API calls. | High | Rod session pool. Health check = navigate to zone list, verify logged-in state. Evict and recreate on failure. One session per account, mutex-protected. |
| Automatic retry with exponential backoff | HE's web UI may timeout, return errors, or rate-limit. Transparent retry makes the API reliable despite brittle upstream. | Medium | Retry with jitter on browser operation failures. Distinguish retryable (timeout, session expired) from non-retryable (invalid record data). |
| Request queuing per account | Serialize mutations per HE account to prevent concurrent browser conflicts, while allowing reads in parallel. External callers get async job IDs for long operations. | High | Per-account mutex or channel-based queue. Critical for correctness -- two concurrent creates on the same account WILL conflict in HE's session. |
| Audit log | Record who (which token) did what (CRUD operation) and when. Essential for compliance and debugging. | Medium | SQLite table: timestamp, token_id, account_id, action, resource, result. |
| Token expiry (time-limited tokens) | Tokens that auto-expire reduce blast radius of leaked credentials. CI/CD pipelines can get short-lived tokens. | Low | `expires_at` column in token table. Check on auth middleware. |
| Rate limiting (per-token and global) | Protect HE from aggressive automation. Protect the service from abuse. Return 429 with Retry-After header. | Medium | Token bucket or sliding window. Per-token limit prevents one integration from starving others. Global limit respects HE's implicit rate limits. |
| Terraform provider compatibility layer | Expose resources and data sources exactly as Terraform expects: importable by ID, full CRUD lifecycle, proper error codes, drift detection via read. | Medium | Not the Terraform provider itself, but the API contract it needs. Document: GET returns current state, PUT is full replace, DELETE is idempotent, 404 on missing resource. |
| Prometheus metrics endpoint | `/metrics` with request counts, latency histograms, browser pool status, HE error rates. Enables alerting on scraping failures. | Low | `prometheus/client_golang`. Counters: requests_total, errors_total, he_operations_total. Histograms: request_duration, he_operation_duration. Gauges: active_sessions, queued_requests. |

## Anti-Features

Features to explicitly NOT build in v1.

| Anti-Feature | Why Avoid | What to Do Instead |
|--------------|-----------|-------------------|
| TOTP/2FA support for HE accounts | Massively increases browser automation complexity (screenshot TOTP, parse QR). PROJECT.md explicitly excludes this. HE accounts used for automation should not have 2FA enabled. | Document requirement: disable 2FA on HE accounts used with this service. |
| OAuth2/OIDC for API clients | Overkill for server-to-server API. Adds IdP dependency. Bearer tokens are the industry standard for infrastructure APIs (Cloudflare, DigitalOcean, Hetzner all use API tokens). | JWT Bearer tokens with admin/viewer roles. Simple, sufficient, standard. |
| Real-time DNS propagation checking | Different concern entirely (DNS monitoring). Requires querying authoritative and recursive resolvers worldwide. Out of scope. | Point users to external tools (whatsmydns.net, dig commands). |
| Multi-provider support | This is an HE-specific wrapper, not a universal DNS abstraction. Multi-provider = 10x scope, different architecture (plugin system). OctoDNS and dnscontrol already exist for multi-provider. | Stay HE-specific. If users need multi-provider, they use OctoDNS with this as an HE backend. |
| DNSSEC management | HE handles DNSSEC signing server-side. Exposing DNSSEC config via API adds complexity with little value since HE auto-signs. | Document that DNSSEC is managed by HE directly. |
| Custom Terraform provider in v1 | Building and maintaining a Terraform provider is a separate project with its own release cycle, registry publishing, acceptance tests. | Build the API contract that a Terraform provider needs. Build the actual provider as a separate project in v2 or as a community contribution. |
| Email notifications | Not needed for an infrastructure API. Monitoring systems (Prometheus + Alertmanager) handle alerting. | Expose metrics. Let operators wire their own alerting. |
| DNS record templates/presets | Feature creep. Users define their records in Ansible playbooks or Terraform configs, not in the API. | Keep the API CRUD-only. Logic belongs in the IaC tool. |
| Scheduled DNS changes | Cron-like scheduled mutations add state machine complexity. Ansible/Terraform pipelines handle scheduling. | Document how to use CI/CD pipelines for scheduled changes. |
| Web UI for DNS record editing | The embedded UI is for token/account management ONLY. DNS record editing via web UI duplicates HE's own interface and adds massive frontend scope. | API-only for DNS operations. Use HE's web UI for one-off manual changes. |

## Feature Dependencies

```
Vault Integration --> Account Management (accounts need credentials from Vault)
Account Management --> Token Issuance (tokens scoped to accounts)
Token Issuance --> Auth Middleware (middleware validates tokens)
Auth Middleware --> All API Endpoints (every endpoint requires auth)

Browser Session Pool --> Account Management (one session per account)
Browser Session Pool --> DNS Record CRUD (all CRUD goes through browser)
Browser Session Pool --> Zone Management (zone ops go through browser)

DNS Record CRUD --> Record ID Strategy (need stable IDs before CRUD)
DNS Record CRUD --> Idempotency Layer (check-before-create logic)

Zone Listing --> DNS Record Listing (must have zone before listing records)
DNS Record Listing --> Sync/Reconcile (reconcile needs current state snapshot)
DNS Record CRUD --> Sync/Reconcile (reconcile applies CRUD operations)

Request Queue --> Browser Session Pool (queue feeds into session pool)
Rate Limiting --> Request Queue (rate limiter gates the queue)

BIND Import --> DNS Record CRUD (import creates records via CRUD)
BIND Export --> DNS Record Listing (export reads all records)

Health Check --> Browser Session Pool (reports pool status)
Health Check --> Vault Integration (reports Vault connectivity)
Prometheus Metrics --> All Operations (instruments everything)

Embedded UI --> Token Issuance (UI manages tokens)
Embedded UI --> Account Management (UI manages accounts)
```

## Critical Path (Build Order)

The dependency graph reveals a clear critical path:

```
1. Vault Integration + Account Management (foundation)
2. Browser Session Pool + Session Health (the engine)
3. Token Issuance + Auth Middleware (security layer)
4. Zone Listing + Record Listing (read operations)
5. Record CRUD with Idempotency (write operations)
6. Request Queue + Rate Limiting (concurrency safety)
7. Sync/Reconcile + BIND Import/Export (advanced features)
8. Embedded UI (management layer)
9. Prometheus Metrics + Audit Log (observability)
```

## MVP Recommendation

**Prioritize (Phase 1 -- usable API):**

1. Account management with Vault credential storage
2. Browser session pool (single session per account, mutex-protected)
3. Token issuance with admin/viewer roles
4. Auth middleware
5. Zone listing
6. Record listing per zone
7. Record CRUD (A, AAAA, CNAME, MX, TXT -- most common types first)
8. Health check endpoint
9. Structured logging
10. Docker container

**Phase 2 -- production-ready:**

1. Request queuing per account (concurrency safety)
2. Rate limiting
3. Automatic retry with backoff
4. All remaining record types (SRV, CAA, NS, etc.)
5. Idempotency enforcement
6. Audit log
7. Prometheus metrics
8. Token expiry
9. OpenAPI spec

**Phase 3 -- power features:**

1. Sync/reconcile (desired-state mode)
2. BIND zone file import/export
3. Embedded management UI
4. Batch operations API
5. Zone management (add/delete zones, not just records)

**Defer to separate project:**

- Terraform provider (separate repo, separate release cycle)
- Ansible collection/module (separate repo, can be community-driven)

## Terraform Provider Compatibility Requirements

The API must be designed FROM DAY ONE to support a future Terraform provider, even though the provider itself is out of scope for v1. This means:

| Requirement | Why | API Implication |
|-------------|-----|-----------------|
| Stable resource IDs | Terraform tracks resources by ID in state file | Every record and zone must have a persistent, unique ID returned in API responses |
| Full CRUD lifecycle | Terraform calls Create, Read, Update, Delete | All four operations must exist for every resource type |
| Read returns full state | `terraform plan` compares desired vs actual | GET must return ALL fields, not just a subset |
| Idempotent create | `terraform apply` may retry on timeout | POST that creates an already-existing record returns 200 + existing record, not 409 |
| Import by ID | `terraform import` needs to adopt existing resources | GET by ID must work for any resource, returning full state |
| Proper 404 on deleted resources | Terraform detects drift via GET returning 404 | DELETE is idempotent (deleting already-deleted = 404 or 204), GET on missing = 404 |
| No server-side defaults that differ from API | If API omits TTL and server defaults to 300, Terraform sees perpetual drift | API must require all fields or document defaults that match what GET returns |
| Consistent response schema | Provider codegen needs predictable shapes | Same record type always returns same JSON schema |
| Support for data sources | `data.dnshe_record.foo` lookups | GET with query parameters (filter by name, type) must be supported |

## Ansible Compatibility Requirements

| Requirement | Why | API Implication |
|-------------|-----|-----------------|
| Idempotent operations | Ansible modules must be idempotent by contract | Same as Terraform -- check-before-create |
| Check mode support | `ansible-playbook --check` does dry run | API should support a `?dry_run=true` parameter or equivalent that validates without mutating |
| Diff mode support | `ansible-playbook --diff` shows what changed | API responses should include before/after state on mutations |
| Batch-friendly | Playbooks with `loop:` over 100 records | Batch endpoint or at minimum fast sequential performance |
| Fact gathering | `ansible.builtin.setup`-style facts | Zone and record listing endpoints serve as fact sources |

## Sources

- Training data knowledge of Cloudflare API, DigitalOcean DNS API, Hetzner DNS API, AWS Route53 API (DNS API design patterns) -- MEDIUM confidence
- Training data knowledge of Terraform provider SDK requirements and resource lifecycle -- MEDIUM confidence
- Training data knowledge of Ansible module development patterns -- MEDIUM confidence
- Training data knowledge of Rod (Go headless browser library) capabilities -- MEDIUM confidence
- Training data knowledge of dns.he.net web interface structure -- LOW confidence (may have changed)
- PROJECT.md requirements and constraints -- HIGH confidence (primary source)

**Note:** All findings are from training data. No live verification was possible during this research session. dns.he.net's actual web interface structure should be validated by manual inspection before implementation begins.

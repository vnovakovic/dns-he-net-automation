# dns-he-net-automation

## What This Is

A Go-based REST API service that acts as a proxy/wrapper for dns.he.net (Hurricane Electric free DNS), which has no official API. It uses headless browser automation (Rod/Chromium) to drive the dns.he.net web interface programmatically, exposing a clean REST API for external systems such as Ansible, Terraform, and CI/CD pipelines. The service supports multiple dns.he.net accounts, role-based access control per account, and includes an embedded web UI for token management.

## Core Value

External systems can manage DNS records on dns.he.net via a REST API as if it were a first-class DNS provider, without any manual web interaction.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] REST API for full DNS record CRUD (A, AAAA, CNAME, MX, TXT, NS, SOA, etc.)
- [ ] Zone management via API (add/delete zones)
- [ ] BIND zone file import and export
- [ ] Sync/reconcile: apply desired DNS state, detect and apply diffs only
- [ ] Multi dns.he.net account support
- [ ] Per-account admin and viewer roles
- [ ] JWT Bearer token issuance (multiple tokens per account, per role)
- [ ] Token expiry (date-limited or unlimited)
- [ ] Token revocation
- [ ] Embedded frontend (Go + templ + htmx) for account and token management
- [ ] Vault integration for storing dns.he.net credentials (not plaintext in DB)
- [ ] SQLite local database for state (tokens, accounts, metadata)
- [ ] Docker container support + standalone binary support
- [ ] Headless browser pool / session management for dns.he.net (Rod/Chromium)

### Out of Scope

- TOTP/2FA support for dns.he.net accounts — all accounts use user/password only
- OAuth2/OIDC for API clients — Bearer JWT is sufficient
- Real-time DNS propagation checking — out of scope for v1
- Support for other DNS providers — HE-specific only

## Context

- dns.he.net is a free DNS hosting service with a web UI but no official REST API
- All DNS operations must be driven via headless Chromium (Rod library) — brittle by nature, requires resilient session management and retries
- The service must handle concurrent requests while maintaining at most one active browser session per dns.he.net account to avoid login conflicts
- Rate limiting and polite delays are needed to avoid triggering HE's bot detection
- Credentials for dns.he.net accounts are sensitive and must be stored in HashiCorp Vault (not in SQLite)
- The embedded frontend is for admin/operator use only (token management), not for end users
- SQLite is the persistence layer for application state (accounts metadata, tokens, audit log)

## Constraints

- **Tech stack**: Go (Golang) — language is fixed
- **Browser automation**: Rod library (Chromium-based) — chosen over direct HTTP scraping for resilience
- **Secrets**: HashiCorp Vault — dns.he.net credentials never stored in SQLite
- **Database**: SQLite — embedded, no external DB dependency for local deployments
- **Frontend**: templ + htmx — embedded in Go binary, no separate SPA build
- **Auth**: JWT Bearer tokens — admin/viewer roles scoped per dns.he.net account
- **Deployment**: Must work as Docker container AND standalone binary
- **Compatibility**: dns.he.net web UI changes can break automation — needs abstraction layer

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Rod (headless Chromium) over HTTP scraping | HE may use JS, better resilience to layout changes | — Pending |
| One browser session per HE account | Prevents concurrent login conflicts | — Pending |
| JWT opaque-style tokens (not signed claims) | Simpler revocation — just delete from DB | — Pending |
| SQLite over Postgres | Single-binary deployment, no infra dependency | — Pending |
| Vault for HE credentials | dns.he.net passwords are privileged ops access | — Pending |
| templ + htmx over React SPA | Single binary, no build step for deployment | — Pending |

---
*Last updated: 2026-02-26 after initialization*

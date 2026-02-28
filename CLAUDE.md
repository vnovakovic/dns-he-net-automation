# CLAUDE.md — dns-he-net-automation

## Shared API Documentation

The canonical API reference for this project is maintained at:

```
C:\Users\vladimir\Documents\Development\shared\APIs\DNS-HE-NET-AUTOMATION-APIS.md
```

**Rules for keeping it current:**
- **Read this file** at the start of any session that touches API handlers, router, or
  validation — it is the source of truth for routes, request/response shapes, and error codes.
- **Update this file** whenever any of the following change:
  - A new route is added or removed in `internal/api/router.go`
  - A handler's request body, response shape, or status codes change
  - New error codes are introduced in `internal/api/response/`
  - Field validation rules change in `internal/api/validate/records.go`
  - New record types are added to `v1RecordTypes` in `internal/api/handlers/records.go`
  - A new TTL value is added to `allowedTTLs` in `internal/api/validate/records.go`
- **Update the Changelog table** in the shared doc after each plan that modifies the API.
- Never let implementation and shared doc diverge — update the doc in the same commit
  as the code change.

## Key Source Files

| File | Purpose |
|------|---------|
| `internal/api/router.go` | Route registrations — single source of truth for all paths |
| `internal/api/handlers/accounts.go` | Account CRUD handlers |
| `internal/api/handlers/tokens.go` | Token issuance, listing, revocation |
| `internal/api/handlers/zones.go` | Zone list, create, delete |
| `internal/api/handlers/records.go` | Record list, get, create, update, delete |
| `internal/api/handlers/health.go` | Health check handler |
| `internal/api/middleware/auth.go` | BearerAuth JWT validation middleware |
| `internal/api/middleware/rbac.go` | RequireAdmin role middleware |
| `internal/api/validate/records.go` | Field validation (TTL allowlist, type constraints) |
| `internal/api/response/errors.go` | WriteError + WriteJSON helpers |

## Project State

See `.planning/STATE.md` for current phase and progress.
See `.planning/ROADMAP.md` for full phase plan.

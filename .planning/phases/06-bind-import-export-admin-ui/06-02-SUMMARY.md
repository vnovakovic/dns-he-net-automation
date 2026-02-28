---
phase: 06-bind-import-export-admin-ui
plan: "02"
subsystem: ui
tags: [templ, htmx, admin-ui, session-cookie, hmac, go-embed, chi]

# Dependency graph
requires:
  - phase: 05-observability-sync-engine
    provides: browser.SessionManager, resilience.BreakerRegistry, metrics.Registry all wired in main.go
  - phase: 02-api-auth
    provides: JWT secret pattern, Config struct, BearerAuth middleware pattern
provides:
  - internal/api/admin package: AdminAuth middleware (Basic Auth + HMAC-SHA256 session cookie)
  - internal/api/admin/templates: Layout + LoginPage templ components
  - internal/api/admin/static: admin.css + htmx 2.0.8 embedded in binary
  - RegisterAdminRoutes FINAL signature (db, sm, breakers, jwtSecret) — plans 03 and 04 use this unchanged
  - /admin/* sub-router mounted and authenticated — auth layer testable before page handlers exist
affects: [06-03-admin-accounts-tokens, 06-04-admin-zones-sync-audit]

# Tech tracking
tech-stack:
  added:
    - github.com/a-h/templ v0.3.1001 (type-safe HTML templating for Go)
    - htmx 2.0.8 (embedded static asset for AJAX page updates in plans 03/04)
  patterns:
    - templ composition: Layout wraps page content via children... slot (type-safe, no runtime template errors)
    - go:embed static assets: CSS + JS embedded in binary for self-contained deployment
    - HMAC-SHA256 session cookie: hex(HMAC(cookieName+username)):username — no DB lookup on each admin request
    - Stub handler pattern: accept full parameter set in stubs so RegisterAdminRoutes signature is frozen

key-files:
  created:
    - internal/api/admin/middleware.go
    - internal/api/admin/static.go
    - internal/api/admin/router.go
    - internal/api/admin/static/admin.css
    - internal/api/admin/static/htmx.min.js
    - internal/api/admin/templates/layout.templ
    - internal/api/admin/templates/layout_templ.go
    - internal/api/admin/templates/login.templ
    - internal/api/admin/templates/login_templ.go
  modified:
    - internal/config/config.go
    - internal/api/router.go
    - cmd/server/main.go
    - go.mod
    - go.sum

key-decisions:
  - "Admin auth is completely separate from REST API Bearer JWT — rotating JWT_SECRET does not invalidate admin sessions; ADMIN_SESSION_KEY is an independent HMAC signing key"
  - "Basic Auth checked before session cookie — curl/scripted clients get 401 on wrong creds, not a browser redirect that would confuse automation"
  - "RegisterAdminRoutes FINAL signature defined in plan 02 — plans 03 and 04 replace stub handler bodies only, never the function signature or main.go wiring"
  - "templ-generated _templ.go files committed to repo — go build works without templ CLI at build time (RESEARCH.md Pitfall 4)"
  - "go:embed for static assets (not runtime filesystem) — binary is self-contained, works in Docker containers without mounted volumes"
  - "SameSite=Strict on session cookie — prevents CSRF on admin POST/DELETE mutations without needing a CSRF token library"
  - "Hex-encoded ADMIN_SESSION_KEY (not raw bytes) — safe to set in env vars; generate with: openssl rand -hex 32"

patterns-established:
  - "templ composition pattern: Layout(data).Render wraps page content via children... slot"
  - "Stub handler pattern: stubs accept final parameter set so wiring is never rewritten"
  - "Separate auth domain pattern: admin uses session cookie, REST API uses Bearer JWT"

requirements-completed: [UI-01, UI-04, UI-05]

# Metrics
duration: 7min
completed: 2026-02-28
---

# Phase 6 Plan 02: Admin UI Foundation Summary

**Admin UI foundation: HMAC-SHA256 session cookie auth, go:embed static assets (admin.css + htmx 2.0.8), templ Layout + LoginPage components, and frozen RegisterAdminRoutes signature with stub handlers — `go build ./...` exits 0**

## Performance

- **Duration:** 7 min
- **Started:** 2026-02-28T22:35:08Z
- **Completed:** 2026-02-28T22:42:13Z
- **Tasks:** 2
- **Files modified:** 13 (5 created new, 4 hand-authored templ, 4 modified)

## Accomplishments

- Admin auth middleware with Basic Auth first (401 for curl) + HMAC-SHA256 signed session cookie second (redirect for browser) — completely decoupled from Bearer JWT
- go:embed static assets: admin.css (full CSS with custom properties, light/dark mode, sidebar layout, cards, badges, tables) + htmx 2.0.8 minified JS — binary is self-contained
- templ Layout + LoginPage components with _templ.go files committed — `go build` works without templ CLI
- RegisterAdminRoutes FINAL signature frozen in plan 02 — plans 03 and 04 only replace stub handler bodies

## Task Commits

Each task was committed atomically:

1. **Task 1: templ dependency, admin config fields, auth middleware, static assets** - `f16347e` (feat)
2. **Task 2: templ templates, admin router, main router wiring** - `f0a4be2` (feat)

**Plan metadata:** *(see final commit below)*

## Files Created/Modified

- `internal/api/admin/middleware.go` - AdminAuth middleware, IssueSessionCookie, ClearSessionCookie, validateSessionCookie (constant-time HMAC comparison)
- `internal/api/admin/static.go` - go:embed directive for static/admin.css and static/htmx.min.js
- `internal/api/admin/router.go` - RegisterAdminRoutes FINAL signature + 12 stub handlers + login/logout/redirect handlers
- `internal/api/admin/static/admin.css` - Full production CSS: custom properties, light/dark mode, sidebar layout, cards, buttons, badges, tables, forms, login page
- `internal/api/admin/static/htmx.min.js` - htmx 2.0.8 downloaded and embedded (51 KB)
- `internal/api/admin/templates/layout.templ` - Base HTML layout with sidebar nav, top bar, content slot
- `internal/api/admin/templates/layout_templ.go` - Templ-generated (committed per RESEARCH.md Pitfall 4)
- `internal/api/admin/templates/login.templ` - Standalone login form page (plain POST, no htmx)
- `internal/api/admin/templates/login_templ.go` - Templ-generated (committed)
- `internal/config/config.go` - Added AdminUsername, AdminPassword, AdminSessionKey fields
- `internal/api/router.go` - Added admin import, extended NewRouter signature, mounted RegisterAdminRoutes
- `cmd/server/main.go` - Pass cfg.AdminUsername/Password/SessionKey to NewRouter (single update for all plans)
- `go.mod` / `go.sum` - Added github.com/a-h/templ v0.3.1001

## Decisions Made

- Admin auth completely separate from REST Bearer JWT — rotating JWT_SECRET does not invalidate admin sessions; independent ADMIN_SESSION_KEY signing key
- Basic Auth checked before session cookie — curl/scripts get 401 on wrong credentials (not a redirect that confuses automation tools)
- RegisterAdminRoutes FINAL signature defined in plan 02 — plans 03 and 04 replace stub handler bodies, never touch main.go or NewRouter
- templ _templ.go files committed to repo — `go build` works without templ CLI installed (RESEARCH.md Pitfall 4)
- go:embed for static assets — self-contained binary, no filesystem dependency at runtime
- SameSite=Strict session cookie — prevents CSRF on admin mutations without CSRF token library
- Hex-encoded ADMIN_SESSION_KEY env var — safe for shell, easy to generate: `openssl rand -hex 32`

## Deviations from Plan

None — plan executed exactly as written.

Minor note: router.go already had BIND export/import routes from plan 06-01 (which ran before this plan). The NewRouter signature extension accommodated these cleanly.

## Issues Encountered

`go get github.com/a-h/templ@latest` initially failed twice with "existing contents have changed since last read" — a harmless file locking race between the two parallel `go get` attempts. Resolved by running `go get github.com/a-h/templ` (without `@latest`) which succeeded immediately. Templ was added to go.mod after the generated _templ.go files were created and `go mod tidy` resolved the import.

## User Setup Required

Three new environment variables needed for admin UI access:

```bash
ADMIN_USERNAME=admin          # Admin UI username
ADMIN_PASSWORD=<strong-pass>  # Admin UI password
ADMIN_SESSION_KEY=$(openssl rand -hex 32)  # 32-byte hex HMAC signing key for session cookies
```

Without these, the admin UI is accessible at /admin but all form logins will fail (wrong credentials) and session cookies will use a zero key (non-persistent sessions). The server starts normally regardless — safe-fail behavior.

## Next Phase Readiness

- Plan 03 (admin accounts + tokens page): ready — RegisterAdminRoutes stub handlers for accounts/tokens accept full db + jwtSecret parameter set; replace handleAccountsPage, handleAccountCreate, handleAccountDelete, handleTokensPage, handleTokensForAccount, handleTokenIssue, handleTokenRevoke
- Plan 04 (admin zones + sync + audit page): ready — stub handlers for zones/sync/audit accept full db + sm + breakers parameter set; replace handleZonesPage, handleSyncPage, handleSyncTrigger, handleAuditPage
- Auth layer is testable immediately — GET /admin redirects to /admin/login, POST /admin/login with correct credentials issues cookie and redirects to /admin/accounts (501)

## Self-Check: PASSED

All files verified present on disk. All task commits verified in git log.

- internal/api/admin/middleware.go: FOUND
- internal/api/admin/static.go: FOUND
- internal/api/admin/router.go: FOUND
- internal/api/admin/static/admin.css: FOUND
- internal/api/admin/static/htmx.min.js: FOUND
- internal/api/admin/templates/layout.templ: FOUND
- internal/api/admin/templates/layout_templ.go: FOUND
- internal/api/admin/templates/login.templ: FOUND
- internal/api/admin/templates/login_templ.go: FOUND
- Task 1 commit f16347e: FOUND
- Task 2 commit f0a4be2: FOUND

---
*Phase: 06-bind-import-export-admin-ui*
*Completed: 2026-02-28*

# Phase 6: BIND Import/Export + Admin UI - Context

**Gathered:** 2026-02-28
**Status:** Ready for planning

<domain>
## Phase Boundary

Two capabilities in one phase:
1. **BIND I/O** — `GET /api/v1/zones/{zoneID}/export` returns zone as BIND zone file; `POST /api/v1/zones/{zoneID}/import` accepts a BIND zone file and applies it via the sync engine.
2. **Admin UI** — Embedded web UI at `/admin` (templ + htmx, Go binary embed) for managing accounts, tokens, zones, sync, and audit log.

All existing REST API endpoints remain unchanged. The UI is additive — every action the UI performs is also available via REST API.

</domain>

<decisions>
## Implementation Decisions

### Import behavior
- **Additive only** — Records present in the live zone but absent from the import file are kept untouched. Import never deletes.
- No `?mode=replace` toggle. Full-replace is out of scope for this phase.
- **Unsupported record types** (e.g., HTTPS, SVCB — not in v1RecordTypes): skip them, do not fail. The response includes a dry-run-style report listing which records were applied, which were skipped (with reason), and which failed.
- Response shape mirrors `POST /sync`: `{ "applied": [...], "skipped": [...], "had_errors": bool }`. HTTP 200 always (same pattern as sync handler).
- `?dry_run=true` supported — returns what would change without applying, consistent with sync engine behavior.

### Export format
- **User-created records only** — A, AAAA, CNAME, MX, TXT, SRV, CAA, NS (user-added delegation NS, not HE's authoritative NS), and any other record in the zone that maps to a v1RecordType.
- SOA record is excluded — HE manages it and it is not exposed in the REST API.
- HE's own authoritative NS records are excluded.
- Output: standard BIND zone file with `$ORIGIN`, `$TTL` (derived from first record or 3600 default), and per-record lines. Generated via `miekg/dns` `dns.RR` serialization.

### Admin UI authentication
- Two auth mechanisms, both protecting `/admin`:
  1. **Session cookie** — Admin submits username + password on a login form (`POST /admin/login`). Credentials come from env vars (`ADMIN_USERNAME`, `ADMIN_PASSWORD`). On success, server issues a signed session cookie (HttpOnly, SameSite=Strict). Logout clears the cookie.
  2. **HTTP Basic Auth** — Also accepted on all `/admin` routes for scripted/curl access. Same `ADMIN_USERNAME` / `ADMIN_PASSWORD` env vars. Checked before the cookie path.
- The REST API's Bearer JWT is NOT used for `/admin` — admin UI has its own auth layer.
- Both mechanisms must be active simultaneously (Basic Auth checked first, then cookie session).

### Admin UI scope
Full management surface — not just accounts + tokens:
- **Accounts**: list, register, remove
- **Tokens**: list (per account), issue, revoke
- **Zones**: list zones per account (read-only view — zone create/delete still require curl; zone CRUD via browser is expensive and out of scope for UI)
- **Sync**: trigger `POST /api/v1/zones/{zoneID}/sync` with dry_run toggle from UI; display result diff
- **Audit log**: paginated table of recent audit_log entries (account, zone, action, timestamp, success/error)

Claude's Discretion: pagination size (default 50), exact table columns, form layout within pages.

### Admin UI style — matches oracle-apex visual language
The oracle-apex project uses shadcn/ui + Tailwind CSS with this design system:
- **Layout**: Fixed sidebar (240px) + header bar (56px) + scrollable main area — app-shell pattern
- **Color tokens** (CSS custom properties, same names as shadcn/ui):
  - Light: `--background: #fff`, `--foreground: #213547`, `--border: #e4e4e7`, `--muted: #f4f4f5`, `--accent: #f4f4f5`, `--primary: #18181b`
  - Dark: `--background: #242424`, `--foreground: rgba(255,255,255,0.87)`, `--border: #27272a`
  - Accent: `#646cff` (purple, used for active nav, primary buttons, focus rings)
- **Typography**: `system-ui, Avenir, Helvetica, Arial, sans-serif`; `line-height: 1.5`; `font-size: 14px` for body, `16px` for headings
- **Border radius**: `0.5rem` (cards, buttons, nav items, badges)
- **Nav links**: `rounded-md`, `padding: 6px 12px`, icon (16px) + text, hover `bg-accent`, active `bg-accent text-accent-foreground`
- **Cards**: `border: 1px solid var(--border)`, `border-radius: 0.5rem`, `padding: 1.5rem`
- **Badges**: small pill labels for status (e.g., "active" = green, "revoked" = red, "admin" = purple)
- **Dark/light mode**: `prefers-color-scheme` media query — same dual-mode as oracle-apex
- Implementation: **single embedded CSS file** (no Tailwind build step). CSS custom properties for theming. No external CDN dependency — self-contained binary.
- htmx interactions: inline table rows for CRUD (add/remove without full page reload), confirmation dialogs for destructive actions, no-JS fallback not required.

</decisions>

<specifics>
## Specific Ideas

- Import and sync share the same response shape (`applied`, `skipped`, `had_errors`, `dry_run`) — consistency makes API consumers predictable.
- The admin UI visually matches oracle-apex: same sidebar layout, same purple `#646cff` accent, same dark-by-default with light-mode support. An operator familiar with oracle-apex will feel at home.
- Sync from the UI: show a before/after diff table (same as dry_run response) with a "Apply" button to confirm.
- Audit log page: newest-first, action column color-coded (green=create, yellow=update, red=delete, blue=sync).

</specifics>

<deferred>
## Deferred Ideas

- Zone create/delete via admin UI — browser automation is expensive; REST API is the right tool. Not in this phase.
- Full BIND-format record editing in UI — out of scope; this is a management UI, not a DNS editor.
- RBAC for admin UI (viewer vs admin role within /admin) — current design: /admin is all-or-nothing admin access. Could be a future phase.
- Multi-account sync trigger from UI — triggering sync across all accounts at once. Could be future milestone.

</deferred>

---

*Phase: 06-bind-import-export-admin-ui*
*Context gathered: 2026-02-28*

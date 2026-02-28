---
phase: 03-dns-operations
plan: 01
subsystem: api
tags: [playwright-go, dns, zones, browser-automation, rest-api, chi, slog]

requires:
  - phase: 02-api-auth
    provides: WithAccount session manager, BearerAuth/RequireAdmin middleware, response.WriteError pattern, claims middleware
  - phase: 01-foundation-browser-core
    provides: ZoneListPage, GetZoneID, ListZones, NavigateToZoneList page object methods

provides:
  - SelectorAddZoneTrigger constant in selectors.go (a[onclick*="add_zone"])
  - ZoneListPage.AddZone(domainName) -> (zoneID, error)
  - ZoneListPage.DeleteZone(zoneID, zoneName) -> error (prompt() handled via OnDialog)
  - ZoneListPage.GetZoneName(zoneID) -> (name, error)
  - GET /api/v1/zones — ListZones handler with fetched_at timestamp
  - POST /api/v1/zones — CreateZone handler (idempotent, 200 existing / 201 new)
  - DELETE /api/v1/zones/{zoneID} — DeleteZone handler (idempotent, 204 always)
  - zones_test.go — 5 HTTP-layer validation unit tests

affects:
  - 03-02-dns-records (record operations require zones to exist; zone page navigation patterns reused)
  - 04-vault-integration (zone handlers follow same credential/session pattern)

tech-stack:
  added: []
  patterns:
    - "ZoneListPage.DeleteZone uses page.OnDialog (playwright-go typed method) registered BEFORE click to handle prompt() dialogs"
    - "Dialog.Accept(promptText string) passes response text to prompt() in single variadic call (playwright-go v0.5700.1 API)"
    - "Idempotent create: GetZoneID pre-check inside WithAccount closure, boolean existed flag signals 200 vs 201"
    - "Idempotent delete: GetZoneName not-found returns nil inside WithAccount, outer handler writes 204"
    - "ZoneResponse struct includes FetchedAt time.Time for API-05 timestamp requirement"
    - "Error mapping: ErrQueueTimeout->429, ErrSessionUnhealthy->503, other->500 with slog timing"

key-files:
  created:
    - internal/api/handlers/zones.go
    - internal/api/handlers/zones_test.go
  modified:
    - internal/browser/pages/selectors.go
    - internal/browser/pages/zonelist.go
    - internal/api/router.go

key-decisions:
  - "playwright-go v0.5700.1 Dialog API uses page.OnDialog(func(dialog playwright.Dialog)) not page.On(\"dialog\", ...) - typed method not generic event emitter"
  - "Dialog.Accept(\"DELETE\") variadic form serves as both Fill+Accept in one call - no separate Fill method exists in this API version"
  - "GetZoneID error return treated as zone-not-found (idempotency) in both CreateZone pre-check and DeleteZone verification"
  - "ZoneResponse.FetchedAt set to start time of the handler (before WithAccount call) for consistent timestamps across list items"

patterns-established:
  - "OnDialog pre-registration pattern: register handler before click that triggers prompt(), not after"
  - "Idempotency via pre-check inside WithAccount closure using boolean flag for status code selection"

requirements-completed: [ZONE-01, ZONE-02, ZONE-03, ZONE-04, API-05, COMPAT-01]

duration: 12min
completed: 2026-02-28
---

# Phase 3 Plan 01: Zone Operations Summary

**Zone CRUD via Playwright page object (AddZone/DeleteZone/GetZoneName) wired into three REST handlers (GET/POST/DELETE /api/v1/zones) with idempotent semantics and prompt() dialog handling**

## Performance

- **Duration:** 12 min
- **Started:** 2026-02-28T08:28:00Z
- **Completed:** 2026-02-28T08:40:47Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- Zone page object extended with AddZone, DeleteZone, GetZoneName methods — covering full CRUD on dns.he.net zones
- DeleteZone correctly handles dns.he.net's prompt()-based confirmation via pre-registered OnDialog handler
- Three zone API handlers (ListZones, CreateZone, DeleteZone) with idempotent semantics (200/201 on create, 204 on delete regardless of existence)
- Zone routes registered in chi router with RequireAdmin enforcement on mutations
- 5 HTTP-layer unit tests covering validation cases without browser dependency

## Task Commits

1. **Task 1: Add SelectorAddZoneTrigger and page methods** - `0d72ca9` (feat)
2. **Task 2: Zone API handlers, unit tests, router registration** - `b65f1da` (feat)

**Plan metadata:** (committed below with SUMMARY.md)

## Files Created/Modified
- `internal/browser/pages/selectors.go` - Added SelectorAddZoneTrigger = `a[onclick*="add_zone"]`
- `internal/browser/pages/zonelist.go` - Added AddZone, DeleteZone, GetZoneName methods
- `internal/api/handlers/zones.go` - ListZones, CreateZone, DeleteZone HTTP handlers with ZoneResponse struct
- `internal/api/handlers/zones_test.go` - 5 unit tests for HTTP-layer input validation
- `internal/api/router.go` - GET/POST/DELETE /api/v1/zones route block with RequireAdmin on mutations

## Decisions Made
- **playwright-go Dialog API:** `page.OnDialog(func(dialog playwright.Dialog))` is the correct typed method (not `page.On("dialog", ...)`). The generic `On` string-based event emitter does not exist; each event has a typed `OnXxx` method. Found during build: `dialog.Fill undefined` then corrected.
- **Dialog.Accept variadic form:** `Accept("DELETE")` passes the prompt response text in one call. No separate `Fill` method exists in playwright-go v0.5700.1 — `Accept(promptText ...string)` combines both operations.
- **FetchedAt timing:** Set to handler `start` time (before `WithAccount`) so all zones in a list response share the same consistent timestamp.
- **Idempotency flag pattern:** `var existed bool` declared before `sm.WithAccount` closure, set inside on the pre-check path. Clean separation between browser-layer logic (inside closure) and HTTP response selection (outside closure).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected playwright-go dialog API usage**
- **Found during:** Task 1 (AddZone/DeleteZone implementation)
- **Issue:** Plan specified `page.On("dialog", func(dialog playwright.Dialog) { dialog.Fill("DELETE"); dialog.Accept() })`. The playwright-go v0.5700.1 library does not have a generic `page.On` method for dialog events, and `Dialog` has no `Fill` method.
- **Fix:** Changed to `page.OnDialog(func(dialog playwright.Dialog) { dialog.Accept("DELETE") })` — typed method with variadic Accept covering both fill and accept.
- **Files modified:** internal/browser/pages/zonelist.go
- **Verification:** `go build ./internal/browser/...` succeeded after fix
- **Committed in:** `0d72ca9` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - API mismatch bug)
**Impact on plan:** Essential correction — dialog handling would have been a compile error. No scope creep.

## Issues Encountered
- playwright-go v0.5700.1 dialog API differs from the plan's specification. The plan's pseudocode referenced `page.On("dialog", ...)` with a separate `Fill` call, but the actual library uses `page.OnDialog()` with `Accept(promptText)` accepting the response text as a variadic argument. Fixed immediately via Rule 1.

## User Setup Required
None — no external service configuration required.

## Next Phase Readiness
- Zone CRUD fully operational at REST layer
- Plan 03-02 can proceed: record operations require only `zoneID` which is now retrievable via `GET /api/v1/zones`
- ZoneListPage navigation pattern (NavigateToZoneList + NavigateToZone) established for record page object reuse

---
*Phase: 03-dns-operations*
*Completed: 2026-02-28*

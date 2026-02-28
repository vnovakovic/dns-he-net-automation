---
phase: 03-dns-operations
plan: 02
subsystem: api
tags: [playwright-go, dns, records, browser-automation, rest-api, chi, slog, idempotency]

requires:
  - phase: 03-dns-operations
    plan: 01
    provides: ZoneListPage.NavigateToZone, GetRecordRows, GetZoneName page object methods; zone API handlers pattern; WithAccount/RequireAdmin/response.WriteError patterns
  - phase: 01-foundation-browser-core
    provides: RecordFormPage (OpenNewRecordForm, EditExistingRecord, FillAndSubmit, DeleteRecord)

provides:
  - ZoneListPage.ParseRecordRow(rowID) -> (*model.Record, error)
  - ZoneListPage.ListRecords(zoneID) -> ([]model.Record, error)
  - ZoneListPage.FindRecord(zoneID, rec) -> (string, error)
  - recordsMatch(a, b model.Record) bool — unexported type-specific matching helper
  - GET /api/v1/zones/{zoneID}/records — ListRecords handler
  - POST /api/v1/zones/{zoneID}/records — CreateRecord handler (idempotent, 200 existing / 201 new)
  - GET /api/v1/zones/{zoneID}/records/{recordID} — GetRecord handler
  - PUT /api/v1/zones/{zoneID}/records/{recordID} — UpdateRecord handler
  - DELETE /api/v1/zones/{zoneID}/records/{recordID} — DeleteRecord handler (idempotent, 204 always)
  - records_test.go — 8 HTTP-layer validation unit tests
  - handleBrowserError DRY helper used by all record handlers
  - v1RecordTypes map enforcing 8-type subset (A/AAAA/CNAME/MX/TXT/SRV/CAA/NS)

affects:
  - 04-vault-integration (record handlers follow same credential/session pattern as zones)

tech-stack:
  added: []
  patterns:
    - "ParseRecordRow uses verified td indices: ID=td[1], Name=td[2], Type=td[3], TTL=td[4], Priority=td[5], Content=td[6], DDNS=td[7] — 10 tds per tr.dns_tr"
    - "SRV content decomposition: td[6] holds 'Weight Port Target' space-separated; split with strings.Fields into Weight/Port/Target, Content left empty"
    - "Priority '-' parses to 0 for non-MX/SRV types; numeric string parsed with strconv.Atoi for MX/SRV"
    - "FindRecord idempotency: type-specific matching via recordsMatch (MX=Content+Priority, SRV=Priority+Weight+Port+Target, default=Content)"
    - "CreateRecord idempotency: FindRecord pre-check inside WithAccount closure; boolean existed flag selects 200 vs 201"
    - "DeleteRecord idempotency: GetRecordRows scan before DeleteRecord JS call; 204 returned whether found or not"
    - "handleBrowserError DRY helper: ErrQueueTimeout->429, ErrSessionUnhealthy->503, other->500"
    - "errRecordNotFound sentinel propagated through WithAccount closure, checked with errors.Is after closure"
    - "v1RecordTypes map at handler level — 422 returned before any browser operation for unsupported types"

key-files:
  created:
    - internal/api/handlers/records.go
    - internal/api/handlers/records_test.go
  modified:
    - internal/browser/pages/zonelist.go
    - internal/api/router.go

key-decisions:
  - "ParseRecordRow uses InnerText on individual td Locators (cells.Nth(idx).InnerText()) rather than GetAttribute — td cells hold text content not attribute values"
  - "ListRecords skips locked rows with slog.Warn rather than returning error — SOA and other system rows cannot be managed via API and should not block list operations"
  - "validateRecordFields extracted as shared helper to avoid duplicating MX/SRV validation logic between CreateRecord and UpdateRecord"
  - "UpdateRecord calls ParseRecordRow after FillAndSubmit to return the authoritative server-side record state rather than echoing the request body"
  - "DeleteRecord navigates to zone page before GetRecordRows scan — requires NavigateToZone to load the record table before scanning for the row ID"

metrics:
  duration: 15min
  completed: 2026-02-28T08:46:57Z
  tasks: 2
  files_modified: 4
---

# Phase 3 Plan 02: DNS Record Operations Summary

**Record page object methods (ParseRecordRow/ListRecords/FindRecord) and five record API handlers (GET/POST/GET single/PUT/DELETE /api/v1/zones/{zoneID}/records) with type-specific field matching for idempotency and 8-type v1 enforcement**

## Performance

- **Duration:** 15 min
- **Started:** 2026-02-28T08:31:00Z
- **Completed:** 2026-02-28T08:46:57Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- ZoneListPage extended with ParseRecordRow (verified 10-td index extraction with SRV decomposition), ListRecords (NavigateToZone + GetRecordRows + skip locked), and FindRecord (type-specific matching via recordsMatch)
- recordsMatch unexported helper provides type-correct idempotency: default=Content, MX=Content+Priority, SRV=Priority+Weight+Port+Target
- Five record API handlers in records.go following the same slog-timing pattern as zones.go
- handleBrowserError DRY helper eliminates repeated error-switch boilerplate across all handlers
- v1RecordTypes map enforces 8-type subset at handler entry — unsupported types get 422 before any browser operation
- CreateRecord idempotent: FindRecord pre-check, returns 200+existing or 201+new
- DeleteRecord idempotent: GetRecordRows scan, returns 204 always whether record existed or not
- errRecordNotFound sentinel propagated cleanly through WithAccount closures; checked with errors.Is after closure
- 8 HTTP-layer unit tests covering NilClaims (3), MissingBody (1), UnsupportedType (1), MissingName (1), MXMissingPriority (1), SRVMissingPort (1)
- Record routes nested under /zones/{zoneID}/records in router.go with RequireAdmin on POST/PUT/DELETE

## Task Commits

1. **Task 1: ParseRecordRow, ListRecords, FindRecord page object methods** - `5914eca` (feat)
2. **Task 2: Record API handlers, unit tests, router registration** - `ebaeb08` (feat)

## Files Created/Modified

- `internal/browser/pages/zonelist.go` - Added ParseRecordRow, ListRecords, FindRecord, recordsMatch (187 lines added)
- `internal/api/handlers/records.go` - All five record handlers with RecordResponse, toRecordResponse, handleBrowserError, v1RecordTypes, validateRecordFields, errRecordNotFound
- `internal/api/handlers/records_test.go` - 8 HTTP-layer validation unit tests (165 lines)
- `internal/api/router.go` - Nested record routes under /{zoneID}/records with RequireAdmin enforcement

## Decisions Made

- **ParseRecordRow uses InnerText on Locators:** Each td cell content read via `cells.Nth(idx).InnerText()`. Text nodes not in attributes, so GetAttribute would return empty strings.
- **ListRecords skips locked rows with slog.Warn:** SOA and system rows fail parsing (wrong td count or locked state) — slog.Warn + continue is correct here; returning an error would break listing of all manageable records.
- **validateRecordFields shared helper:** MX priority>0 and SRV priority>0+port>0+target-nonempty validation used in both CreateRecord and UpdateRecord. Extracted to avoid duplication.
- **UpdateRecord returns parsed record (not echoed body):** After FillAndSubmit, ParseRecordRow fetches authoritative state from the page. This ensures the response reflects what dns.he.net actually stored.
- **DeleteRecord navigates before scanning:** GetRecordRows requires the zone page to be loaded. NavigateToZone is called explicitly inside the closure before GetRecordRows.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- Full DNS record CRUD operational at REST layer for all 8 v1 types
- Plan 03-03 (if any) can proceed — zone+record CRUD is complete
- Phase 4 (Vault integration) can consume these handlers — same credential/session pattern is in place

---
*Phase: 03-dns-operations*
*Completed: 2026-02-28*

## Self-Check: PASSED

**Files verified:**
- FOUND: internal/browser/pages/zonelist.go
- FOUND: internal/api/handlers/records.go
- FOUND: internal/api/handlers/records_test.go
- FOUND: internal/api/router.go

**Commits verified:**
- FOUND: 5914eca (feat(03-02): add ParseRecordRow, ListRecords, FindRecord to ZoneListPage)
- FOUND: ebaeb08 (feat(03-02): add record API handlers, unit tests, and router registration)

**Test results:** 13/13 handler tests PASS, 11/11 browser tests PASS
**Build:** go build ./... zero errors
**Vet:** go vet ./... zero warnings

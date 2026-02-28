---
phase: 05-observability-sync-engine
plan: "05"
subsystem: api
tags: [sync, reconcile, audit, metrics, chi, browser, go]

# Dependency graph
requires:
  - phase: 05-02
    provides: "metrics.Registry with SyncOpsTotal counter; WithAccount opType; NewRouter reg param"
  - phase: 05-03
    provides: "audit.Write() and audit.Entry struct for post-apply audit trail"
  - phase: 05-04
    provides: "reconcile.DiffRecords() and reconcile.Apply() with delete/update/create closures"
provides:
  - "POST /api/v1/zones/{zoneID}/sync handler (SyncRecords) wired into chi router behind RequireAdmin"
  - "dry_run=true returns SyncPlan without browser mutations"
  - "Non-dry-run applies deletes, updates, adds via reconcile.Apply — no short-circuit"
  - "had_errors=true in response body when any op fails; HTTP 200 always"
  - "dnshe_sync_operations_total counter incremented per applied operation"
  - "audit.Write called after apply with action=sync, resource=zone:<id>"
  - "Shared API doc updated: GET /metrics + POST /sync documented"
affects:
  - "Phase 6: sync endpoint is now the primary API surface for Terraform/automation clients"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "syncHTTPResponse inline struct — owns had_errors field without modifying reconcile.SyncResponse"
    - "delete closure: NavigateToZone + GetZoneName + ParseRecordRow + rf.DeleteRecord (mirrors DeleteRecord handler)"
    - "update closure: NavigateToZone + EditExistingRecord + rf.FillAndSubmit (mirrors UpdateRecord handler)"
    - "create closure: NavigateToZone + OpenNewRecordForm + rf.FillAndSubmit (mirrors CreateRecord handler)"
    - "Audit written after apply, regardless of partial failure — non-fatal (slog.ErrorContext)"
    - "nil-guard on reg *metrics.Registry — safe for unit tests without metrics registry"

key-files:
  created:
    - internal/api/handlers/sync.go
  modified:
    - internal/api/router.go
    - C:/Users/vladimir/Documents/Development/shared/APIs/DNS-HE-NET-AUTOMATION-APIS.md

key-decisions:
  - "syncHTTPResponse defined in sync.go (not in reconcile) — had_errors is HTTP-layer concern, not reconcile concern"
  - "DELETE /api/v1/zones/{zoneID} kept as sibling route alongside Route(/{zoneID}) subrouter — chi resolves correctly by method"
  - "HTTP 200 always for sync response — had_errors in body signals partial failure; avoids 207 Multi-Status complexity"
  - "dry_run path skips audit.Write — no mutations occurred, nothing to audit"
  - "Delete closure must re-navigate per call (NavigateToZone) — each Apply invocation uses fresh WithAccount session"

requirements-completed:
  - SYNC-01
  - SYNC-02
  - SYNC-03
  - SYNC-04
  - SYNC-05
  - SYNC-06

# Metrics
duration: 3min
completed: 2026-02-28
---

# Phase 5 Plan 05: Sync HTTP Handler and Router Wiring Summary

**SyncRecords handler connecting reconcile diff engine + audit log + Prometheus metrics into POST /api/v1/zones/{zoneID}/sync — with dry_run preview, partial failure support, and shared API doc updated**

## Performance

- **Duration:** 3 min
- **Started:** 2026-02-28T13:38:45Z
- **Completed:** 2026-02-28T13:41:08Z
- **Tasks:** 2
- **Files modified:** 3 (sync.go created, router.go modified, DNS-HE-NET-AUTOMATION-APIS.md updated)

## Accomplishments

- Created `internal/api/handlers/sync.go` with SyncRecords handler factory (`db, sm, breakers, reg *metrics.Registry`)
- Handler extracts claims, zoneID, dry_run query param; decodes desired state from JSON body
- Scrapes live state via `breakers.Execute + resilience.WithRetry + sm.WithAccount("list_records", ...)`
- Computes diff with `reconcile.DiffRecords(current, desired)`
- dry_run path responds immediately with plan; no mutations, no audit
- Three apply closures (deleteFn, updateFn, createFn) each wrap their own `breakers.Execute + WithRetry + WithAccount`
  - deleteFn: NavigateToZone + GetZoneName + ParseRecordRow + rf.DeleteRecord
  - updateFn: NavigateToZone + EditExistingRecord + rf.FillAndSubmit
  - createFn: NavigateToZone + OpenNewRecordForm + rf.FillAndSubmit
- `reconcile.Apply` called with delete → update → add order, no short-circuit (SYNC-04)
- SyncOpsTotal incremented per result with op_type + result labels (nil-guarded)
- `audit.Write` called with `action="sync"`, `resource="zone:<id>"` after apply
- HTTP 200 always; `had_errors` in response body signals partial failure
- Updated `internal/api/router.go`: added `r.Route("/{zoneID}", ...)` subrouter containing `/sync` behind RequireAdmin and `/records` subrouter
- Updated shared API doc at `C:/Users/vladimir/Documents/Development/shared/APIs/DNS-HE-NET-AUTOMATION-APIS.md`:
  - Added `GET /metrics` section with 8 dnshe_* metrics table
  - Added `POST /api/v1/zones/{zoneID}/sync` section with full request/response examples
  - Updated Full Route Table with `/metrics` and `/sync` rows
  - Added Changelog entry for Phase 5 Plan 05-05

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement SyncRecords handler** - `b04ddff` (feat)
2. **Task 2: Register sync route in router and update shared API doc** - `5551a6e` (feat)

## Files Created/Modified

- `internal/api/handlers/sync.go` — SyncRecords handler (225 lines); syncHTTPResponse struct; delete/update/create closures; metrics + audit integration
- `internal/api/router.go` — Added `r.Route("/{zoneID}", ...)` with POST /sync + RequireAdmin; /records subrouter nested inside
- `C:/Users/vladimir/Documents/Development/shared/APIs/DNS-HE-NET-AUTOMATION-APIS.md` — GET /metrics section; POST /sync section; Full Route Table updated; Changelog updated

## Decisions Made

- `syncHTTPResponse` defined in `sync.go` (not promoted to reconcile package) — `had_errors` is an HTTP-layer concern
- `DELETE /{zoneID}` and `Route("/{zoneID}", ...)` are sibling registrations in chi — chi resolves by method without conflict
- HTTP 200 always for sync — `had_errors` in body is the error signal; avoids HTTP 207 complexity
- `dry_run` path skips audit.Write — no mutations occurred, nothing to record
- Each apply closure independently wraps `breakers.Execute + WithRetry + WithAccount` — consistent with existing handler patterns

## Deviations from Plan

None - plan executed exactly as written.

The plan mentioned `pages.NewRecordPage` but the actual pages package has `pages.NewZoneListPage` (for ListRecords/NavigateToZone) and `pages.NewRecordFormPage` (for DeleteRecord/FillAndSubmit). The handler uses the correct constructors per the actual codebase.

## Issues Encountered

None. Both files compiled clean on first attempt. All 17 existing tests passed without modification.

## User Setup Required

None.

## Next Phase Readiness

- Phase 5 is complete. All requirements satisfied: OBS-01, OBS-02, SYNC-01 through SYNC-06.
- `POST /api/v1/zones/{zoneID}/sync` is production-ready for Terraform/automation clients
- `GET /metrics` is production-ready for Prometheus scraping
- `go build ./...`, `go vet ./...`, `go test ./...` all pass clean

## Self-Check: PASSED

- FOUND: `internal/api/handlers/sync.go`
- FOUND: commit `b04ddff` (Task 1 - SyncRecords handler)
- FOUND: commit `5551a6e` (Task 2 - router + API doc)
- Build passes: `go build ./...`
- Vet passes: `go vet ./...`
- Tests pass: `go test ./...` (all packages, no failures)
- grep "sync" internal/api/router.go: shows POST /sync route with RequireAdmin
- grep "DiffRecords" internal/api/handlers/sync.go: present
- grep "Apply" internal/api/handlers/sync.go: present (reconcile.Apply)
- grep "audit.Write" internal/api/handlers/sync.go: present
- grep "SyncOpsTotal" internal/api/handlers/sync.go: present with nil-guard
- Shared API doc: GET /metrics section added
- Shared API doc: POST /sync section added with both dry_run and apply response examples
- Shared API doc: Full Route Table updated
- Shared API doc: Changelog entry added

---
*Phase: 05-observability-sync-engine*
*Completed: 2026-02-28*

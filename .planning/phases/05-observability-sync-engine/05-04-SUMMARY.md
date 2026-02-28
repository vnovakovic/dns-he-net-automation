---
phase: 05-observability-sync-engine
plan: "04"
subsystem: api
tags: [reconcile, diff, sync, dns, tdd, go]

# Dependency graph
requires:
  - phase: 01-foundation-browser-core
    provides: model.Record and model.RecordType used in DiffRecords and Apply signatures
provides:
  - RecordKey struct with (Type, Name, Content) + SRV-specific Priority/Weight/Port disambiguation
  - SyncPlan struct (Add/Update/Delete slices of model.Record)
  - SyncResult struct (Op, Record, Status, ErrorMsg)
  - SyncResponse envelope (DryRun, Plan, Results with json tags)
  - DiffRecords() pure-function diff algorithm
  - Apply() executor with delete-before-add ordering and no-short-circuit guarantee
affects:
  - 05-05-PLAN.md (sync HTTP handler that wires DiffRecords + Apply into POST /v1/zones/:zoneID/sync)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "RecordKey composite key pattern: (Type, Name, Content) allows multi-value same-name records; SRV extends key with Priority+Weight+Port"
    - "recordsEqual excludes key fields and ID from comparison — key fields already matched, ID intentionally differs between current and desired"
    - "matched-key tracking: both identical and update cases mark key as matched to prevent spurious deletes"
    - "Apply never short-circuits — all operations executed regardless of earlier errors (SYNC-04)"

key-files:
  created:
    - internal/reconcile/diff.go
    - internal/reconcile/diff_test.go
  modified: []

key-decisions:
  - "Package name is reconcile (not sync) — sync collides with Go stdlib sync package"
  - "RecordKey uses (Type, Name, Content) as base to support multi-value A records for same hostname; SRV adds Priority+Weight+Port for port/weight disambiguation"
  - "recordsEqual compares TTL, Priority, Weight, Port, Target, Dynamic — Content/Name/Type are in the key, ID intentionally differs between current (server-assigned) and desired (empty)"
  - "DiffRecords Update slice carries cur.ID into the desired record — browser UpdateRecord call requires the existing record ID"
  - "Apply delete-before-add order avoids transient conflicts when replacing records of same name"
  - "Apply uses make([]SyncResult, 0, ...) not nil — empty plan returns non-nil empty slice for safe range"

patterns-established:
  - "Pure-function diff: DiffRecords takes two slices and returns SyncPlan — no side effects, fully testable"
  - "TDD commit sequence: test (RED) → feat (GREEN) — no refactor needed as implementation was clean"

requirements-completed:
  - SYNC-01
  - SYNC-02
  - SYNC-03
  - SYNC-04
  - SYNC-05

# Metrics
duration: 2min
completed: 2026-02-28
---

# Phase 5 Plan 04: DNS Record Diff and Apply Engine Summary

**Pure-Go TDD diff engine in `internal/reconcile` with (Type,Name,Content)+SRV-port keying, ID carry-through on update, and no-short-circuit Apply — 13 tests, go vet clean**

## Performance

- **Duration:** 2 min
- **Started:** 2026-02-28T13:23:46Z
- **Completed:** 2026-02-28T13:25:58Z
- **Tasks:** 2 (RED + GREEN; no refactor needed)
- **Files modified:** 2

## Accomplishments

- Wrote 13 failing tests covering all DiffRecords and Apply behaviors (RED)
- Implemented `internal/reconcile/diff.go` making all 13 tests pass (GREEN)
- Multi-value same-name A records correctly tracked by (Type,Name,Content) key
- SRV records disambiguated with Priority+Weight+Port added to key
- Update operations carry current record ID for browser UpdateRecord call
- Apply never short-circuits on error, verifying SYNC-04 compliance
- `go vet ./internal/reconcile/...` clean, package name is `reconcile` (not `sync`)

## Task Commits

1. **RED: Failing tests** - `83ddc5c` (test)
2. **GREEN: Implementation** - `776cc2b` (feat)

## Files Created/Modified

- `internal/reconcile/diff_test.go` - 13 table-driven tests for DiffRecords and Apply
- `internal/reconcile/diff.go` - RecordKey, SyncPlan, SyncResult, SyncResponse, DiffRecords(), Apply(), keyOf(), recordsEqual(), opResult()

## Decisions Made

- Package name is `reconcile` — `sync` would collide with Go stdlib
- RecordKey base key is (Type, Name, Content) to track multi-value round-robin records independently; SRV extends key with Priority+Weight+Port
- `recordsEqual` compares only TTL, Priority, Weight, Port, Target, Dynamic — excludes key fields and ID
- DiffRecords carries `cur.ID` into the desired record in Update slice — browser UpdateRecord requires the server-assigned ID
- Apply processes delete → update → add without short-circuiting (SYNC-04)
- `make([]SyncResult, 0, ...)` guarantees non-nil empty slice for callers ranging over results

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `internal/reconcile` package is ready for Plan 05 to wire into the sync HTTP handler
- `DiffRecords(current, desired []model.Record) SyncPlan` and `Apply(ctx, zoneID, plan, deleteFn, updateFn, createFn) []SyncResult` are the exported API Plan 05 will call

## Self-Check: PASSED

- internal/reconcile/diff.go: FOUND
- internal/reconcile/diff_test.go: FOUND
- .planning/phases/05-observability-sync-engine/05-04-SUMMARY.md: FOUND
- Commit 83ddc5c (RED): FOUND
- Commit 776cc2b (GREEN): FOUND
- All 13 tests pass: VERIFIED

---
*Phase: 05-observability-sync-engine*
*Completed: 2026-02-28*

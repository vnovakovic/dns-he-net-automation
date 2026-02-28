---
phase: 03-dns-operations
plan: 03
subsystem: api
tags: [validation, query-filtering, makefile, cross-compilation, slog, rest-api]

requires:
  - phase: 03-dns-operations
    plan: 02
    provides: CreateRecord, UpdateRecord, ListRecords handlers; validateRecordFields helper; v1RecordTypes map; response.WriteError pattern

provides:
  - validate.ValidateRecord(rec model.Record) error — full field validation enforcing TTL allowlist, IP format, type-specific constraints for all 8 v1 types
  - internal/api/validate/records_test.go — 41 table-driven test cases covering all types and failure modes
  - CreateRecord and UpdateRecord return HTTP 422 with descriptive message before any browser operation when ValidateRecord fails
  - ListRecords ?type= and ?name= query filters applied post-scrape (API-06)
  - response.WriteJSON(w, status, v) helper for all non-error JSON responses (API-02)
  - Makefile build-linux: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 cross-compilation (COMPAT-03)

affects:
  - 04-vault-integration (record handlers follow same validation-before-browser pattern)

tech-stack:
  added: []
  patterns:
    - "ValidateRecord applies rules in order: type check -> name check -> TTL allowlist -> type-specific field rules"
    - "allowedTTLs map[int]bool mirrors the dns.he.net TTL select element values exactly"
    - "A record: net.ParseIP + ip.To4() != nil; AAAA: net.ParseIP + ip.To4() == nil (rejects IPv4-mapped)"
    - "ListRecords query filters use records[:0] reslice trick to avoid allocation while filtering in-place"
    - "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 verified — modernc.org/sqlite is pure Go, no CGO needed"
    - "response.WriteJSON centralises Content-Type header, WriteHeader, and json.Encode for success paths"

key-files:
  created:
    - internal/api/validate/records.go
    - internal/api/validate/records_test.go
    - Makefile
  modified:
    - internal/api/handlers/records.go
    - internal/api/response/errors.go

key-decisions:
  - "validate package is self-contained: v1Types map replicated from handlers to avoid circular import and keep validation independent"
  - "validateRecordFields (handler-level) kept as fast pre-filter for presence checks; ValidateRecord (validate package) is authoritative — both checks are fine"
  - "?type filter uses strings.ToUpper for case-insensitive matching (Terraform provider may send lowercase type names)"
  - "?name filter uses strings.EqualFold for case-insensitive exact match (DNS names are case-insensitive)"
  - "WriteJSON added to response package (not handlers) to follow existing pattern of centralised response helpers"
  - "Makefile uses tab indentation (required by make) — not spaces"

metrics:
  duration: 3min
  completed: 2026-02-28T08:53:49Z
  tasks: 2
  files_modified: 5
---

# Phase 3 Plan 03: Field Validation, Query Filters, WriteJSON, and Makefile Summary

**ValidateRecord function enforcing TTL allowlist + IP format + type-specific constraints for all 8 v1 types, wired into CreateRecord/UpdateRecord; ListRecords ?type/?name query filters; response.WriteJSON helper; Makefile with CGO_ENABLED=0 Linux cross-compilation target**

## Performance

- **Duration:** 3 min
- **Started:** 2026-02-28T08:50:25Z
- **Completed:** 2026-02-28T08:53:49Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- New package `internal/api/validate` with `ValidateRecord` enforcing all field constraints in order: type subset check, name non-empty + max 253 chars, TTL allowlist ({300,900,1800,3600,7200,14400,28800,43200,86400,172800}), then type-specific rules
- A: net.ParseIP + To4() check; AAAA: net.ParseIP + rejects IPv4-mapped addresses; MX: priority 1-65535 + non-empty content; SRV: priority 1-65535, weight 0-65535, port 1-65535, non-empty target; CAA: non-empty content with at least 3 space-separated tokens (flags tag value); CNAME/NS/TXT: non-empty content
- 41 table-driven test cases in records_test.go covering happy paths and all failure modes for all 8 types; all pass
- CreateRecord and UpdateRecord call validate.ValidateRecord before sm.WithAccount — invalid records return 422 without consuming a browser session slot
- ListRecords applies ?type (case-insensitive ToUpper) and ?name (EqualFold) query filters post-scrape; slog entry includes filter_type and filter_name fields
- response.WriteJSON(w, status, v) helper added; all five handler success paths migrated to use it
- Makefile with .PHONY build, build-linux (CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/server-linux ./cmd/server), test, and vet targets
- Cross-compilation CGO_ENABLED=0 GOOS=linux GOARCH=amd64 verified — passes with zero errors (modernc.org/sqlite is pure Go)

## Task Commits

1. **Task 1: ValidateRecord function with 41 table-driven tests** - `fde8730` (feat)
2. **Task 2: Wire validation into handlers, add query filters, WriteJSON helper, and Makefile** - `1a29247` (feat)

## Files Created/Modified

- `internal/api/validate/records.go` - ValidateRecord with all type-specific rules; allowedTTLs and v1Types maps
- `internal/api/validate/records_test.go` - 41 table-driven test cases (package validate_test)
- `internal/api/handlers/records.go` - validate package import; ValidateRecord wired in CreateRecord and UpdateRecord; ?type/?name filters in ListRecords; all success responses use response.WriteJSON
- `internal/api/response/errors.go` - WriteJSON(w, status, v) helper added after WriteError
- `Makefile` - build, build-linux, test, vet targets

## Decisions Made

- **validate package is self-contained:** v1Types map replicated from handlers rather than imported, avoiding circular imports and keeping validation independent of handler layer.
- **validateRecordFields kept as fast pre-filter:** The existing handler-level presence checks are kept alongside ValidateRecord. Both are fine — ValidateRecord is authoritative for all field constraints.
- **?type filter uses strings.ToUpper:** Ensures case-insensitive matching for Terraform provider query patterns that may send lowercase type names.
- **?name filter uses strings.EqualFold:** DNS names are case-insensitive; EqualFold match is correct and avoids false negatives.
- **WriteJSON in response package:** Follows existing pattern of centralised response helpers alongside WriteError.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None.

## Next Phase Readiness

- Phase 3 complete — DNS record CRUD with full field validation, query filtering, and cross-compilation verified
- Phase 4 (Vault integration) can proceed — same credential/session pattern in place; ValidateRecord is already the authoritative guard before browser ops

---
*Phase: 03-dns-operations*
*Completed: 2026-02-28*

## Self-Check: PASSED

**Files verified:**
- FOUND: internal/api/validate/records.go
- FOUND: internal/api/validate/records_test.go
- FOUND: internal/api/handlers/records.go
- FOUND: internal/api/response/errors.go
- FOUND: Makefile
- FOUND: .planning/phases/03-dns-operations/03-03-SUMMARY.md

**Commits verified:**
- FOUND: fde8730 (feat(03-03): add ValidateRecord function with 41 table-driven tests)
- FOUND: 1a29247 (feat(03-03): wire ValidateRecord, add query filters, WriteJSON helper, and Makefile)

**Test results:** 41/41 validate tests PASS, handler tests PASS (cached), all other packages PASS
**Build:** go build ./... zero errors
**Vet:** go vet ./... zero warnings
**Cross-compile:** CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/server SUCCESS

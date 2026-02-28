---
phase: 06-bind-import-export-admin-ui
plan: "01"
subsystem: bind-io
tags: [bind, export, import, miekg, dns, rest-api]
dependency_graph:
  requires: [05-05]
  provides: [bindio-package, export-endpoint, import-endpoint]
  affects: [internal/api/router.go, go.mod]
tech_stack:
  added: [github.com/miekg/dns v1.1.72]
  patterns: [pure-data-package, additive-sync, text-plain-attachment]
key_files:
  created:
    - internal/bindio/export.go
    - internal/bindio/import.go
    - internal/api/handlers/bind.go
  modified:
    - go.mod
    - go.sum
    - internal/api/router.go (export/import routes — wired in 06-02 sibling commit)
decisions:
  - "dns.CNAME struct in miekg/dns v1.1.x uses Target field (not Cname) — plan used wrong field name, auto-fixed"
  - "CNAME Target normalization: strip trailing dot from miekg Target to match HE storage format"
  - "Single browser session for zone name + records in ExportZone and ImportZone — avoids double queue acquisition"
  - "additive-only import: plan.Delete = nil before reconcile.Apply — records absent from file are never deleted"
  - "a-h/templ dependency added to fix go build ./... (admin/templates generated files blocked build; already in go.mod from 06-02)"
metrics:
  duration_minutes: 7
  completed_date: "2026-02-28"
  tasks_completed: 2
  files_created: 3
  files_modified: 3
---

# Phase 6 Plan 01: BIND Import/Export Summary

**One-liner:** BIND zone file export (text/plain via miekg/dns) and additive-only import (plan.Delete=nil) as REST endpoints under /zones/{zoneID}/export and /import.

## What Was Built

### Task 1: internal/bindio package

Created `internal/bindio/export.go` and `internal/bindio/import.go` as a pure data-conversion package.

**export.go — ExportZone:**
- Converts `[]model.Record` to BIND zone file string using miekg/dns
- Produces `$ORIGIN <zone>. ` and `$TTL <n>` headers
- Filters HE's own NS records (content suffix `.he.net`) per RESEARCH.md Pitfall 7
- Silently skips unsupported types
- CAA content split into Flag/Tag/Value fields (miekg/dns struct requirement)
- CNAME/MX targets FQDN-normalized via `dns.Fqdn()`

**import.go — ParseZoneFile:**
- Parses BIND zone file via `dns.NewZoneParser` with `dns.Fqdn(origin)` as origin
- SOA always skipped (HE-managed)
- Unsupported types collected in `[]SkippedRecord` (informational, not errors)
- Name normalization: strip trailing dot + zone origin suffix for HE-compatible relative names
- Apex records (name == origin) mapped to `"@"`

Added `github.com/miekg/dns v1.1.72` to go.mod.

**Commit:** `ee1d2ec`

### Task 2: HTTP handlers and route wiring

Created `internal/api/handlers/bind.go`:

**ExportZone (GET /api/v1/zones/{zoneID}/export):**
- Single browser session acquires zone name + records (avoids two queue acquisitions)
- Calls `bindio.ExportZone(records, zoneName)`
- Returns `Content-Type: text/plain; charset=utf-8` with `Content-Disposition: attachment; filename="{zone}.zone"`
- Requires admin role

**ImportZone (POST /api/v1/zones/{zoneID}/import):**
- Reads body (max 1 MB via `io.LimitReader`)
- Single browser session acquires zone name + current records
- Calls `bindio.ParseZoneFile(body, zoneName)` — skips unsupported types
- Diffs via `reconcile.DiffRecords`, then clears `plan.Delete = nil` (additive-only)
- `?dry_run=true` returns plan without mutating dns.he.net
- Applies via `reconcile.Apply` with update/create closures
- Writes audit log (action="import", resource="zone:<zoneID>")
- Response: `{ dry_run, applied, skipped, had_errors }`
- Requires admin role

Routes registered in `internal/api/router.go` under `/{zoneID}` sub-route alongside `/sync`.

Updated shared API documentation at `C:\Users\vladimir\Documents\Development\shared\APIs\DNS-HE-NET-AUTOMATION-APIS.md`.

**Commit:** `bd725df`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed incorrect CNAME field name in miekg/dns**
- **Found during:** Task 1 — `go build` failed
- **Issue:** Plan code used `dns.CNAME{..., Cname: ...}` and `r.Cname` but miekg/dns v1.1.x uses `Target` field for the canonical name in `dns.CNAME` struct
- **Fix:** Changed `Cname:` to `Target:` in export.go; changed `r.Cname` to `r.Target` in import.go; added explanatory comment
- **Files modified:** `internal/bindio/export.go`, `internal/bindio/import.go`
- **Commit:** `ee1d2ec`

**2. [Rule 3 - Blocking] Added github.com/a-h/templ dependency**
- **Found during:** Task 2 — `go build ./...` failed because `internal/api/admin/templates/*_templ.go` (committed by 06-02 sibling) imported `github.com/a-h/templ` not yet in go.mod
- **Fix:** `go get github.com/a-h/templ@v0.3.1001` + `go mod tidy`; dependency was already present in go.mod from 06-02 commit (the go get was a no-op)
- **Note:** 06-02 had already committed the templ dependency; `go build ./...` was passing by the time task 2 commit was made
- **Files modified:** go.mod, go.sum (already in HEAD)

**3. [Scope observation] ExportZone and ImportZone routes already in router.go**
- The 06-02 commit (`f0a4be2`) included the export/import route registrations in router.go
- My edit produced the same content — the routes were idempotently already present
- No conflict or rework needed

## Self-Check: PASSED

Files exist:
- `internal/bindio/export.go` — FOUND
- `internal/bindio/import.go` — FOUND
- `internal/api/handlers/bind.go` — FOUND

Commits exist:
- `ee1d2ec` — FOUND (bindio package)
- `bd725df` — FOUND (handlers + routes)

Build verification:
- `go build ./...` — PASSED
- `go vet ./...` — PASSED
- `grep ExportZone internal/api/router.go` — FOUND (line 133)
- `grep "plan.Delete = nil" bind.go` — FOUND (line 206)
- `grep "miekg/dns" go.mod` — FOUND (v1.1.72)

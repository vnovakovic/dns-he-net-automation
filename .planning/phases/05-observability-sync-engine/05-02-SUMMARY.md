---
phase: 05-observability-sync-engine
plan: "02"
subsystem: observability
tags: [prometheus, metrics, middleware, chi, session-manager, instrumentation, go]

# Dependency graph
requires:
  - phase: 05-01
    provides: "Registry struct with 8 exported metric vars; NewRegistry(); Handler()"
provides:
  - "GET /metrics endpoint mounted at root chi router (unauthenticated, OBS-01)"
  - "PrometheusMiddleware using chi RoutePattern() labels — no cardinality explosion"
  - "SessionManager.WithAccount() instrumented with QueueDepth, BrowserOpsTotal, BrowserOpDuration"
  - "createBrowserSession increments ActiveSessions; closeBrowserContext decrements"
  - "metrics.NewRegistry() created in main.go and passed to both SessionManager and Router"
affects:
  - 05-03-plan (audit handler uses same SessionManager — WithAccount signature updated)
  - 05-05-plan (integration/wiring complete for all OBS-01 metrics)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "PrometheusMiddleware uses chi.RouteContext(r.Context()).RoutePattern() — never r.URL.Path (cardinality anti-pattern)"
    - "Nil guard on all sm.metrics calls — pass nil registry for unit tests"
    - "opType string parameter on WithAccount — fine-grained label per operation (list_zones, create_record, etc.)"
    - "Queue depth: Inc before waiting, Dec on acquire OR timeout OR cancel — exact accounting"
    - "ActiveSessions: Inc in createBrowserSession after successful login; Dec in closeBrowserContext when ctx!=nil"

key-files:
  created: []
  modified:
    - "internal/api/router.go — PrometheusMiddleware func, /metrics route, *metrics.Registry param"
    - "internal/browser/session.go — metrics field, NewSessionManager reg param, WithAccount opType + instrumentation"
    - "internal/browser/session_test.go — nil reg arg, opType args to all WithAccount calls"
    - "internal/api/handlers/zones.go — opType args: list_zones, create_zone, delete_zone"
    - "internal/api/handlers/records.go — opType args: list_records, find_record, create_record, update_record, delete_record"
    - "cmd/server/main.go — metrics import, metrics.NewRegistry(), reg passed to NewSessionManager and NewRouter"

key-decisions:
  - "opType on WithAccount — fine-grained operation labels (not generic 'browser_op') for actionable dashboards"
  - "PrometheusMiddleware applied after panic recovery — panics still captured before metrics record"
  - "QueueDepth Dec on all three exit paths (acquired/timeout/cancel) — prevents gauge drift"
  - "/metrics at root level (next to /healthz), never inside /api/v1 BearerAuth group"
  - "reg parameter appended (not prepended) to NewRouter and NewSessionManager — minimizes diff, preserves existing arg order"

requirements-completed: [OBS-01]

# Metrics
duration: 5min
completed: 2026-02-28
---

# Phase 5 Plan 02: HTTP Middleware Instrumentation Summary

**PrometheusMiddleware wired into chi router using RoutePattern() labels; SessionManager.WithAccount() instrumented with opType, QueueDepth, BrowserOpsTotal, BrowserOpDuration, and ActiveSessions gauges**

## Performance

- **Duration:** 5 min
- **Started:** 2026-02-28T13:30:38Z
- **Completed:** 2026-02-28T13:35:40Z
- **Tasks:** 3
- **Files modified:** 6

## Accomplishments

- Added `PrometheusMiddleware` to `router.go` — uses `chi.RouteContext(r.Context()).RoutePattern()` labels to prevent cardinality explosion; nil-guard for tests
- Mounted `GET /metrics` at root chi router level (outside `/api/v1` BearerAuth group) via `reg.Handler().ServeHTTP`
- Updated `NewRouter` signature to accept `*metrics.Registry` as final parameter
- Added `metrics *metrics.Registry` field to `SessionManager` struct with nil guard on all calls
- Added `reg *metrics.Registry` as final parameter to `NewSessionManager`
- Added `opType string` parameter to `WithAccount` — enables fine-grained Prometheus labels per operation type
- QueueDepth gauge: Inc before goroutine starts, Dec on all three exit paths (acquired, timeout, context cancel)
- BrowserOpsTotal + BrowserOpDuration: measured AFTER `op()` returns (actual operation time, not setup time)
- ActiveSessions: Inc in `createBrowserSession` after successful login; Dec in `closeBrowserContext` when ctx is non-nil
- Updated all 8 `WithAccount` callers in handlers (list_zones, create_zone, delete_zone, list_records, find_record, create_record, update_record, delete_record)
- Updated `session_test.go` to pass nil registry and `"test_op"` opType string
- Wired `metrics.NewRegistry()` in `main.go` and passed to both `NewSessionManager` and `NewRouter`

## Task Commits

Each task was committed atomically:

1. **Task 1: Add PrometheusMiddleware and /metrics route to router** - `6973395` (feat)
2. **Task 2: Instrument SessionManager browser operations and lifecycle** - `f6bfc76` (feat)
3. **Task 3: Wire metrics registry in main.go** - `99e0f03` (feat)

## Files Created/Modified

- `internal/api/router.go` — `PrometheusMiddleware` func using RoutePattern(); `/metrics` route; `*metrics.Registry` param on `NewRouter`
- `internal/browser/session.go` — `metrics` field; `NewSessionManager` reg param; `WithAccount` opType + all metric instrumentation
- `internal/browser/session_test.go` — nil reg arg; `"test_op"` opType in all 4 `WithAccount` calls
- `internal/api/handlers/zones.go` — `"list_zones"`, `"create_zone"`, `"delete_zone"` opType args
- `internal/api/handlers/records.go` — `"list_records"`, `"find_record"`, `"create_record"`, `"update_record"`, `"delete_record"` opType args
- `cmd/server/main.go` — `metrics` import; `metrics.NewRegistry()`; reg passed to both constructors

## Decisions Made

- Fine-grained opType labels per handler operation (not one generic label) — enables per-operation dashboards
- PrometheusMiddleware applied after panic recovery middleware — panics still counted before metrics
- QueueDepth Dec on all three mutex-acquisition exit paths — prevents permanent gauge inflation
- `/metrics` registered at root level adjacent to `/healthz`, never inside BearerAuth group
- `*metrics.Registry` appended (not prepended) to both function signatures — preserves existing arg order, minimizes diff

## Deviations from Plan

None — plan executed exactly as written.

The only minor implementation detail: `r.URL.Path` mention in the `PrometheusMiddleware` comment (anti-pattern warning) caused a superficial grep match; the actual implementation exclusively uses `RoutePattern()`.

## Issues Encountered

None. All 3 tasks compiled clean on first attempt. All existing tests pass without modification to test logic (only signature updates).

## User Setup Required

None.

## Next Phase Readiness

- GET /metrics will return live Prometheus data after service startup
- All browser operations are now labelled with fine-grained opType strings
- `go build ./...`, `go vet ./...`, `go test ./...` all pass clean
- No blockers for 05-03 or 05-05

## Self-Check: PASSED

- FOUND: `internal/api/router.go` (PrometheusMiddleware + /metrics route)
- FOUND: commit `6973395` (Task 1)
- FOUND: commit `f6bfc76` (Task 2)
- FOUND: commit `99e0f03` (Task 3)
- Build passes: `go build ./...`
- Vet passes: `go vet ./...`
- Tests pass: `go test ./...` (all packages)
- No `r.URL.Path` in actual code (only in comment)
- RoutePattern() used in PrometheusMiddleware implementation
- ActiveSessions tracked in createBrowserSession + closeBrowserContext
- QueueDepth Dec on all 3 exit paths

---
*Phase: 05-observability-sync-engine*
*Completed: 2026-02-28*

---
phase: 04-production-hardening
plan: "02"
subsystem: resilience
tags: [retry, circuit-breaker, rate-limiting, gobreaker, httprate, go-retry, jitter]

# Dependency graph
requires:
  - phase: 01-foundation-browser-core
    provides: SessionManager, ErrSessionUnhealthy, ErrQueueTimeout
  - phase: 02-api-auth
    provides: BearerAuth middleware, response.WriteError
provides:
  - WithRetry wrapper for browser operations with exponential backoff and jitter
  - BreakerRegistry for per-account circuit breakers (thread-safe)
  - PerTokenRateLimit and GlobalRateLimit chi middleware
  - SessionManager.WithAccount with random jitter inter-operation delay
affects:
  - 04-production-hardening (plans 03+: handlers that call browser ops should wrap with WithRetry+Execute)
  - 05-observability (metrics for retry counts, circuit breaker state changes)

# Tech tracking
tech-stack:
  added:
    - github.com/sony/gobreaker/v2 v2.4.0 (circuit breaker)
    - github.com/go-chi/httprate v0.15.0 (HTTP rate limiting)
    - github.com/sethvargo/go-retry v0.3.0 promoted to direct (was indirect)
  patterns:
    - Double-checked locking for per-key registry (BreakerRegistry, SessionManager)
    - RetryableError wrapping pattern to distinguish transient from permanent errors
    - chi middleware factory functions returning func(http.Handler) http.Handler
    - math/rand jitter for rate limiting (not crypto/rand — rate limiting, not security)

key-files:
  created:
    - internal/resilience/retry.go
    - internal/resilience/circuitbreaker.go
    - internal/api/middleware/ratelimit.go
  modified:
    - internal/browser/session.go (maxOpDelay field + jitter in WithAccount)
    - internal/browser/session_test.go (maxOpDelay arg in newTestSessionManager)
    - cmd/server/main.go (maxOpDelay arg in NewSessionManager call)
    - go.mod / go.sum (new direct dependencies)

key-decisions:
  - "isTransientBrowserError classifies ErrSessionUnhealthy, DeadlineExceeded, timeout string, Target closed string — vault credential errors excluded (handled by stale cache)"
  - "BreakerRegistry.Execute returns wrapped 'circuit breaker open for account <id>' for ErrOpenState (not gobreaker error directly) — cleaner for handler layer mapping to 503"
  - "PerTokenRateLimit falls back to r.RemoteAddr when Authorization header has no Bearer token — allows middleware to be safe even if registered before BearerAuth"
  - "maxOpDelay added to Config and NewSessionManager; jitter range is [minOpDelay, maxOpDelay] using rand.Int63n — math/rand not crypto/rand, this is rate-limiting not security"

patterns-established:
  - "Resilience primitives in internal/resilience/ package — separate from browser and API packages"
  - "BreakerRegistry uses gobreaker.CircuitBreaker[error] generics (v2 API, not v1 interface{})"
  - "Chi middleware factories: func(requestsPerMin int) func(http.Handler) http.Handler signature"

requirements-completed: [RES-01, RES-02, RES-03, BROWSER-08]

# Metrics
duration: 4min
completed: 2026-02-28
---

# Phase 4 Plan 02: Resilience Layer Summary

**Exponential retry with jitter for browser ops, per-account circuit breakers via gobreaker v2, per-token and global chi rate limiting via httprate, and inter-operation jitter replacing fixed delay in SessionManager**

## Performance

- **Duration:** 4 min
- **Started:** 2026-02-28T11:57:42Z
- **Completed:** 2026-02-28T12:01:19Z
- **Tasks:** 2
- **Files modified:** 7 (3 created, 4 modified)

## Accomplishments

- WithRetry wraps any browser operation with 3-attempt exponential backoff (500ms base, 200ms jitter), retrying only transient errors (session unhealthy, deadline exceeded, timeout string, Target closed)
- BreakerRegistry provides thread-safe per-account circuit breakers: opens after 5 consecutive failures, stays open 30s, allows 1 probe in half-open; uses gobreaker v2 generics
- PerTokenRateLimit and GlobalRateLimit are drop-in chi middleware factories returning 429 JSON with Retry-After header
- SessionManager.WithAccount now applies random jitter between minOpDelay and maxOpDelay instead of fixed minOpDelay delay

## Task Commits

Each task was committed atomically:

1. **Task 1: Retry/backoff wrapper and per-account circuit breaker registry** - `cd08d7b` (feat)
2. **Task 2: Rate limiting middleware and inter-operation jitter in SessionManager** - `b494b91` (feat)

## Files Created/Modified

- `internal/resilience/retry.go` - WithRetry using sethvargo/go-retry with exponential backoff; isTransientBrowserError classifier
- `internal/resilience/circuitbreaker.go` - BreakerRegistry with per-account gobreaker.CircuitBreaker[error]; double-checked locking; state change slog.Warn
- `internal/api/middleware/ratelimit.go` - PerTokenRateLimit (httprate.Limit with bearer token key) and GlobalRateLimit (httprate.LimitAll)
- `internal/browser/session.go` - Added maxOpDelay field; replaced fixed minOpDelay delay with rand.Int63n jitter in WithAccount
- `internal/browser/session_test.go` - Added maxOpDelay=0 to newTestSessionManager call
- `cmd/server/main.go` - Added maxOpDelay from cfg.MaxOperationDelaySec to NewSessionManager call
- `go.mod` / `go.sum` - Added sony/gobreaker/v2, go-chi/httprate; promoted sethvargo/go-retry to direct

## Decisions Made

- isTransientBrowserError does not retry Vault credential errors — those are handled by the stale cache mechanism (research Pitfall 5), not retry loops
- BreakerRegistry.Execute wraps gobreaker.ErrOpenState into a descriptive message ("circuit breaker open for account X") rather than exposing library error directly; handler layer maps to 503
- PerTokenRateLimit falls back to RemoteAddr when no Bearer token present — safe to register even if placed before BearerAuth, though recommended after
- math/rand used for jitter (not crypto/rand) — jitter is for rate-limiting anti-fingerprinting, not cryptographic randomness

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. New env vars (`MAX_OPERATION_DELAY_SEC`, `RATE_LIMIT_PER_TOKEN_RPM`, `RATE_LIMIT_GLOBAL_RPM`, `CIRCUIT_BREAKER_MAX_FAILURES`, `CIRCUIT_BREAKER_TIMEOUT_SEC`) were added to Config in Plan 04-01 with production-safe defaults.

## Next Phase Readiness

- WithRetry and BreakerRegistry are ready for integration into handlers (Plan 04-03 should wire them into zone/record browser calls)
- Rate limiting middleware is ready to be registered in router.go (Plan 04-03 or 04-04)
- Full project builds and browser session tests pass

---
*Phase: 04-production-hardening*
*Completed: 2026-02-28*

## Self-Check: PASSED

- internal/resilience/retry.go: FOUND
- internal/resilience/circuitbreaker.go: FOUND
- internal/api/middleware/ratelimit.go: FOUND
- Commit cd08d7b (Task 1): FOUND
- Commit b494b91 (Task 2): FOUND

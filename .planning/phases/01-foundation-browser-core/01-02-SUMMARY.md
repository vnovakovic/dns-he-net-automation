---
phase: 01-foundation-browser-core
plan: 02
subsystem: browser-core
tags: [go, playwright-go, browser-context, session-manager, credential-interface, mutex, queue-timeout]

# Dependency graph
requires:
  - 01-01 (go module, config struct with all timeout/headless fields, main.go scaffold)
provides:
  - credential.Provider interface (GetCredential, ListAccountIDs) -- Phase 4 Vault seam
  - credential.EnvProvider parsing HE_ACCOUNTS JSON with validation and SEC-03 compliance
  - browser.Launcher wrapping playwright.Run() + Chromium.Launch() lifecycle
  - browser.NewAccountContext creating isolated BrowserContext per account
  - browser.SessionManager with per-account sync.Mutex serialization (REL-02)
  - WithAccount goroutine-based queue timeout returning ErrQueueTimeout (REL-03)
  - main.go fully wired: credentials, launcher, session manager, defer cleanup
affects:
  - 01-03-PLAN (page objects use WithAccount + ensureHealthy real login logic)
  - Phase 2 HTTP API (API handlers call sm.WithAccount for browser operations)
  - Phase 4 (credential.Provider interface is the Vault integration seam)

# Tech tracking
tech-stack:
  added:
    - github.com/playwright-community/playwright-go v0.5700.1 (added to go.mod)
  patterns:
    - "playwright.Run() then pw.Chromium.Launch() then defer browser.Close() + defer pw.Stop()"
    - "browser.NewContext() creates isolated per-account BrowserContext (independent cookies)"
    - "ctx.SetDefaultTimeout(ms) on BrowserContext sets default timeout for all Playwright actions"
    - "Goroutine-based mutex queue: goroutine locks + sends on acquired channel; caller selects
       acquired/timeout/cancel; on timeout, caller closes done channel, goroutine unlocks on acquire"
    - "compile-time interface check: var _ Provider = (*EnvProvider)(nil)"
    - "sort.Strings(ids) in ListAccountIDs for deterministic output"
    - "time.Duration(floatSec * float64(time.Second)) for converting float config to Duration"

key-files:
  created:
    - internal/credential/provider.go
    - internal/credential/env.go
    - internal/credential/env_test.go
    - internal/browser/errors.go
    - internal/browser/launcher.go
    - internal/browser/launcher_test.go
    - internal/browser/launcher_integration_test.go
    - internal/browser/session.go
    - internal/browser/session_test.go
  modified:
    - cmd/server/main.go (wired credential, launcher, session manager with defer cleanup)
    - go.mod (added playwright-go v0.5700.1)
    - go.sum (updated checksums)

key-decisions:
  - "Goroutine-based queue timeout (not TryLock): ensures no goroutine leak when queue times out -- goroutine sends on non-buffered channel or receives from done channel (closed on timeout/cancel), then unlocks"
  - "ensureHealthy is a STUB in 01-02: creates context+page if nil, nil-launcher safe for unit tests; real login logic deferred to 01-03 when page objects exist"
  - "Integration tests in separate file launcher_integration_test.go with //go:build integration tag -- unit tests never require Chromium"
  - "minOpDelay uses time.Duration(float64 * float64(time.Second)) because MinOperationDelaySec is float64 in config"
  - "playlist-go was listed in 01-01 SUMMARY as added but was NOT actually in go.mod -- added here as Rule 3 (blocking dependency for Task 2)"

patterns-established:
  - "Pattern: All browser operations go through sm.WithAccount(ctx, accountID, func(page)) -- never access session.page directly"
  - "Pattern: Credential lookup via credProvider.GetCredential(ctx, id) -- never access credentials directly from env"
  - "Pattern: defer launcher.Close() + defer sm.Close() in main.go ensures cleanup on SIGTERM/SIGINT"

requirements-completed: [BROWSER-01, BROWSER-03, BROWSER-04, BROWSER-06, REL-02, REL-03]

# Metrics
duration: 7min
completed: 2026-02-28
---

# Phase 1 Plan 02: Playwright Launcher and Session Manager Summary

**Playwright browser lifecycle, per-account BrowserContext isolation, goroutine-based mutex queue with configurable timeout, and credential provider interface backed by HE_ACCOUNTS JSON**

## Performance

- **Duration:** 7 min
- **Started:** 2026-02-27T23:01:09Z
- **Completed:** 2026-02-28T23:08:17Z
- **Tasks:** 2
- **Files modified:** 12

## Accomplishments

- `credential.Provider` interface defined with `GetCredential` and `ListAccountIDs` -- seam for Phase 4 Vault swap
- `EnvProvider` parses HE_ACCOUNTS JSON array, validates all fields (non-empty id/username/password, no duplicates), SEC-03 compliant (password never in error messages)
- `Launcher` wraps `playwright.Run()` + `pw.Chromium.Launch()`, `Close()` calls `browser.Close()` then `pw.Stop()` -- no orphaned processes
- `NewAccountContext(ms)` creates isolated `BrowserContext` with `SetDefaultTimeout` -- per-account cookie isolation
- `SessionManager.WithAccount()` uses goroutine-based queue timeout with correct lock handoff -- no goroutine leaks on timeout
- `ErrQueueTimeout` (HTTP 429) and `ErrSessionUnhealthy` (HTTP 503) sentinel errors defined
- `ensureHealthy` stub creates context+page when nil; real login logic deferred to Plan 01-03
- `main.go` wires credential provider, launcher, session manager; logs account IDs (never passwords); `defer launcher.Close()` + `defer sm.Close()` ensure clean shutdown on SIGTERM/SIGINT
- 19 tests pass: 11 credential validation tests + 8 browser/session tests

## Task Commits

Each task was committed atomically:

1. **Task 1: Credential provider interface and env implementation** - `d5f100d` (feat)
2. **Task 2: Playwright launcher, session manager, and main.go wiring** - `cbad0f5` (feat)

**Plan metadata:** (committed with SUMMARY.md, STATE.md, ROADMAP.md)

## Files Created/Modified

- `internal/credential/provider.go` - Provider interface + Credential struct
- `internal/credential/env.go` - EnvProvider: JSON parse, validation, sorted ListAccountIDs
- `internal/credential/env_test.go` - 11 tests: valid, invalid JSON, missing fields, duplicates, SEC-03 verification
- `internal/browser/errors.go` - ErrQueueTimeout + ErrSessionUnhealthy sentinel errors
- `internal/browser/launcher.go` - Launcher: NewLauncher, NewAccountContext, Close, IsConnected
- `internal/browser/launcher_test.go` - Unit tests: nil-state Close, nil-browser IsConnected
- `internal/browser/launcher_integration_test.go` - Integration test (build tag: integration) for real Chromium
- `internal/browser/session.go` - SessionManager: getOrCreateSession, WithAccount, ensureHealthy stub, Close
- `internal/browser/session_test.go` - 8 tests: same/different IDs, queue timeout, context cancel, serialization, error propagation
- `cmd/server/main.go` - Updated: wires credential, launcher, session manager with defer cleanup
- `go.mod` - Added playwright-go v0.5700.1 + transitive dependencies
- `go.sum` - Updated checksums

## Decisions Made

- Used goroutine-based queue timeout instead of `sync.Mutex.TryLock()`: goroutine locks mutex and tries to send on non-buffered `acquired` channel; if caller timed out (closes `done`), goroutine receives from done and unlocks immediately. This prevents goroutine leaks and abandoned locks regardless of timing.
- `ensureHealthy` is a deliberate stub in this plan: marks session healthy and logs warning when launcher is nil (allows unit tests without Chromium). Real login verification is Plan 01-03's responsibility.
- Integration tests in `launcher_integration_test.go` with `//go:build integration` tag -- avoids requiring Playwright in CI for unit test runs.
- `minOpDelay` conversion: `time.Duration(cfg.MinOperationDelaySec * float64(time.Second))` -- float64 config field requires explicit float multiplication before Duration cast.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added playwright-go v0.5700.1 to go.mod**
- **Found during:** Pre-execution check
- **Issue:** Plan noted "playwright-go already in go.mod" and 01-01 SUMMARY listed it as added, but go.mod inspection showed it was NOT present. Task 2 would fail to compile without it.
- **Fix:** Ran `go get github.com/playwright-community/playwright-go@v0.5700.1` before starting tasks
- **Files modified:** `go.mod`, `go.sum`
- **Committed in:** `d5f100d` (Task 1 commit)

**2. [Rule 3 - Blocking] Fixed misplaced //go:build directive in launcher_test.go**
- **Found during:** Task 2 (first test run)
- **Issue:** Initial launcher_test.go placed `//go:build integration` directive inside a function body at line 31. Go requires build directives to be at the top of the file before the package declaration.
- **Fix:** Split into two files: `launcher_test.go` (unit tests, no build tag) and `launcher_integration_test.go` (integration tests, `//go:build integration` at top)
- **Files modified:** `internal/browser/launcher_test.go`, `internal/browser/launcher_integration_test.go` (new)
- **Committed in:** `cbad0f5` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 3 blocking issues)
**Impact on plan:** No scope creep; both fixes required for compilation.

## Issues Encountered

- `go test -race` fails in Cygwin environment on Windows (CGo incompatibility with Cygwin GCC). This is an environment limitation, not a code issue. Standard `go test` without race detector passes all tests.

## User Setup Required

None - no external service configuration required for this plan. Playwright browser will be launched when `HE_ACCOUNTS` is set and the service starts.

## Next Phase Readiness

- `browser.Launcher` and `browser.SessionManager` are ready for Plan 01-03 to implement real login logic in `ensureHealthy()`
- `credential.Provider` interface is ready for use in Plan 01-03's `LoginPage` page object
- `WithAccount(ctx, accountID, func(page) error)` is the stable API that all future browser operations will use
- Ready for Plan 01-03: Login page object, ZoneList page object, health check with real re-login

---
*Phase: 01-foundation-browser-core*
*Completed: 2026-02-28*

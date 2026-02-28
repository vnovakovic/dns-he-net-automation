---
phase: 04-production-hardening
plan: "03"
subsystem: observability
tags: [screenshot, debug, crash-recovery, OBS-03, BROWSER-09, playwright]

# Dependency graph
requires:
  - phase: 01-foundation-browser-core
    provides: SessionManager, ensureHealthy, createBrowserSession, Launcher
  - phase: 04-production-hardening
    plan: "02"
    provides: maxOpDelay in NewSessionManager signature (prerequisite for this plan's parameter addition)
provides:
  - SaveDebugScreenshot helper (internal/browser/screenshot.go)
  - screenshotDir field and parameter in SessionManager/NewSessionManager
  - Screenshot capture on health-check failure (before teardown)
  - Screenshot capture on login failure (before context close)
  - slog.Error logging on fatal crash recovery failure (BROWSER-09)
affects:
  - 05-observability (screenshot directory monitoring, alerting on saved screenshots)
  - cmd/server/main.go (SCREENSHOT_DIR env var now wired through to SessionManager)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "No-op guard pattern: if dir == '' || page == nil { return } for optional side-effect helpers"
    - "Screenshot before teardown: capture before closeBrowserContext so page is still accessible"
    - "Screenshot failure does not mask original error: log Warn and return, original error propagates"

key-files:
  created:
    - internal/browser/screenshot.go
  modified:
    - internal/browser/session.go (screenshotDir field + param + 2 screenshot call sites + BROWSER-09 Error log)
    - internal/browser/session_test.go (screenshotDir="" added to newTestSessionManager)
    - cmd/server/main.go (cfg.ScreenshotDir passed to NewSessionManager)

key-decisions:
  - "Screenshot API in playwright-go v0.5700.1 uses variadic PageScreenshotOptions value (not pointer) — page.Screenshot(opts) not page.Screenshot(&opts)"
  - "screenshotDir added as final parameter to NewSessionManager — follows established pattern of adding new optional config to the end"
  - "SaveDebugScreenshot has no test file — filesystem side-effect helper; integration verified by session.go usage and build/vet"
  - "BROWSER-09 crash recovery logging added at slog.Error level with account ID — makes crash events distinguishable from routine session expiry"

requirements-completed: [OBS-03, BROWSER-09]

# Metrics
duration: 2min
completed: 2026-02-28
---

# Phase 4 Plan 03: Debug Screenshot Capture and Fatal Crash Recovery Summary

**Full-page PNG debug screenshots on browser failures via SaveDebugScreenshot, wired into SessionManager health-check and login failure paths, with BROWSER-09 Error-level logging on crash recovery failure**

## Performance

- **Duration:** 2 min
- **Started:** 2026-02-28T12:06:33Z
- **Completed:** 2026-02-28T12:08:56Z
- **Tasks:** 2
- **Files modified:** 4 (1 created, 3 modified)

## Accomplishments

- Created `internal/browser/screenshot.go` with `SaveDebugScreenshot` — captures full-page PNG to a configurable directory; no-op when dir is empty (OBS-03 default-off); screenshot failure is a Warn, never masks original error
- Added `screenshotDir` field and parameter to `SessionManager`/`NewSessionManager` as the final parameter
- Integrated screenshot capture at two failure points: health-check failure (Case 3 in `ensureHealthy`, before `closeBrowserContext`) and login failure (in `createBrowserSession`, before `browserCtx.Close()`)
- Added `slog.Error("session recovery failed after crash", "account", ..., "err", ...)` for BROWSER-09 fatal crash visibility
- Updated `session_test.go` and `cmd/server/main.go` for the new `screenshotDir` parameter

## Task Commits

Each task was committed atomically:

1. **Task 1: SaveDebugScreenshot helper** - `75f42af` (feat)
2. **Task 2: Integrate screenshot capture and fatal crash recovery into SessionManager** - `380f635` (feat)

## Files Created/Modified

- `internal/browser/screenshot.go` - SaveDebugScreenshot: no-op guard, MkdirAll(0750), timestamp filename, variadic PageScreenshotOptions, Warn on failure, Info on success
- `internal/browser/session.go` - screenshotDir field; screenshotDir param in NewSessionManager; SaveDebugScreenshot at health-check failure; SaveDebugScreenshot at login failure; slog.Error on recovery failure
- `internal/browser/session_test.go` - Added `""` as screenshotDir arg in newTestSessionManager call
- `cmd/server/main.go` - Passed `cfg.ScreenshotDir` as final arg to NewSessionManager

## Decisions Made

- playwright-go v0.5700.1 `Screenshot` method is variadic `PageScreenshotOptions` (value, not pointer) — compiler error caught this; fixed to `page.Screenshot(playwright.PageScreenshotOptions{...})`
- `screenshotDir` added as final parameter to preserve backward compatibility ordering with prior parameter additions
- No dedicated test file for `SaveDebugScreenshot` — it is a filesystem side-effect helper that would require complex mocking; build + vet + integration via session.go usage is sufficient

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Playwright Screenshot API takes value not pointer**
- **Found during:** Task 1 verification (go build)
- **Issue:** Plan specified `page.Screenshot(&playwright.PageScreenshotOptions{...})` but playwright-go v0.5700.1 declares `Screenshot(options ...PageScreenshotOptions)` (variadic value, not pointer)
- **Fix:** Changed to `page.Screenshot(playwright.PageScreenshotOptions{...})` (remove `&`)
- **Files modified:** `internal/browser/screenshot.go`
- **Commit:** `75f42af` (fixed before commit)

## Issues Encountered

None beyond the auto-fixed API signature mismatch above.

## User Setup Required

- Set `SCREENSHOT_DIR=/path/to/screenshots` environment variable to enable debug screenshots
- When empty (default), no screenshots are taken and no directory is created — completely inert
- Directory is created automatically by the service if it does not exist (MkdirAll 0750)

## Next Phase Readiness

- Screenshot infrastructure is ready; Plan 04-04 can wire WithRetry+BreakerRegistry into handlers
- SCREENSHOT_DIR is already in Config (added in Plan 04-01) with empty default; wiring to SessionManager is complete

---
*Phase: 04-production-hardening*
*Completed: 2026-02-28*

## Self-Check: PASSED

- internal/browser/screenshot.go: FOUND
- internal/browser/session.go: FOUND
- .planning/phases/04-production-hardening/04-03-SUMMARY.md: FOUND
- Commit 75f42af (Task 1): FOUND
- Commit 380f635 (Task 2): FOUND

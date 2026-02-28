---
phase: 04-production-hardening
plan: "04"
subsystem: integration
tags: [vault, circuit-breaker, retry, rate-limiting, docker, makefile, healthz, oci-labels]

# Dependency graph
requires:
  - phase: 04-production-hardening
    plan: "01"
    provides: VaultProvider, Config with 15 Phase 4 env vars
  - phase: 04-production-hardening
    plan: "02"
    provides: BreakerRegistry, WithRetry, GlobalRateLimit, PerTokenRateLimit middleware
  - phase: 04-production-hardening
    plan: "03"
    provides: screenshotDir in NewSessionManager (final param)

provides:
  - VaultProvider/EnvProvider selection in main.go (backward compatible)
  - vaultHealthFn closure passed to /healthz — reports "ok", "degraded: <reason>", or "disabled"
  - BreakerRegistry + WithRetry wrapping all browser-op handlers (zones, records)
  - GlobalRateLimit before BearerAuth; PerTokenRateLimit after BearerAuth in router
  - Dockerfile with OCI labels and non-root user (uid 1001)
  - Makefile targets: build-arm64, docker-build, docker-run, test-integration

affects:
  - 05-observability (circuit breaker state changes logged via slog.Warn, ready for metrics)
  - All zone/record browser operations now have retry + circuit breaker protection

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Type assertion credProvider.(*credential.VaultProvider) after cfg.VaultAddr != '' branch — safe because only VaultProvider is set in that branch"
    - "vaultHealthFn as closure injected at startup — avoids global state and enables clean testing"
    - "nil BreakerRegistry safe for unit tests — validation/auth early-exits are before breakers.Execute call"

key-files:
  created: []
  modified:
    - cmd/server/main.go
    - internal/api/router.go
    - internal/api/handlers/health.go
    - internal/api/handlers/zones.go
    - internal/api/handlers/records.go
    - internal/api/handlers/records_test.go
    - internal/api/handlers/zones_test.go
    - internal/credential/vault.go
    - Dockerfile
    - Makefile
    - C:\Users\vladimir\Documents\Development\shared\APIs\DNS-HE-NET-AUTOMATION-APIS.md

key-decisions:
  - "VaultProvider.Client() accessor added to vault.go — needed for vaultHealthFn closure in main.go; exposes underlying *api.Client safely"
  - "Type assertion credProvider.(*credential.VaultProvider) is safe — only reached when cfg.VaultAddr != '' branch sets VaultProvider"
  - "Dockerfile non-root USER server added after playwright install (requires root for apt-get and playwright install --with-deps)"
  - "Test signatures updated to (nil, nil, nil) for breakers param — unit tests exercise validation/auth paths that return before breakers.Execute"

# Metrics
duration: 7min
completed: 2026-02-28
---

# Phase 4 Plan 04: Integration Wiring Summary

**All Phase 4 components wired into the running service: VaultProvider/EnvProvider selection, per-account circuit breakers + retry on all browser ops, rate limiting in correct middleware order, Vault health in /healthz, Docker OCI labels + non-root user, and arm64 cross-compilation**

## Performance

- **Duration:** 7 min
- **Started:** 2026-02-28T12:12:31Z
- **Completed:** 2026-02-28T12:19:00Z
- **Tasks:** 2
- **Files modified:** 11 (+ 1 external shared API doc)

## Accomplishments

- `cmd/server/main.go`: credential provider selection — VaultProvider when `VAULT_ADDR` set, EnvProvider when `HE_ACCOUNTS` set; account ID logging only for EnvProvider; BreakerRegistry initialized from config; vaultHealthFn closure built at startup
- `internal/api/router.go`: updated `NewRouter` signature to accept `breakers`, `globalRPM`, `perTokenRPM`, `vaultHealthFn`; GlobalRateLimit registered before BearerAuth; PerTokenRateLimit registered after BearerAuth
- `internal/api/handlers/health.go`: `HealthHandler` now accepts `vaultHealthFn func() string`; adds `"vault"` key to checks map; "degraded: X" marks service degraded; "disabled" does not affect status
- `internal/api/handlers/zones.go` + `records.go`: all 8 browser-operation handlers (ListZones, CreateZone, DeleteZone, ListRecords, GetRecord, CreateRecord, UpdateRecord, DeleteRecord) wrap `sm.WithAccount` with `breakers.Execute(...) + resilience.WithRetry(...)`
- `internal/credential/vault.go`: added `Client() *api.Client` accessor
- `Dockerfile`: OCI standard labels (title, description, source); non-root user uid 1001; commented SCREENSHOT_DIR hint
- `Makefile`: added `build-arm64`, `docker-build`, `docker-run`, `test-integration` targets; updated `.PHONY`
- Shared API doc: vault key in /healthz response; 429 rate_limited in error codes; rate limiting section; changelog row

## Task Commits

Each task was committed atomically:

1. **Task 1: Wire credential provider selection, resilience, and rate limiting** - `1e9b5e0` (feat)
2. **Task 2: Docker image polish and Makefile targets for OPS-05** - `4f49ff9` (feat)

## Files Created/Modified

- `cmd/server/main.go` - VaultProvider/EnvProvider selection; BreakerRegistry init; vaultHealthFn closure; updated NewRouter call
- `internal/api/router.go` - New signature; GlobalRateLimit before auth; PerTokenRateLimit after auth; breakers passed to zone/record handlers; vaultHealthFn to HealthHandler
- `internal/api/handlers/health.go` - vaultHealthFn parameter; vault status in checks map; "disabled" does not degrade status
- `internal/api/handlers/zones.go` - All 3 handlers accept breakers; breakers.Execute + WithRetry wrapping
- `internal/api/handlers/records.go` - All 5 handlers accept breakers; breakers.Execute + WithRetry wrapping
- `internal/api/handlers/records_test.go` - Updated to (nil, nil, nil) for new breakers param
- `internal/api/handlers/zones_test.go` - Updated to (nil, nil, nil) for new breakers param
- `internal/credential/vault.go` - Added Client() *api.Client accessor
- `Dockerfile` - OCI labels; non-root user; SCREENSHOT_DIR comment
- `Makefile` - build-arm64, docker-build, docker-run, test-integration; updated .PHONY

## Decisions Made

- **VaultProvider.Client() accessor:** Added to `credential/vault.go` to expose the underlying `*api.Client` for the `vaultHealthFn` closure in `main.go`. The type assertion `credProvider.(*credential.VaultProvider)` is safe because it is only executed in the `cfg.VaultAddr != ""` branch which exclusively sets `VaultProvider`.
- **Dockerfile USER after playwright install:** The `useradd` and `USER server` instructions are placed after `playwright install --with-deps chromium` because that command requires root access (apt-get package installation). The server binary itself runs as UID 1001.
- **Test nil breakers:** Unit tests pass `nil` for the breakers parameter. This is safe because all test cases exercise validation and auth-check code paths that return before reaching `breakers.Execute`. No nil dereference occurs.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Handler test call sites broke after breakers parameter addition**
- **Found during:** Task 1 verification (`go test ./...`)
- **Issue:** `records_test.go` and `zones_test.go` called `ListZones(nil, nil)`, `CreateZone(nil, nil)`, etc. with the old two-argument signature. After adding `breakers *resilience.BreakerRegistry`, the tests would not compile.
- **Fix:** Updated all 10 call sites across both test files to pass `nil` as the third argument. Tests exercise pre-breaker code paths (validation, auth) so nil is safe.
- **Files modified:** `internal/api/handlers/records_test.go`, `internal/api/handlers/zones_test.go`
- **Commit:** `1e9b5e0` (fixed before commit)

**2. [Rule 2 - Missing critical functionality] VaultProvider.Client() accessor needed for vaultHealthFn**
- **Found during:** Task 1 implementation (main.go uses `vp.Client().Sys().Health()`)
- **Issue:** The plan's `vaultHealthFn` closure calls `vp.Client()`, but `VaultProvider.client` is an unexported field with no accessor method.
- **Fix:** Added `func (p *VaultProvider) Client() *api.Client` accessor to `vault.go`.
- **Files modified:** `internal/credential/vault.go`
- **Commit:** `1e9b5e0` (included in same commit)

## Issues Encountered

None beyond the two auto-fixed issues above.

## User Setup Required

None — all features are activated via environment variables:
- Set `VAULT_ADDR` to activate VaultProvider; otherwise EnvProvider (HE_ACCOUNTS) is used
- Rate limiting defaults: `RATE_LIMIT_GLOBAL_RPM=1000`, `RATE_LIMIT_PER_TOKEN_RPM=100`
- Circuit breaker defaults: `CIRCUIT_BREAKER_MAX_FAILURES=5`, `CIRCUIT_BREAKER_TIMEOUT_SEC=30`
- Dockerfile runs as non-root (UID 1001) — no mount permission changes needed for default config

---
*Phase: 04-production-hardening*
*Completed: 2026-02-28*

## Self-Check: PASSED

- cmd/server/main.go: FOUND
- internal/api/router.go: FOUND
- internal/api/handlers/health.go: FOUND
- internal/api/handlers/zones.go: FOUND
- internal/api/handlers/records.go: FOUND
- internal/credential/vault.go: FOUND
- Dockerfile: FOUND
- Makefile: FOUND
- .planning/phases/04-production-hardening/04-04-SUMMARY.md: FOUND
- Commit 1e9b5e0 (Task 1): FOUND
- Commit 4f49ff9 (Task 2): FOUND

---
phase: 01-foundation-browser-core
plan: 01
subsystem: database
tags: [go, sqlite, goose, playwright-go, modernc-sqlite, caarlos0-env, slog]

# Dependency graph
requires: []
provides:
  - Go module github.com/vnovakov/dns-he-net-automation with all Phase 1 dependencies pinned
  - Config struct loading from env vars via caarlos0/env v11 (12-factor, OPS-03)
  - Domain types: Account, Zone, Record with all 17 RecordType constants
  - SQLite store with WAL mode, busy_timeout=5000, foreign_keys=ON (REL-01)
  - Goose migration 001_init.sql creating accounts and schema_info tables (OPS-06)
  - Database file created with 0600 permissions (SEC-03)
  - main.go entry point with JSON slog, signal handling (SIGTERM/SIGINT), graceful shutdown
  - Multi-stage Dockerfile (modules, builder, ubuntu:noble runtime with Chromium)
affects:
  - 01-02-PLAN (browser launcher and session manager use Config and store.Open)
  - 01-03-PLAN (page objects and integration tests use store + config)
  - All subsequent phases (every plan builds on this foundation)

# Tech tracking
tech-stack:
  added:
    - github.com/playwright-community/playwright-go v0.5700.1
    - modernc.org/sqlite v1.46.1 (pure Go, no CGo)
    - github.com/pressly/goose/v3 v3.27.0
    - github.com/caarlos0/env/v11 v11.4.0
    - github.com/google/uuid v1.6.0
    - github.com/stretchr/testify v1.11.1
  patterns:
    - caarlos0/env v11 generics API: env.ParseAs[Config]() returns (Config, error)
    - modernc.org/sqlite driver name is "sqlite" (NOT "sqlite3")
    - goose Provider API with embed.FS (not deprecated global goose.Up() API)
    - goose dialect is DialectSQLite3 (SQL dialect name, separate from Go driver name)
    - SQLite pragmas via DSN: _pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)
    - WAL mode test requires temp file (not :memory: -- in-memory always uses "memory" journal)
    - slog.Level.UnmarshalText() for parsing log level strings

key-files:
  created:
    - go.mod
    - go.sum
    - internal/config/config.go
    - internal/config/config_test.go
    - internal/model/types.go
    - internal/store/sqlite.go
    - internal/store/sqlite_test.go
    - internal/store/migrations/001_init.sql
    - cmd/server/main.go
    - Dockerfile
    - .gitignore
  modified: []

key-decisions:
  - "Use required+notEmpty tags on HE_ACCOUNTS (required alone only checks existence, notEmpty catches empty string)"
  - "WAL mode cannot be verified with :memory: database (always returns 'memory' journal mode) -- use temp file for WAL test"
  - "DB file created with os.OpenFile before sql.Open to ensure 0600 permissions before SQLite touches the file"
  - "Dockerfile uses golang:1.25 base to match go.mod minimum version (upgraded by goose v3.27 requirement)"

patterns-established:
  - "Pattern: All env var config via Config struct with caarlos0/env v11 ParseAs generics"
  - "Pattern: SQLite always opened via store.Open() -- never sql.Open directly in application code"
  - "Pattern: Goose migrations embedded via //go:embed migrations/*.sql in store package"
  - "Pattern: main.go uses signal.NotifyContext for graceful shutdown, never os.Exit in goroutines"
  - "Pattern: HE_ACCOUNTS credential value NEVER logged (SEC-03) -- only log port, db_path, headless, slow_mo"

requirements-completed: [OPS-03, OPS-06, REL-01, SEC-03]

# Metrics
duration: 9min
completed: 2026-02-27
---

# Phase 1 Plan 01: Foundation Scaffolding Summary

**Go module with SQLite (WAL+goose), env-driven config (caarlos0/env v11), domain types (17 RecordTypes), and signal-handling main.go entrypoint for dns-he-net-automation**

## Performance

- **Duration:** 9 min
- **Started:** 2026-02-27T22:46:56Z
- **Completed:** 2026-02-27T22:56:00Z
- **Tasks:** 2
- **Files modified:** 11

## Accomplishments

- Go module initialized with all Phase 1 dependencies (playwright-go v0.5700.1, modernc.org/sqlite, goose v3, env/v11, uuid, testify)
- Config struct loading from env vars with correct defaults and required+notEmpty validation on HE_ACCOUNTS
- Domain types defined: Account, Zone, Record with all 17 HE.net RecordType constants (A through SRV)
- SQLite store opens with WAL mode, busy_timeout=5000, foreign_keys=ON; goose migrations create accounts + schema_info tables
- main.go starts with JSON slog, opens DB, handles SIGTERM/SIGINT cleanly -- smoke tested to verify shutdown log
- Multi-stage Dockerfile with playwright CLI extraction from go.mod version and `--with-deps chromium`

## Task Commits

Each task was committed atomically:

1. **Task 1: Go module, dependencies, project skeleton, config, and domain types** - `dad5f2a` (feat)
2. **Task 2: SQLite store with goose migrations and main.go entry point** - `cd09435` (feat)

**Plan metadata:** (committed with SUMMARY.md, STATE.md, ROADMAP.md)

## Files Created/Modified

- `go.mod` - Module definition with all Phase 1 dependencies, go 1.25
- `go.sum` - Dependency checksums
- `internal/config/config.go` - Config struct with env tags, Load() via env.ParseAs[Config]()
- `internal/config/config_test.go` - Tests: defaults, required field, custom values
- `internal/model/types.go` - Account, Zone, Record structs; all 17 RecordType constants
- `internal/store/sqlite.go` - store.Open(): file permissions, WAL DSN, goose Provider, migration run
- `internal/store/sqlite_test.go` - Tests: in-memory migrations, WAL mode (temp file), foreign keys, file creation
- `internal/store/migrations/001_init.sql` - Goose migration: accounts + schema_info tables
- `cmd/server/main.go` - Entry point: config load, slog setup, DB open, signal handling
- `Dockerfile` - 3-stage: modules cache, builder (binary + playwright CLI), ubuntu:noble runtime
- `.gitignore` - Exclude *.db files, .env files, build artifacts

## Decisions Made

- Used `required,notEmpty` tags on HE_ACCOUNTS (caarlos0/env v11 `required` only checks var existence; `notEmpty` also rejects empty string -- tests verified both behaviors)
- WAL mode test uses temp file database (`:memory:` always uses "memory" journal mode, not "wal" -- SQLite limitation)
- Database file created with `os.OpenFile(..., 0600)` before `sql.Open()` to ensure permissions are set before SQLite creates WAL/SHM sidecar files
- Upgraded go.mod to 1.25 (forced by goose v3.27.0 which requires go >= 1.25)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed `required` tag not catching empty HE_ACCOUNTS**
- **Found during:** Task 1 (config and config_test.go)
- **Issue:** caarlos0/env v11 `required` tag only checks that the env var exists (is set), not that it is non-empty. `t.Setenv("HE_ACCOUNTS", "")` sets the var to empty string which passes `required` but is invalid
- **Fix:** Changed tag from `env:"HE_ACCOUNTS,required"` to `env:"HE_ACCOUNTS,required,notEmpty"` -- notEmpty enforces non-empty value
- **Files modified:** `internal/config/config.go`
- **Verification:** `TestLoad_MissingRequired` passes -- Load() returns error on empty HE_ACCOUNTS
- **Committed in:** `dad5f2a` (Task 1 commit)

**2. [Rule 1 - Bug] Fixed WAL mode test using :memory: (always returns "memory" journal)**
- **Found during:** Task 2 (sqlite_test.go)
- **Issue:** `PRAGMA journal_mode` on an in-memory SQLite database always returns "memory", not "wal" -- WAL mode is not applicable to in-memory databases
- **Fix:** Changed `TestOpen_WALMode` to use a temp file database (`t.TempDir()`) instead of `:memory:`
- **Files modified:** `internal/store/sqlite_test.go`
- **Verification:** `TestOpen_WALMode` passes with "wal" result
- **Committed in:** `cd09435` (Task 2 commit)

**3. [Rule 2 - Missing Critical] Added .gitignore**
- **Found during:** Task 2 (post-smoke-test cleanup)
- **Issue:** Smoke test created `dns-he-net.db` in project root; no .gitignore to prevent accidental commit of database files or .env credential files
- **Fix:** Created `.gitignore` excluding *.db, *.db-wal, *.db-shm, .env, build artifacts
- **Files modified:** `.gitignore` (new)
- **Verification:** `git status` shows dns-he-net.db correctly excluded
- **Committed in:** `cd09435` (Task 2 commit)

---

**Total deviations:** 3 auto-fixed (2 Rule 1 bugs, 1 Rule 2 missing critical)
**Impact on plan:** All auto-fixes necessary for correctness and security. No scope creep.

## Issues Encountered

- modernc.org/sqlite v1.46.1 requires go >= 1.24.0; goose v3.27.0 requires go >= 1.25.0 -- go.mod was auto-upgraded to 1.25 during `go get`. This is expected behavior and does not affect the plan.

## User Setup Required

None - no external service configuration required for this plan.

## Next Phase Readiness

- Go foundation is complete: module, config, types, store all compilable and tested
- store.Open() can be called with any path (file or :memory:) from next plan's browser launcher
- Config struct has all fields browser plans will need (headless, slowMo, timeouts, session age)
- Ready for Plan 01-02: Playwright browser launcher and session manager

---
*Phase: 01-foundation-browser-core*
*Completed: 2026-02-27*

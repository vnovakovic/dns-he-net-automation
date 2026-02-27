# Phase 1: Foundation + Browser Core - Research

**Researched:** 2026-02-27
**Domain:** Go service scaffolding, playwright-go browser automation, SQLite migrations, session management
**Confidence:** HIGH

## Summary

Phase 1 builds the foundation of a Go service that automates dns.he.net via headless Chromium controlled by playwright-go. The core challenge is establishing a reliable browser automation engine with per-account session isolation, automatic session recovery, and a clean page-object abstraction layer. All CSS selectors for dns.he.net have already been empirically verified (see CONTEXT.md), so the research focus is on the correct Go module structure, playwright-go API patterns, SQLite schema with goose migrations, credential interface design, and Dockerfile patterns.

playwright-go v0.5700.1 is the current release (Feb 24, 2026). It is a thin Go client that delegates all browser automation to the upstream Node.js Playwright driver process via stdio. This means the Docker image needs Node.js runtime (~50MB) bundled with the Playwright driver, plus Chromium installed via `playwright install --with-deps`. The tradeoff vs Rod (pure Go, no Node.js) is accepted per CONTEXT.md decision -- playwright-go provides auto-waits, Playwright Inspector, trace viewer, and a larger community.

**Primary recommendation:** Use the standard Go `internal/` layout with packages for `browser/`, `config/`, `store/`, and `browser/pages/`. Use goose v3 Provider API with embedded SQL migrations. Design credentials behind a `CredentialProvider` interface from day one. Use one shared Playwright Browser instance with per-account BrowserContext for cookie isolation.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **Browser automation**: playwright-go (`github.com/playwright-community/playwright-go`) -- NOT go-rod/rod
- **Browser config**: `PLAYWRIGHT_HEADLESS=false` for dev, `PLAYWRIGHT_SLOW_MO` for debugging
- **Chromium**: downloaded via `playwright install` in Dockerfile build step (NOT vendored)
- **Credentials**: `HE_ACCOUNTS` JSON array env var with `id`, `username`, `password` fields
- **Concurrency**: per-account `sync.Mutex`, queue with `OPERATION_QUEUE_TIMEOUT_SEC` (default 60s) returning 429
- **Session recovery**: transparent retry, `OPERATION_TIMEOUT_SEC` configurable (default 30s)
- **Port**: `PORT` env var, default 8080
- **All config from env vars only** (12-factor, no config file)
- **Test zone**: `royalheadshots.online` (zone ID: 1294061) on production account `vnovakov`
- **Session expiry**: start with 30-minute proactive re-login threshold
- **Rate limiting**: start with 1.5s minimum delay between browser operations
- **All selectors verified live** (see CONTEXT.md `<specifics>` section)

### Claude's Discretion
- Go module path naming
- Internal package organization details
- SQLite schema design for Phase 1
- Credential interface shape
- Session health detection signals
- Goose migration file naming conventions

### Deferred Ideas (OUT OF SCOPE)
- Playwright Inspector integration into CI (Phase 4)
- BIND export curl command discovery (Phase 6)
- BIND export via "Raw Zone" expand panel (Phase 6)
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BROWSER-01 | Launch headless Chromium via browser automation with no orphaned processes | playwright-go lifecycle: `Run()` -> `Chromium.Launch()` -> `browser.Close()` -> `pw.Stop()` with defer chain; Playwright driver manages Chromium process lifecycle |
| BROWSER-02 | Log into dns.he.net using credentials fetched at runtime | Credential interface `CredentialProvider` with `EnvCredentialProvider` implementation; login page object uses verified selectors |
| BROWSER-03 | Browser sessions isolated per account (separate contexts, no cookie cross-contamination) | `browser.NewContext()` creates isolated BrowserContext per account with independent cookies/storage |
| BROWSER-04 | All operations for an account serialized via per-account mutex | `sync.Mutex` per account in SessionManager; queue with timeout returning 429 |
| BROWSER-05 | Detect stale/expired sessions and automatically re-authenticate | Session health check: navigate to zone list, verify presence of logout link vs login form redirect |
| BROWSER-06 | Configurable timeout for every browser operation (default 30s) | `context.WithTimeout` wrapping all operations + playwright-go `Timeout` option on all methods |
| BROWSER-07 | All selectors in `internal/browser/pages/` -- no selectors leak elsewhere | Page Object pattern: `LoginPage`, `ZoneListPage`, `RecordFormPage` structs in pages package |
| OPS-03 | Configuration from environment variables (12-factor) | `caarlos0/env` v11 for struct-based parsing with defaults |
| OPS-06 | SQLite schema managed by embedded SQL migrations via goose v3 | goose v3 Provider API with `embed.FS`, `DialectSQLite3`, modernc.org/sqlite driver |
| REL-01 | SQLite WAL mode with busy_timeout=5000 and foreign_keys=ON | DSN params: `?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)` |
| REL-02 | Chromium launched with process lifecycle management | Playwright manages Chromium process; `defer browser.Close()` + `defer pw.Stop()` ensures cleanup |
| REL-03 | Service can restart cleanly -- no persistent browser state required | BrowserContext is ephemeral; session re-created on demand after restart |
| SEC-03 | SQLite database file permissions set to 0600 | Set via `os.OpenFile` with mode 0600 before passing path to sql.Open, or via umask |
</phase_requirements>

## Standard Stack

### Core (Phase 1 only)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| playwright-go | v0.5700.1 | Headless Chromium automation | Locked decision. Auto-waits, Inspector, trace viewer, upstream Playwright ecosystem |
| modernc.org/sqlite | v1.46.1+ | SQLite driver (pure Go, CGo-free) | No CGo = clean cross-compilation, static binary, simpler Docker |
| pressly/goose | v3.27.0 | Database migrations | Provider API with embed.FS, context support, structured results |
| caarlos0/env | v11 | Env var parsing to structs | Zero dependencies, struct tags, defaults, validation |
| log/slog | stdlib | Structured JSON logging | Standard library since Go 1.21, no external dependency needed |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| google/uuid | v1.6+ | UUID generation | Account IDs, session tracking |
| stretchr/testify | v1.10+ | Test assertions | Cleaner test assertions than raw if/t.Error |

### NOT Needed in Phase 1

| Library | Why Deferred |
|---------|-------------|
| go-chi/chi | HTTP router -- Phase 2 (API Layer) |
| golang-jwt/jwt | JWT tokens -- Phase 2 (Authentication) |
| hashicorp/vault/api | Vault integration -- Phase 4 |
| a-h/templ, htmx | Admin UI -- Phase 6 |
| miekg/dns | BIND import/export -- Phase 6 |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| playwright-go | go-rod/rod | Rod is pure Go (no Node.js), but CONTEXT.md locked playwright-go |
| modernc.org/sqlite | mattn/go-sqlite3 | go-sqlite3 is CGo-based, faster but breaks cross-compilation |
| caarlos0/env | os.Getenv | Manual parsing is error-prone; env/v11 gives typed structs with defaults |
| goose v3 | golang-migrate | goose has better embed.FS integration and Provider pattern |

**Installation (Phase 1 only):**
```bash
go mod init github.com/vnovakov/dns-he-net-automation

# Core
go get github.com/playwright-community/playwright-go@v0.5700.1
go get modernc.org/sqlite@latest
go get github.com/pressly/goose/v3@latest
go get github.com/caarlos0/env/v11@latest
go get github.com/google/uuid@latest

# Test
go get -t github.com/stretchr/testify@latest

# Install Playwright browsers (run once, or in Dockerfile)
go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install --with-deps chromium
```

## Architecture Patterns

### Recommended Project Structure

```
dns-he-net-automation/
├── cmd/
│   └── server/
│       └── main.go              # Entry point: config, DB, browser init, signal handling
├── internal/
│   ├── config/
│   │   └── config.go            # Config struct with env tags, Load() function
│   ├── credential/
│   │   ├── provider.go          # CredentialProvider interface
│   │   └── env.go               # EnvCredentialProvider (Phase 1: HE_ACCOUNTS JSON)
│   ├── browser/
│   │   ├── launcher.go          # Playwright lifecycle: Run, Launch, Stop
│   │   ├── session.go           # SessionManager: per-account context, mutex, health
│   │   └── pages/
│   │       ├── login.go         # LoginPage page object
│   │       ├── zonelist.go      # ZoneListPage page object
│   │       └── recordform.go    # RecordFormPage page object
│   ├── store/
│   │   ├── sqlite.go            # Database init, connection, pragmas
│   │   └── migrations/
│   │       └── 001_init.sql     # Initial schema (goose format)
│   └── model/
│       └── types.go             # Shared domain types: Account, Zone, Record, Credential
├── go.mod
├── go.sum
└── Dockerfile
```

**Key principles:**
- `internal/` prevents external import of all application packages
- `internal/browser/pages/` is the ONLY package that knows CSS selectors (BROWSER-07)
- `internal/credential/` defines the interface now, env implementation now, Vault in Phase 4
- `internal/store/` owns all SQLite interaction
- `cmd/server/main.go` wires everything together

### Pattern 1: Playwright Lifecycle Management

**What:** Single Playwright instance, single Browser, per-account BrowserContext
**When to use:** Always -- this is the foundation pattern

```go
// Source: pkg.go.dev/github.com/playwright-community/playwright-go (verified Feb 2026)

// internal/browser/launcher.go
package browser

import (
    "fmt"
    "log/slog"

    "github.com/playwright-community/playwright-go"
)

type Launcher struct {
    pw      *playwright.Playwright
    browser playwright.Browser
    headless bool
    slowMo   float64
}

func NewLauncher(headless bool, slowMo float64) (*Launcher, error) {
    pw, err := playwright.Run()
    if err != nil {
        return nil, fmt.Errorf("start playwright: %w", err)
    }

    opts := playwright.BrowserTypeLaunchOptions{
        Headless: playwright.Bool(headless),
    }
    if slowMo > 0 {
        opts.SlowMo = playwright.Float(slowMo)
    }

    browser, err := pw.Chromium.Launch(opts)
    if err != nil {
        pw.Stop()
        return nil, fmt.Errorf("launch chromium: %w", err)
    }

    slog.Info("browser launched",
        "headless", headless,
        "slowMo", slowMo,
    )

    return &Launcher{pw: pw, browser: browser, headless: headless, slowMo: slowMo}, nil
}

// NewAccountContext creates an isolated BrowserContext for one HE account.
// Each context has independent cookies -- no cross-contamination.
func (l *Launcher) NewAccountContext() (playwright.BrowserContext, error) {
    ctx, err := l.browser.NewContext()
    if err != nil {
        return nil, fmt.Errorf("new browser context: %w", err)
    }
    // Set default timeout for all operations in this context
    ctx.SetDefaultTimeout(30000) // 30 seconds
    return ctx, nil
}

func (l *Launcher) Close() {
    if l.browser != nil {
        l.browser.Close()
    }
    if l.pw != nil {
        l.pw.Stop()
    }
}
```

### Pattern 2: Per-Account Session Manager with Mutex

**What:** Serialized access per account, queue with timeout, auto-recovery
**When to use:** All browser operations

```go
// internal/browser/session.go
package browser

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/playwright-community/playwright-go"
)

type AccountSession struct {
    mu        sync.Mutex
    ctx       playwright.BrowserContext
    page      playwright.Page
    accountID string
    lastLogin time.Time
    healthy   bool
}

type SessionManager struct {
    launcher    *Launcher
    sessions    map[string]*AccountSession
    sessionsMu  sync.RWMutex
    queueTimeout time.Duration // default 60s -> 429
    opTimeout    time.Duration // default 30s
    reloginAge   time.Duration // default 30min
}

// WithAccount acquires the per-account mutex, ensures a healthy session,
// and calls the operation function. Returns 429-eligible error if queue timeout.
func (sm *SessionManager) WithAccount(ctx context.Context, accountID string, op func(playwright.Page) error) error {
    session := sm.getOrCreateSession(accountID)

    // Queue with timeout (OPERATION_QUEUE_TIMEOUT_SEC)
    acquired := make(chan struct{})
    go func() {
        session.mu.Lock()
        close(acquired)
    }()

    select {
    case <-acquired:
        defer session.mu.Unlock()
    case <-time.After(sm.queueTimeout):
        return ErrQueueTimeout // caller maps to HTTP 429
    case <-ctx.Done():
        return ctx.Err()
    }

    // Operation timeout (OPERATION_TIMEOUT_SEC)
    opCtx, cancel := context.WithTimeout(ctx, sm.opTimeout)
    defer cancel()

    // Ensure session is healthy (re-login if stale)
    if err := sm.ensureHealthy(opCtx, session); err != nil {
        return fmt.Errorf("session recovery failed: %w", err)
    }

    return op(session.page)
}
```

### Pattern 3: Page Object with Verified Selectors

**What:** All dns.he.net selectors encapsulated in page object structs
**When to use:** Every browser interaction with dns.he.net

```go
// internal/browser/pages/login.go
package pages

import (
    "fmt"

    "github.com/playwright-community/playwright-go"
)

// Selectors verified against live dns.he.net on 2026-02-27
const (
    loginEmailInput    = "input[name=\"email\"]"
    loginPasswordInput = "input[name=\"pass\"]"
    loginSubmitButton  = "text=Login!"
    logoutLink         = "a[href=\"/?action=logout\"]"
)

type LoginPage struct {
    page playwright.Page
}

func NewLoginPage(page playwright.Page) *LoginPage {
    return &LoginPage{page: page}
}

func (lp *LoginPage) Login(username, password string) error {
    // Navigate to login page
    if _, err := lp.page.Goto("https://dns.he.net/"); err != nil {
        return fmt.Errorf("navigate to login: %w", err)
    }

    // Fill credentials (auto-waits for elements to be actionable)
    if err := lp.page.Locator(loginEmailInput).Fill(username); err != nil {
        return fmt.Errorf("fill email: %w", err)
    }
    if err := lp.page.Locator(loginPasswordInput).Fill(password); err != nil {
        return fmt.Errorf("fill password: %w", err)
    }

    // Click login and wait for navigation
    if err := lp.page.Locator(loginSubmitButton).Click(); err != nil {
        return fmt.Errorf("click login: %w", err)
    }

    // Wait for page to fully load after form POST redirect
    if err := lp.page.WaitForLoadState(playwright.LoadStateNetworkidle); err != nil {
        return fmt.Errorf("wait after login: %w", err)
    }

    // Verify login succeeded by checking for logout link
    visible, err := lp.page.Locator(logoutLink).IsVisible()
    if err != nil || !visible {
        return fmt.Errorf("login failed: logout link not visible after login")
    }

    return nil
}

// IsLoggedIn checks if the current session is authenticated.
func (lp *LoginPage) IsLoggedIn() (bool, error) {
    return lp.page.Locator(logoutLink).IsVisible()
}
```

### Pattern 4: Credential Provider Interface

**What:** Abstract credential retrieval to support env vars (Phase 1) and Vault (Phase 4)
**When to use:** Any code that needs HE account credentials

```go
// internal/credential/provider.go
package credential

import "context"

// Credential holds dns.he.net account credentials.
type Credential struct {
    AccountID string
    Username  string
    Password  string
}

// Provider abstracts credential retrieval.
// Phase 1: EnvProvider (reads HE_ACCOUNTS JSON env var)
// Phase 4: VaultProvider (reads from HashiCorp Vault KV v2)
type Provider interface {
    // GetCredential returns credentials for the given account ID.
    GetCredential(ctx context.Context, accountID string) (*Credential, error)

    // ListAccountIDs returns all known account IDs.
    ListAccountIDs(ctx context.Context) ([]string, error)
}
```

```go
// internal/credential/env.go
package credential

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
)

type envAccount struct {
    ID       string `json:"id"`
    Username string `json:"username"`
    Password string `json:"password"`
}

// EnvProvider reads credentials from HE_ACCOUNTS JSON env var.
type EnvProvider struct {
    accounts map[string]*Credential
}

func NewEnvProvider() (*EnvProvider, error) {
    raw := os.Getenv("HE_ACCOUNTS")
    if raw == "" {
        return nil, fmt.Errorf("HE_ACCOUNTS environment variable not set")
    }

    var accounts []envAccount
    if err := json.Unmarshal([]byte(raw), &accounts); err != nil {
        return nil, fmt.Errorf("parse HE_ACCOUNTS: %w", err)
    }

    m := make(map[string]*Credential, len(accounts))
    for _, a := range accounts {
        m[a.ID] = &Credential{
            AccountID: a.ID,
            Username:  a.Username,
            Password:  a.Password,
        }
    }

    return &EnvProvider{accounts: m}, nil
}

func (p *EnvProvider) GetCredential(_ context.Context, accountID string) (*Credential, error) {
    cred, ok := p.accounts[accountID]
    if !ok {
        return nil, fmt.Errorf("account %q not found in HE_ACCOUNTS", accountID)
    }
    return cred, nil
}

func (p *EnvProvider) ListAccountIDs(_ context.Context) ([]string, error) {
    ids := make([]string, 0, len(p.accounts))
    for id := range p.accounts {
        ids = append(ids, id)
    }
    return ids, nil
}
```

### Pattern 5: SQLite Initialization with Goose Provider

**What:** Database setup with WAL mode, goose migrations, embedded SQL files
**When to use:** Service startup

```go
// internal/store/sqlite.go
package store

import (
    "context"
    "database/sql"
    "embed"
    "fmt"
    "io/fs"
    "log/slog"
    "os"

    "github.com/pressly/goose/v3"
    _ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

func Open(dbPath string) (*sql.DB, error) {
    // Ensure file permissions (SEC-03)
    if dbPath != ":memory:" {
        if _, err := os.Stat(dbPath); os.IsNotExist(err) {
            f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
            if err != nil {
                return nil, fmt.Errorf("create db file: %w", err)
            }
            f.Close()
        }
    }

    // Open with pragmas (REL-01)
    dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", dbPath)
    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, fmt.Errorf("open sqlite: %w", err)
    }

    // Verify connection
    if err := db.PingContext(context.Background()); err != nil {
        db.Close()
        return nil, fmt.Errorf("ping sqlite: %w", err)
    }

    // Run migrations (OPS-06)
    migrationFS, err := fs.Sub(embedMigrations, "migrations")
    if err != nil {
        db.Close()
        return nil, fmt.Errorf("migration fs: %w", err)
    }

    provider, err := goose.NewProvider(
        goose.DialectSQLite3,
        db,
        migrationFS,
    )
    if err != nil {
        db.Close()
        return nil, fmt.Errorf("goose provider: %w", err)
    }

    results, err := provider.Up(context.Background())
    if err != nil {
        db.Close()
        return nil, fmt.Errorf("run migrations: %w", err)
    }
    for _, r := range results {
        slog.Info("migration applied", "path", r.Source.Path, "duration", r.Duration)
    }

    return db, nil
}
```

### Anti-Patterns to Avoid

- **Selectors outside `internal/browser/pages/`**: Violates BROWSER-07. Every CSS selector, form field name, and URL pattern MUST live in the pages package.
- **Sharing BrowserContext between accounts**: Causes cookie cross-contamination. One BrowserContext per account, always.
- **Using `time.Sleep()` for synchronization**: Use playwright-go's auto-wait (`WaitForLoadState`, `WaitForSelector`, Locator auto-retry). Sleep is both too slow and too fast.
- **Manual HTTP form POST instead of browser interaction**: dns.he.net forms include hidden fields (`account` hash, `hosted_dns_editzone`, etc.) that are set server-side. Always interact with the real page elements -- Fill + Click, never construct POST requests.
- **Multiple Playwright `Run()` calls**: One `Run()` per process. It spawns a Node.js driver. Multiple calls waste resources and may conflict.
- **Accessing `session.page` without holding `session.mu`**: Race condition. All page operations MUST go through `WithAccount()`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Database migrations | Custom SQL execution | goose v3 Provider API | Version tracking, rollback, embed support, battle-tested |
| Env var parsing | Manual os.Getenv + strconv | caarlos0/env v11 | Typed structs, defaults, required validation, slices |
| Browser auto-waits | Custom retry loops checking element existence | playwright-go Locator API | Built-in auto-retry with configurable timeout, actionability checks |
| Cookie isolation | Manual cookie management per account | `browser.NewContext()` | Playwright contexts handle cookie isolation transparently |
| Process cleanup | PID tracking + kill logic | Playwright driver lifecycle | `pw.Stop()` + `browser.Close()` handles Chromium cleanup |
| Form submission | Constructing HTTP POST requests | `page.Locator().Fill()` + `.Click()` | Hidden fields, CSRF tokens, JS validation handled automatically |
| Select dropdown | Setting values via JS injection | `page.Locator().SelectOption()` | Proper event triggering for TTL `<select>` element |

**Key insight:** playwright-go's Locator API handles most of the complexity that would require manual retry/wait logic. The auto-wait behavior means Fill(), Click(), and other actions automatically retry until the element is actionable (visible, enabled, stable). This eliminates most timing-related bugs.

## Common Pitfalls

### Pitfall 1: Node.js Driver Process Leak

**What goes wrong:** playwright-go's `Run()` spawns a Node.js driver process. If `pw.Stop()` is not called (e.g., panic, os.Exit before defer), the Node.js process and its managed Chromium processes become orphans.
**Why it happens:** Go's `defer` only runs on normal function return, not on `os.Exit()` or unrecovered panics in goroutines.
**How to avoid:** Use signal handling (SIGTERM, SIGINT) that explicitly calls `launcher.Close()` before exit. In main.go, use `signal.NotifyContext` and pass the cancellation down. Never call `os.Exit()` in goroutines.
**Warning signs:** Multiple `node` and `chromium` processes in `ps aux` after service restart.

### Pitfall 2: playwright-go Version Mismatch with Installed Browsers

**What goes wrong:** The playwright-go Go module version and the installed Playwright driver/browser version must match exactly. A version mismatch causes "please install the driver" errors at runtime.
**Why it happens:** Each playwright-go minor version (e.g., v0.5700.x) requires a specific Playwright driver version. Updating `go.mod` without re-running `playwright install` breaks things.
**How to avoid:** In Dockerfile, extract the version from go.mod and use it in the install command. Pin the exact version in CI. Always run `playwright install` after `go mod download`.
**Warning signs:** "please install the driver (v1.XX.0) first" error message.

### Pitfall 3: dns.he.net Session Invalidation on Concurrent Logins

**What goes wrong:** If two BrowserContexts log into the same HE account, the second login invalidates the first session. Subsequent operations on the first context silently fail.
**Why it happens:** dns.he.net uses server-side sessions. Two concurrent logins to the same account = session conflict.
**How to avoid:** Per-account mutex ensures only one BrowserContext and one active session per account. The mutex serializes ALL operations for a given account.
**Warning signs:** Operations succeed individually but fail when run concurrently for the same account.

### Pitfall 4: WaitForLoadState After Form POST

**What goes wrong:** After clicking a submit button on dns.he.net (which does a full-page form POST), reading the DOM immediately returns stale content from the previous page.
**Why it happens:** Form POST triggers a full page navigation. The DOM is temporarily the old page until the new page loads.
**How to avoid:** After every form submission: `page.WaitForLoadState(playwright.LoadStateNetworkidle)`, then verify a page-specific element (e.g., logout link, zone table) before reading content.
**Warning signs:** "element not found" errors that are intermittent; adding sleep "fixes" them.

### Pitfall 5: modernc.org/sqlite Driver Name Is "sqlite" Not "sqlite3"

**What goes wrong:** Code uses `sql.Open("sqlite3", ...)` which works with mattn/go-sqlite3 but fails with modernc.org/sqlite.
**Why it happens:** modernc.org/sqlite registers as `"sqlite"`, not `"sqlite3"`. This is a common migration stumbling block.
**How to avoid:** Always use `sql.Open("sqlite", dsn)`. The goose dialect is still `DialectSQLite3` -- that is the SQL dialect, not the driver name.
**Warning signs:** `sql: unknown driver "sqlite3"` error at startup.

### Pitfall 6: SQLite Pragma Syntax with modernc.org/sqlite

**What goes wrong:** Using `?_journal_mode=WAL` in DSN does not work with modernc.org/sqlite. Pragmas are silently ignored.
**Why it happens:** modernc.org/sqlite uses `_pragma=` prefix for DSN pragma parameters, not the `_journal_mode=` syntax used by mattn/go-sqlite3.
**How to avoid:** Use `?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)` format.
**Warning signs:** Database not in WAL mode despite DSN config; `PRAGMA journal_mode;` returns `delete`.

### Pitfall 7: Chromium in Docker Needs --with-deps

**What goes wrong:** `playwright install chromium` downloads the browser binary but not system dependencies (libx11, libnss3, etc.). Chromium fails to launch with cryptic errors.
**Why it happens:** Chromium requires ~50 system libraries. The `--with-deps` flag installs them via apt.
**How to avoid:** Always use `playwright install --with-deps chromium` in Dockerfile. Use ubuntu:noble (not alpine) as base -- Chromium on Alpine requires musl workarounds.
**Warning signs:** "Failed to launch browser" or "error while loading shared libraries" in Docker logs.

## Code Examples

### TTL Select Dropdown Interaction

```go
// Source: CONTEXT.md verified selectors + playwright-go API
// TTL is a <select> element with predefined values

const ttlSelect = "select#_ttl"

// SelectTTL selects a TTL value from the dropdown.
// Valid values: "300", "900", "1800", "3600", "7200", "14400", "28800", "43200", "86400", "172800"
func (rf *RecordFormPage) SelectTTL(ttlSeconds string) error {
    _, err := rf.page.Locator(ttlSelect).SelectOption(playwright.SelectOptionValues{
        Values: playwright.StringSlice(ttlSeconds),
    })
    return err
}
```

### Zone ID Extraction from Delete Button

```go
// Source: CONTEXT.md verified selectors
// Extract zone ID from: img[alt="delete"][name="ZONE_NAME"][value="ZONE_ID"]

func (zl *ZoneListPage) GetZoneID(zoneName string) (string, error) {
    selector := fmt.Sprintf("img[alt=\"delete\"][name=\"%s\"]", zoneName)
    zoneID, err := zl.page.Locator(selector).GetAttribute("value")
    if err != nil {
        return "", fmt.Errorf("get zone ID for %s: %w", zoneName, err)
    }
    return zoneID, nil
}
```

### Record Row Parsing

```go
// Source: CONTEXT.md verified selectors
// Record rows: tr.dns_tr[onclick="editRow(this)"]

func (zl *ZoneListPage) GetRecordIDs() ([]string, error) {
    rows, err := zl.page.Locator("tr.dns_tr").All()
    if err != nil {
        return nil, fmt.Errorf("get record rows: %w", err)
    }

    var ids []string
    for _, row := range rows {
        id, err := row.GetAttribute("id")
        if err != nil {
            continue // skip rows without id (e.g., locked SOA rows)
        }
        if id != "" {
            ids = append(ids, id)
        }
    }
    return ids, nil
}
```

### Config Loading

```go
// internal/config/config.go
package config

import (
    "fmt"
    "github.com/caarlos0/env/v11"
)

type Config struct {
    Port                   int     `env:"PORT" envDefault:"8080"`
    DBPath                 string  `env:"DB_PATH" envDefault:"dns-he-net.db"`
    HEAccountsJSON         string  `env:"HE_ACCOUNTS,required"`
    PlaywrightHeadless     bool    `env:"PLAYWRIGHT_HEADLESS" envDefault:"true"`
    PlaywrightSlowMo       float64 `env:"PLAYWRIGHT_SLOW_MO" envDefault:"0"`
    OperationTimeoutSec    int     `env:"OPERATION_TIMEOUT_SEC" envDefault:"30"`
    OperationQueueTimeout  int     `env:"OPERATION_QUEUE_TIMEOUT_SEC" envDefault:"60"`
    LogLevel               string  `env:"LOG_LEVEL" envDefault:"info"`
    MinOperationDelaySec   float64 `env:"MIN_OPERATION_DELAY_SEC" envDefault:"1.5"`
}

func Load() (*Config, error) {
    cfg, err := env.ParseAs[Config]()
    if err != nil {
        return nil, fmt.Errorf("parse config: %w", err)
    }
    return &cfg, nil
}
```

### Goose Migration File

```sql
-- internal/store/migrations/001_init.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS accounts (
    id          TEXT PRIMARY KEY,
    username    TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- NOTE: password is NOT stored in SQLite (SEC-01).
-- Credentials come from CredentialProvider (env var in Phase 1, Vault in Phase 4).
-- This table stores only metadata about known accounts.

CREATE TABLE IF NOT EXISTS schema_info (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO schema_info (key, value) VALUES ('version', '1');

-- +goose Down
DROP TABLE IF EXISTS schema_info;
DROP TABLE IF EXISTS accounts;
```

### Session Health Check

```go
// internal/browser/session.go

// isSessionHealthy navigates to the zone list and checks for login state.
// Returns true if the session is authenticated, false if re-login needed.
func (sm *SessionManager) isSessionHealthy(session *AccountSession) bool {
    // Check if session is too old (proactive re-login at 30 min)
    if time.Since(session.lastLogin) > sm.reloginAge {
        slog.Info("session aged out, needs re-login",
            "account", session.accountID,
            "age", time.Since(session.lastLogin),
        )
        return false
    }

    // Quick health check: is the logout link visible?
    loginPage := pages.NewLoginPage(session.page)
    loggedIn, err := loginPage.IsLoggedIn()
    if err != nil || !loggedIn {
        slog.Info("session health check failed",
            "account", session.accountID,
            "error", err,
        )
        return false
    }

    return true
}
```

### Dockerfile

```dockerfile
# Stage 1: Cache Go modules
FROM golang:1.24 AS modules
WORKDIR /modules
COPY go.mod go.sum ./
RUN go mod download

# Stage 2: Build Go binary + extract playwright-go version
FROM golang:1.24 AS builder
COPY --from=modules /go/pkg /go/pkg
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/server ./cmd/server

# Extract playwright version from go.mod for browser install
RUN grep playwright-go go.mod | sed 's/.*\(v[^ ]*\)/\1/' > /tmp/pw-version
RUN go install github.com/playwright-community/playwright-go/cmd/playwright@$(cat /tmp/pw-version)

# Stage 3: Runtime with Chromium
FROM ubuntu:noble
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata && \
    rm -rf /var/lib/apt/lists/*

# Copy playwright CLI and install Chromium + system deps
COPY --from=builder /go/bin/playwright /usr/local/bin/playwright
RUN playwright install --with-deps chromium

# Copy application
COPY --from=builder /bin/server /usr/local/bin/server

ENV PLAYWRIGHT_HEADLESS=true
EXPOSE 8080

ENTRYPOINT ["server"]
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Rod (go-rod/rod) pure Go | playwright-go with Node.js driver | CONTEXT.md decision 2026-02-27 | Requires Node.js in Docker; gains auto-waits, Inspector, traces |
| goose global API (`goose.Up()`) | goose Provider API (`goose.NewProvider()`) | goose v3.16.0 (2023) | Context support, no global state, structured results |
| `goose.SetDialect("sqlite3")` | `goose.DialectSQLite3` constant | goose v3.16.0 | Type-safe dialect, no string magic |
| mattn/go-sqlite3 (CGo) | modernc.org/sqlite (pure Go) | ~2023 mainstream adoption | No CGo = static binary, cross-compilation, simpler Docker |
| Manual env var parsing | caarlos0/env v11 generics `ParseAs[T]()` | env v11 (2024) | Type-safe parsing with generics, no struct pointer needed |

**Deprecated/outdated:**
- `goose.SetBaseFS()` + `goose.Up()`: Still works but the global API is deprecated in favor of Provider
- `playwright.Run(&playwright.RunOptions{SkipInstallBrowsers: true})`: Changed option names across versions; always check current API
- `page.WaitForNavigation()`: Deprecated in favor of `page.WaitForURL()` or action-specific waits

## Open Questions

1. **modernc.org/sqlite pragma DSN format**
   - What we know: modernc.org/sqlite uses `_pragma=name(value)` format in DSN
   - What's unclear: Whether all pragmas (journal_mode, busy_timeout, foreign_keys, synchronous) work via DSN, or if some must be executed as SQL after Open
   - Recommendation: Test at implementation time. Fallback is `db.Exec("PRAGMA ...")` after Open

2. **playwright-go SelectOption exact Go API for `<select>` elements**
   - What we know: JavaScript Playwright uses `selectOption('value')` or `selectOption({value: 'x'})`
   - What's unclear: The exact Go function signature for `SelectOption` on Locator
   - Recommendation: Check pkg.go.dev at implementation time; the Go API mirrors JS but with Go types

3. **dns.he.net session expiry duration**
   - What we know: Session uses browser cookies, expiry is undocumented
   - What's unclear: Exact timeout -- could be 15 minutes, 30 minutes, or hours
   - Recommendation: Start with 30-minute proactive re-login (per CONTEXT.md), tune based on observed failures

4. **`playwright install` size impact in Docker**
   - What we know: Playwright Chromium + Node.js driver adds ~250-350MB to Docker image
   - What's unclear: Exact final image size with ubuntu:noble base
   - Recommendation: Build and measure; optimize later if needed (Phase 4)

## Sources

### Primary (HIGH confidence)
- [playwright-go GitHub repo](https://github.com/playwright-community/playwright-go) - v0.5700.1 (Feb 24, 2026), installation, Dockerfile.example, API
- [playwright-go pkg.go.dev](https://pkg.go.dev/github.com/playwright-community/playwright-go) - Full API reference: Page, Locator, BrowserContext, Launch options
- [playwright-go DeepWiki](https://deepwiki.com/playwright-community/playwright-go) - Architecture overview, Run/Install, Context isolation, timeout config
- [Playwright official docs](https://playwright.dev/docs/api/class-page) - Page, Locator, BrowserContext API (JS API that Go mirrors)
- [Playwright auth docs](https://playwright.dev/docs/auth) - StorageState, session reuse patterns
- [Playwright browser contexts](https://playwright.dev/docs/browser-contexts) - Isolation model, cookie management
- [goose v3 pkg.go.dev](https://pkg.go.dev/github.com/pressly/goose/v3) - v3.27.0 (Feb 22, 2026), Provider API, DialectSQLite3
- [goose Provider docs](https://pressly.github.io/goose/documentation/provider/) - NewProvider with embed.FS, options
- [goose embed blog](https://pressly.github.io/goose/blog/2021/embed-sql-migrations/) - SetBaseFS pattern
- [modernc.org/sqlite pkg.go.dev](https://pkg.go.dev/modernc.org/sqlite) - Driver name "sqlite", pure Go SQLite
- [caarlos0/env v11 GitHub](https://github.com/caarlos0/env) - Struct tags, ParseAs generics

### Secondary (MEDIUM confidence)
- [Playwright Docker docs](https://playwright.dev/docs/docker) - Official Docker patterns for Playwright
- [Go project structure blog](https://www.glukhov.org/post/2025/12/go-project-structure/) - internal/ layout best practices
- [Secrets management in Go](https://tolubanji.com/posts/secrets-management-in-go/) - Credential interface pattern

### Tertiary (LOW confidence)
- PITFALLS.md training data (May 2025 cutoff) - dns.he.net session behavior, rate limiting thresholds (needs empirical validation)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - All versions verified on pkg.go.dev (Feb 2026)
- Architecture: HIGH - Standard Go patterns (internal/, page object) well-established
- playwright-go API: HIGH - Full API reference verified on pkg.go.dev, examples from GitHub
- Pitfalls: MEDIUM - Browser/Docker pitfalls are well-known; dns.he.net-specific behavior needs empirical validation
- SQLite/goose integration: HIGH - Driver name, dialect, pragma format verified

**Research date:** 2026-02-27
**Valid until:** 2026-03-27 (30 days - stable ecosystem, playwright-go may release minor updates)

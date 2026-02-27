# Architecture Patterns

**Domain:** Go REST API service wrapping headless browser automation for dns.he.net
**Researched:** 2026-02-26

## Recommended Architecture

The service follows a layered architecture with a critical concurrency boundary around browser sessions. The key insight: this is NOT a typical CRUD API. Every "write" operation translates to a browser automation sequence against an external web UI, which is slow (~2-10 seconds per operation), brittle, and must be serialized per account.

```
                    +------------------+
                    |   HTTP Clients   |  (Ansible, Terraform, curl, CI/CD)
                    +--------+---------+
                             |
                    +--------v---------+
                    |   HTTP Router    |  (chi or stdlib mux)
                    |  + Auth Middleware|  (JWT validation)
                    +--------+---------+
                             |
              +--------------+--------------+
              |              |              |
     +--------v---+  +------v------+  +----v--------+
     | API Handlers|  | Admin UI    |  | Sync Engine |
     | (REST CRUD) |  | (templ+htmx)|  | (Reconcile) |
     +--------+---+  +------+------+  +----+--------+
              |              |              |
     +--------v--------------v--------------v--------+
     |              Service Layer                     |
     |  (DNS operations, zone mgmt, sync logic)      |
     +---------------------+-------------------------+
                            |
     +---------------------v-------------------------+
     |           Account Session Manager             |
     |  (per-account mutex, request queue, retry)    |
     +---------------------+-------------------------+
                            |
     +---------------------v-------------------------+
     |            Browser Pool (Rod)                 |
     |  (one Browser per account, page lifecycle)    |
     +---------------------+-------------------------+
                            |
              +-------------+-------------+
              |                           |
     +--------v--------+       +----------v---------+
     | HE Page Objects  |       | Vault Client       |
     | (login, zone,    |       | (credential fetch) |
     | record screens)  |       +--------------------+
     +--------+---------+
              |                  +--------------------+
     +--------v--------+       | SQLite (go-sqlite3) |
     | dns.he.net       |       | (tokens, accounts,  |
     | Web Interface    |       |  metadata, audit)   |
     +------------------+       +--------------------+
```

### Component Boundaries

| Component | Responsibility | Communicates With | Package |
|-----------|---------------|-------------------|---------|
| HTTP Router + Middleware | Request routing, JWT auth, rate limiting, request ID | API Handlers, Admin UI | `internal/api` |
| API Handlers (REST) | Validate input, call service layer, format responses | Service Layer | `internal/api/handlers` |
| Admin UI (templ+htmx) | Render HTML for token/account management | Service Layer, Templates | `internal/ui` |
| Service Layer | Business logic: DNS CRUD, zone ops, sync/reconcile | Account Session Manager, SQLite | `internal/service` |
| Account Session Manager | Per-account mutex, request queuing, session health | Browser Pool | `internal/browser` |
| Browser Pool | Chromium lifecycle, page creation/cleanup | Rod, HE Page Objects | `internal/browser` |
| HE Page Objects | Encapsulate dns.he.net UI interactions per screen | Rod Pages | `internal/browser/pages` |
| Vault Client | Fetch HE credentials at runtime | HashiCorp Vault | `internal/vault` |
| SQLite Store | Persist tokens, account metadata, audit log | go-sqlite3 | `internal/store` |
| Sync Engine | Desired state diffing, plan generation, apply | Service Layer | `internal/sync` |

### Data Flow: DNS Record CRUD Request

Here is how a request to create a DNS record flows through the system:

```
1. HTTP POST /api/v1/zones/{zone}/records
   Headers: Authorization: Bearer <token>

2. Auth Middleware:
   - Look up token in SQLite -> get account_id, role
   - Reject if token expired/revoked or role=viewer for write ops
   - Attach account context to request

3. API Handler (RecordCreate):
   - Validate request body (record type, name, value, TTL)
   - Call service.CreateRecord(ctx, accountID, zoneID, record)

4. Service Layer:
   - Call sessionManager.Execute(ctx, accountID, func(session) error {
       return session.CreateRecord(zoneID, record)
     })

5. Account Session Manager:
   - Acquire per-account mutex (or enqueue if busy)
   - Ensure browser session is healthy (logged in, not stale)
   - If not healthy: fetch credentials from Vault, re-login
   - Execute the callback with the session
   - Release mutex

6. HE Page Object (RecordPage):
   - Navigate to zone edit page
   - Fill in record form fields
   - Submit form
   - Wait for confirmation / detect errors
   - Return result

7. Response flows back up:
   Service -> Handler -> JSON response -> Client
```

**Critical path timing:** Steps 5-6 take 2-10 seconds. The mutex ensures only one browser operation per account at a time. Other requests for the same account queue up.

### Data Flow: Sync/Reconcile Operation

```
1. HTTP POST /api/v1/zones/{zone}/sync
   Body: { "records": [...desired state...] }

2. Service Layer:
   a. Fetch current state: sessionManager.Execute(accountID, session.ListRecords(zoneID))
   b. Diff desired vs current -> generate changeset (add, update, delete)
   c. For each change: sessionManager.Execute(accountID, session.ApplyChange(change))
   d. Return sync report: { added: N, updated: N, deleted: N, errors: [...] }

3. Each Execute call acquires the account mutex sequentially
   - Changes applied one at a time (browser cannot parallelize within one session)
   - Short delay between operations to avoid bot detection
```

## Concurrency Model

This is the most architecturally significant aspect of the service.

### The Problem

- Multiple API clients may hit the same dns.he.net account simultaneously
- A browser session can only do one thing at a time (navigate, click, wait)
- Logging in twice to the same HE account from two browsers may invalidate the first session
- Browser operations are slow (seconds, not milliseconds)

### The Solution: Per-Account Session with Mutex Queue

```go
// internal/browser/manager.go

type AccountSession struct {
    mu        sync.Mutex        // Serializes all operations for this account
    accountID string
    browser   *rod.Browser      // One Chromium instance per account
    page      *rod.Page         // Reusable page (or create per-op)
    loggedIn  bool
    lastUsed  time.Time
    vault     vault.Client      // To fetch credentials on-demand
}

type SessionManager struct {
    mu       sync.RWMutex                  // Protects the sessions map
    sessions map[string]*AccountSession    // keyed by account ID
    launcher *launcher.Launcher            // Shared Chromium launcher config
}

// Execute runs a browser operation for a specific account, serialized.
func (m *SessionManager) Execute(ctx context.Context, accountID string,
    fn func(*AccountSession) error) error {

    session := m.getOrCreateSession(accountID)

    session.mu.Lock()         // Block until this account's session is free
    defer session.mu.Unlock()

    // Health check: is the browser still alive? Is it logged in?
    if err := session.ensureHealthy(ctx); err != nil {
        return fmt.Errorf("session unhealthy: %w", err)
    }

    // Execute the actual browser operation
    err := fn(session)
    session.lastUsed = time.Now()

    if isFatalBrowserError(err) {
        session.restart(ctx) // Kill and recreate browser
    }

    return err
}
```

**Why sync.Mutex and not channels:** The pattern here is straightforward mutual exclusion. A mutex is simpler, well-understood, and Go's sync.Mutex is fair (FIFO in practice since Go 1.9). Channels would add complexity without benefit for this use case.

**Why one Browser per account (not one shared Browser with multiple Pages):** Rod's Browser maps to a Chromium process. Separate processes per account provide:
- Session isolation (cookies, localStorage are per-browser-profile)
- Crash isolation (one account's Chromium crash does not affect others)
- Simpler login state management

**For low account counts (likely 1-5), this is fine.** If scaling to 50+ accounts, switch to a single Browser with incognito contexts per account (Rod supports `browser.Incognito()`).

### Request Timeout and Queuing

```go
func (m *SessionManager) Execute(ctx context.Context, accountID string,
    fn func(*AccountSession) error) error {

    session := m.getOrCreateSession(accountID)

    // Do not wait forever if the session is busy
    done := make(chan error, 1)
    go func() {
        session.mu.Lock()
        defer session.mu.Unlock()
        done <- fn(session)
    }()

    select {
    case err := <-done:
        return err
    case <-ctx.Done():
        return fmt.Errorf("request timed out waiting for account session: %w", ctx.Err())
    }
}
```

### Session Lifecycle

```
Account Session States:

  IDLE ------> EXECUTING ------> IDLE
    |              |
    |         (on fatal error)
    |              |
    v              v
  STARTING <-- RESTARTING ----> FAILED
    |                             |
    v                             v
  LOGGING_IN                 (backoff retry)
    |
    v
  READY ------> IDLE
```

**Startup:** Lazy initialization. Sessions are created on first request to an account, not at service boot.

**Health Check (before each operation):**
1. Is the Chromium process alive? (browser.GetContext() / process check)
2. Has the session been idle too long? (HE may have expired it -- configurable threshold, default 15 min)
3. Is the page responsive? (try a simple evaluation)
4. If any check fails: restart browser, re-login via Vault credentials

**Graceful Shutdown:** On SIGTERM/SIGINT, close all browser sessions cleanly before exiting. Rod's Browser.Close() handles Chromium process cleanup.

**Idle Cleanup:** A background goroutine periodically checks lastUsed and closes sessions idle for >30 minutes to free resources.

## Component Details

### 1. HTTP Router + Auth Middleware

**Use chi router.** Go 1.22 added method-based routing to the stdlib, which eliminates the primary reason for chi. However, chi provides middleware chaining and route grouping ergonomics that are still valuable.

**Recommendation: chi.** Marginal complexity, significant DX improvement for middleware stacking.

```go
r := chi.NewRouter()
r.Use(middleware.RequestID)
r.Use(middleware.RealIP)
r.Use(middleware.Logger)
r.Use(middleware.Recoverer)
r.Use(middleware.Timeout(30 * time.Second))

// Public routes
r.Post("/api/v1/auth/token", handlers.IssueToken)

// Protected API routes
r.Route("/api/v1", func(r chi.Router) {
    r.Use(auth.JWTMiddleware)
    r.Get("/zones", handlers.ListZones)
    r.Post("/zones/{zone}/records", handlers.CreateRecord)
    r.Post("/zones/{zone}/sync", handlers.SyncRecords)
    // ...
})

// Admin UI routes (separate auth - session cookie or same JWT)
r.Route("/admin", func(r chi.Router) {
    r.Use(auth.AdminOnly)
    // templ handlers
})

// Static assets (embedded)
r.Handle("/static/*", http.FileServer(http.FS(staticFS)))
```

### 2. Auth: Database-Backed Bearer Token System

Per PROJECT.md: "JWT opaque-style tokens (not signed claims) -- Simpler revocation, just delete from DB."

This means tokens are random strings stored in SQLite, NOT cryptographically signed JWTs. This is actually a **database-backed bearer token** system, which is the correct choice here because:
- Instant revocation (delete row)
- No token size bloat
- No key rotation complexity
- Account/role lookup requires DB anyway

```go
// Token is a random 32-byte hex string
// Stored in SQLite: tokens(id, token_hash, account_id, role, expires_at, created_at, revoked_at)
// Lookup: SELECT account_id, role FROM tokens
//         WHERE token_hash = ? AND revoked_at IS NULL
//         AND (expires_at IS NULL OR expires_at > NOW())
```

**Store the SHA-256 hash of the token, not the token itself.** The plaintext token is shown once at creation and never stored.

### 3. HE Page Objects (Browser Automation Layer)

This is the brittle layer. Encapsulate ALL dns.he.net UI interaction behind clean interfaces so that when HE changes their HTML, you fix ONE package.

```go
// internal/browser/pages/login.go
type LoginPage struct {
    page *rod.Page
}

func (p *LoginPage) Login(username, password string) error {
    // Navigate to login URL
    // Fill email field
    // Fill password field
    // Click submit
    // Wait for redirect / check for error message
}

// internal/browser/pages/zone.go
type ZonePage struct {
    page *rod.Page
}

func (p *ZonePage) ListZones() ([]Zone, error) { ... }
func (p *ZonePage) AddZone(domain string) error { ... }
func (p *ZonePage) DeleteZone(zoneID string) error { ... }

// internal/browser/pages/record.go
type RecordPage struct {
    page *rod.Page
}

func (p *RecordPage) ListRecords(zoneID string) ([]Record, error) { ... }
func (p *RecordPage) AddRecord(zoneID string, r Record) error { ... }
func (p *RecordPage) UpdateRecord(zoneID string, r Record) error { ... }
func (p *RecordPage) DeleteRecord(zoneID, recordID string) error { ... }
```

**Key patterns for Rod page objects:**
- Use rod.Page.WaitStable() or page.WaitLoad() before interacting
- Use CSS selectors with fallback selectors (HE may change class names)
- Add configurable delays between operations (polite scraping)
- Screenshot on error for debugging (page.Screenshot())
- Retry with exponential backoff on transient errors (network, timeout)

### 4. Vault Client

Thin wrapper around the Vault Go client. Fetch credentials on-demand, cache briefly (5 min TTL) in memory to avoid hammering Vault.

```go
// internal/vault/client.go
type Client struct {
    client    *vaultapi.Client
    cache     map[string]*cachedCred
    cacheMu   sync.RWMutex
    cacheTTL  time.Duration
    mountPath string  // e.g., "secret/data/dns-he-net"
}

type HECredentials struct {
    Username string
    Password string
}

func (c *Client) GetCredentials(ctx context.Context, accountID string) (*HECredentials, error) {
    // Check cache first
    // If miss or expired: vault read secret/data/dns-he-net/{accountID}
    // Cache and return
}
```

**Vault path convention:** `secret/data/dns-he-net/{account-id}` with keys `username` and `password`.

**Startup behavior:** Service should verify Vault connectivity at startup but NOT pre-fetch all credentials. Fetch lazily on first request to each account.

### 5. SQLite Store

**Recommendation: `modernc.org/sqlite`** for zero-CGO builds (simpler Docker images, cross-compilation). For a service where browser ops take seconds, SQLite query speed difference vs CGO variant is irrelevant.

```go
// internal/store/store.go
type Store struct {
    db *sql.DB
}

// Tables:
// accounts(id TEXT PK, name TEXT, vault_path TEXT, created_at, updated_at)
// tokens(id TEXT PK, token_hash TEXT UNIQUE, account_id FK, role TEXT,
//        label TEXT, expires_at, created_at, revoked_at)
// audit_log(id INTEGER PK, account_id, action TEXT, details TEXT, created_at)

func (s *Store) CreateToken(accountID, role, label string, expiresAt *time.Time) (plaintext string, err error)
func (s *Store) ValidateToken(tokenPlaintext string) (*TokenInfo, error)
func (s *Store) RevokeToken(tokenID string) error
func (s *Store) ListTokens(accountID string) ([]TokenMeta, error)
func (s *Store) GetAccount(id string) (*Account, error)
func (s *Store) ListAccounts() ([]Account, error)
```

**SQLite pragmas at connection time:**
```sql
PRAGMA journal_mode=WAL;      -- Concurrent reads while writing
PRAGMA busy_timeout=5000;      -- Wait up to 5s on lock instead of failing
PRAGMA foreign_keys=ON;
PRAGMA synchronous=NORMAL;     -- Good enough for this use case
```

### 6. Sync Engine

The reconcile engine compares desired state against current state and produces a plan.

```go
// internal/sync/engine.go

type SyncPlan struct {
    ToAdd    []Record
    ToUpdate []RecordUpdate  // {Old, New}
    ToDelete []Record
}

type SyncResult struct {
    Added   int
    Updated int
    Deleted int
    Errors  []SyncError
}

func Diff(desired, current []Record) *SyncPlan {
    // Match records by (type, name) composite key
    // Compare: value, TTL, priority (for MX)
    // Produce add/update/delete sets
}

func (e *Engine) Apply(ctx context.Context, accountID, zoneID string,
    plan *SyncPlan) (*SyncResult, error) {
    // Apply deletes first (avoid conflicts)
    // Then updates
    // Then adds
    // Each operation goes through SessionManager.Execute()
    // Collect errors but continue (partial success is OK)
}
```

**Idempotency:** Calling sync twice with the same desired state should produce no changes on the second call. This is achieved by always re-reading current state before diffing.

### 7. Embedded Frontend (templ + htmx)

```
internal/ui/
  templates/        -- .templ files
    layout.templ
    accounts.templ
    tokens.templ
  handlers.go       -- HTTP handlers that render templ components
  static/           -- CSS, JS (htmx, minimal custom JS)
    embed.go        -- //go:embed directive
```

Build step: `templ generate` converts .templ files to .go files before `go build`.

The admin UI is simple CRUD for accounts and tokens. htmx handles dynamic updates without page reloads. No complex state management needed.

## Project Package Structure

```
dns-he-net-automation/
  cmd/
    server/
      main.go              -- Entry point, wire everything together
  internal/
    api/
      router.go            -- Chi router setup, route registration
      middleware/
        auth.go            -- Token validation middleware
        ratelimit.go       -- Per-account rate limiting
      handlers/
        zones.go           -- Zone CRUD handlers
        records.go         -- Record CRUD handlers
        sync.go            -- Sync/reconcile handler
        tokens.go          -- Token management handlers (API)
        health.go          -- Health check endpoint
    browser/
      manager.go           -- SessionManager (per-account mutex, lifecycle)
      session.go           -- AccountSession (single account's browser)
      launcher.go          -- Chromium launcher configuration
      pages/
        login.go           -- Login page automation
        zones.go           -- Zone listing/management page
        records.go         -- Record CRUD page automation
        common.go          -- Shared selectors, wait helpers
    service/
      dns.go               -- DNS business logic (zones, records)
      accounts.go          -- Account management logic
      tokens.go            -- Token issuance/revocation logic
    sync/
      diff.go              -- State diffing algorithm
      engine.go            -- Sync orchestration
      plan.go              -- SyncPlan types
    store/
      store.go             -- SQLite store (constructor, migrations)
      tokens.go            -- Token queries
      accounts.go          -- Account queries
      audit.go             -- Audit log queries
      migrations/          -- SQL migration files
    vault/
      client.go            -- Vault client wrapper
    ui/
      handlers.go          -- templ page handlers
      templates/           -- .templ files
      static/
        embed.go           -- Embedded static assets
    config/
      config.go            -- Configuration struct + loading
    models/
      zone.go              -- Zone, Record domain types
      account.go           -- Account, Token domain types
  Dockerfile
  Makefile
  go.mod
```

## Patterns to Follow

### Pattern 1: Functional Options for Configuration

**What:** Use functional options for configuring complex components like SessionManager.
**When:** Components with many optional configuration parameters.

```go
type SessionManagerOption func(*SessionManager)

func WithIdleTimeout(d time.Duration) SessionManagerOption {
    return func(m *SessionManager) { m.idleTimeout = d }
}

func WithPoliteDelay(d time.Duration) SessionManagerOption {
    return func(m *SessionManager) { m.politeDelay = d }
}

func NewSessionManager(vault vault.Client, opts ...SessionManagerOption) *SessionManager {
    m := &SessionManager{
        sessions:    make(map[string]*AccountSession),
        vault:       vault,
        idleTimeout: 30 * time.Minute, // defaults
        politeDelay: 500 * time.Millisecond,
    }
    for _, opt := range opts {
        opt(m)
    }
    return m
}
```

### Pattern 2: Context Propagation for Cancellation

**What:** Pass context.Context through every layer so HTTP request cancellation kills browser waits.
**When:** Always -- every public method takes a context.

```go
// Rod supports context natively
page.Context(ctx).MustNavigate(url)
// If the HTTP client disconnects, context cancels, Rod aborts navigation
```

### Pattern 3: Error Wrapping with Domain Types

**What:** Define domain error types so handlers can map to correct HTTP status codes.
**When:** Service layer returns errors that handlers must interpret.

```go
var (
    ErrAccountNotFound   = errors.New("account not found")
    ErrZoneNotFound      = errors.New("zone not found")
    ErrRecordNotFound    = errors.New("record not found")
    ErrSessionBusy       = errors.New("account session busy")
    ErrBrowserCrashed    = errors.New("browser session crashed")
    ErrHELoginFailed     = errors.New("dns.he.net login failed")
    ErrVaultUnavailable  = errors.New("vault unavailable")
)
```

### Pattern 4: Graceful Shutdown

**What:** Handle SIGTERM/SIGINT to close browser sessions before exiting.
**When:** Always -- leaked Chromium processes waste server resources.

```go
ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer cancel()

// Start server
go srv.ListenAndServe()

<-ctx.Done()
log.Info("shutting down...")

// Shutdown HTTP server (stop accepting, drain connections)
srv.Shutdown(context.Background())

// Close all browser sessions
sessionManager.CloseAll()

// Close SQLite
store.Close()
```

## Anti-Patterns to Avoid

### Anti-Pattern 1: Shared Browser Across Accounts
**What:** Using one Chromium instance with regular pages for multiple accounts.
**Why bad:** Cookie contamination between accounts. Login to account B invalidates account A's session.
**Instead:** One Browser (or incognito context) per account. Strict session isolation.

### Anti-Pattern 2: Fire-and-Forget Browser Operations
**What:** Starting a browser operation without waiting for completion or checking results.
**Why bad:** Silent failures. Records appear created but are not. Stale pages accumulate.
**Instead:** Every browser operation must confirm success (check for success indicator on page), return errors, and clean up on failure.

### Anti-Pattern 3: Hardcoded CSS Selectors Scattered Everywhere
**What:** Putting `page.MustElement("#dns_record_form input[name='Name']")` directly in handler code.
**Why bad:** When HE changes their HTML, you are grepping the entire codebase.
**Instead:** All selectors in the pages/ package. One file per screen. Selector constants at the top.

### Anti-Pattern 4: No Operation Timeout
**What:** Browser operations that can hang forever waiting for an element.
**Why bad:** One hung operation blocks the account mutex forever, queueing all other requests.
**Instead:** Every Rod operation must have a timeout via context. Default 30s per operation, configurable.

### Anti-Pattern 5: Direct Vault Calls in Handlers
**What:** Fetching credentials in API handlers.
**Why bad:** Mixing concerns. Handler should not know about Vault.
**Instead:** Vault is only called by the SessionManager when it needs to (re)authenticate a browser session. Handlers never touch credentials.

## Scalability Considerations

| Concern | 1-3 accounts | 5-10 accounts | 20+ accounts |
|---------|-------------|---------------|--------------|
| Browser processes | One Chromium per account (~150MB each) | ~1.5GB RAM, still fine | Switch to incognito contexts to share Chromium instances |
| Concurrent requests | Mutex queuing, sub-second wait | Queue depth may grow, add timeout | Consider priority queuing or dedicated workers |
| SQLite writes | No contention | WAL mode handles fine | Still fine -- SQLite handles thousands of writes/sec |
| Vault calls | Minimal, cached | Moderate, cache helps | Consider batch prefetch on startup |
| Docker image size | Chromium adds ~400MB | Same | Same -- Chromium size is fixed |

For the expected use case (1-5 accounts, moderate request rate), the simple mutex-per-account model is correct and sufficient. Do not over-engineer.

## Build Order (Dependencies)

This directly informs the roadmap phase structure.

```
Phase 1: Foundation
  [config] -> [store/SQLite] -> [models]
  These have zero external dependencies. Build and test in isolation.

Phase 2: Browser Core
  [vault/client] -> [browser/launcher] -> [browser/pages/login]
  Cannot do anything without logging in first. This is the riskiest component.
  Build a manual test that logs into dns.he.net and verifies session.

Phase 3: Browser Page Objects
  [browser/pages/zones] -> [browser/pages/records]
  Depends on Phase 2 login working. Each page object can be built/tested incrementally.

Phase 4: Session Manager
  [browser/manager] + [browser/session]
  Wraps Phase 2+3 with concurrency control. Depends on page objects existing.

Phase 5: Service Layer + API
  [service/*] -> [api/handlers] -> [api/router] -> [api/middleware/auth]
  Wires session manager to HTTP. Depends on Phase 4.

Phase 6: Sync Engine
  [sync/diff] -> [sync/engine]
  Depends on service layer (Phase 5) for individual record operations.

Phase 7: Frontend
  [ui/templates] -> [ui/handlers] -> [ui/static]
  Can be built in parallel with Phase 6. Only needs store and service layer.

Phase 8: Integration + Deployment
  [Dockerfile] -> [Makefile] -> integration tests -> documentation
```

**Critical path:** Phases 1 -> 2 -> 3 -> 4 -> 5 are strictly sequential. Phase 2 (browser login) is the highest-risk component and should be proven early.

**Parallel opportunities:** Phase 7 (frontend) can start as soon as Phase 5 is done. Phase 6 (sync) can start as soon as basic CRUD works in Phase 5.

## Sources

- Go Rod library documentation and API (go-rod.github.io) -- HIGH confidence on Rod patterns, based on extensive library documentation review
- Go stdlib net/http routing improvements in Go 1.22 -- HIGH confidence
- chi router patterns -- HIGH confidence, well-established library
- SQLite WAL mode and modernc.org/sqlite -- HIGH confidence
- HashiCorp Vault Go client (github.com/hashicorp/vault/api) -- HIGH confidence
- templ library for Go HTML templating -- MEDIUM confidence (newer library, patterns still evolving)
- htmx integration patterns with Go -- MEDIUM confidence

**Note:** Rod concurrency patterns are based on library API analysis and general Go concurrency best practices. The specific pattern of one-browser-per-account with mutex serialization is an architectural recommendation, not a prescribed Rod pattern. It should be validated during Phase 2 implementation.

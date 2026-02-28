# Phase 4: Production Hardening - Research

**Researched:** 2026-02-28
**Domain:** HashiCorp Vault Go client, circuit breakers, rate limiting, retry/backoff, Docker packaging, playwright-go screenshots
**Confidence:** HIGH (core stack verified via official docs and pkg.go.dev)

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| VAULT-01 | dns.he.net credentials stored in Vault KV v2 at configurable mount path `secret/data/dns-he-net/{account-id}` | `hashicorp/vault/api` v1.22.0 with `client.KVv2(mountPath).Get(ctx, path)` |
| VAULT-02 | Credentials fetched lazily on first request, not pre-fetched at startup | `credential.Provider` interface already exists; `VaultProvider` implements lazy fetch via `sync.Mutex` + nil check pattern |
| VAULT-03 | Fetched credentials cached in-memory with configurable TTL (default 5 min) | `map[string]*cachedCred` + `sync.RWMutex` + `time.Time` expiry field pattern; no external library needed |
| VAULT-04 | Service verifies Vault connectivity at startup, reports via health endpoint | `client.Sys().Health()` or lightweight `client.Logical().Read("sys/health")` at startup; health handler already exists in `handlers/health.go` |
| VAULT-05 | If Vault unreachable, cached credentials + active sessions continue (degraded mode) | Cache read on fetch error; `slog.Warn` + continue on Vault error when cache hit exists |
| VAULT-06 | Vault auth supports token auth AND AppRole, selectable via config | `client.SetToken()` for token auth; `vault/api/auth/approle` v0.11.0 for AppRole |
| BROWSER-08 | Configurable inter-operation delay with jitter (default 2-3s range) | Already partially implemented as `minOpDelay` in `SessionManager`; needs jitter addition using `math/rand` |
| BROWSER-09 | Fatal browser error causes automatic restart with fresh Chromium context + re-login | `ensureHealthy` already handles recovery; needs explicit "fatal crash" detection path + OBS-03 screenshot before restart |
| RES-01 | Transient browser failures retried with exponential backoff + jitter (max 3 attempts) | `github.com/sethvargo/go-retry` v0.3.0 already in go.mod; `retry.NewExponential` + `retry.WithJitter` + `retry.WithMaxRetries(3)` |
| RES-02 | Per-token + global rate limiting returns 429 with Retry-After header | `github.com/go-chi/httprate` v0.15.0; `WithKeyFuncs` for per-token key; sets `Retry-After` automatically |
| RES-03 | Circuit breaker pauses account after N consecutive failures, auto-recovers | `github.com/sony/gobreaker/v2` v2.4.0; `Settings.ReadyToTrip` configures N; `Settings.Timeout` for recovery |
| OBS-03 | Failed browser operations produce debug screenshot to configurable directory | `page.Screenshot(&playwright.PageScreenshotOptions{Path: playwright.String(path)})` in error recovery path |
| OPS-05 | Ships as static Go binary AND Docker image based on `chromedp/headless-shell` (~150MB) | Dockerfile already exists with ubuntu:noble base; OPS-05 spec says `chromedp/headless-shell` — need to evaluate switching base or keeping ubuntu+playwright |
</phase_requirements>

---

## Summary

Phase 4 adds five distinct production-hardening layers to the existing working DNS automation service: (1) Vault credential storage replacing environment variables, (2) exponential retry/backoff for transient browser failures, (3) per-account circuit breakers, (4) per-token HTTP rate limiting with 429/Retry-After, and (5) Docker packaging with debug screenshots on failure.

The existing codebase has a clean `credential.Provider` interface (`internal/credential/provider.go`) designed exactly for this phase's Vault integration — `EnvProvider` is replaced by `VaultProvider` implementing the same interface. The `sethvargo/go-retry` package is already in `go.mod` (added as an indirect dependency via playwright-go). The `SessionManager.WithAccount` pattern wraps browser operations cleanly, making retry wrapping straightforward. Rate limiting integrates as chi middleware.

The biggest architectural decision is the Docker base image. The requirements spec says `chromedp/headless-shell` but the project uses playwright-go (not chromedp). The existing Dockerfile already works with `ubuntu:noble` + playwright install. The recommendation is to keep the ubuntu:noble approach (which is tested and working) and update the requirement description to reflect reality — `chromedp/headless-shell` is chromedp-specific and is not compatible with playwright-go.

**Primary recommendation:** Implement in three focused plans: (1) Vault provider, (2) resilience layer (retry + circuit breaker + rate limiting + jitter), (3) Docker refinement + debug screenshots.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/hashicorp/vault/api` | v1.22.0 | Vault client: KV v2, token/AppRole auth, health | Official HashiCorp Go client, 4,878+ imports, MPL-2.0 |
| `github.com/hashicorp/vault/api/auth/approle` | v0.11.0 | AppRole authentication method | Official companion package, pairs with vault/api |
| `github.com/sony/gobreaker/v2` | v2.4.0 | Circuit breaker with generics support | De facto standard Go circuit breaker; MIT; Jan 2026 release |
| `github.com/go-chi/httprate` | v0.15.0 | HTTP rate limiting middleware for chi router | Already using go-chi; returns 429 + Retry-After automatically |
| `github.com/sethvargo/go-retry` | v0.3.0 | Exponential backoff with jitter, max retries | Already in go.mod (indirect); zero external deps; context-aware |

### Already In go.mod (No New Dependencies)

| Library | Version | Phase 4 Use |
|---------|---------|-------------|
| `github.com/sethvargo/go-retry` | v0.3.0 | Retry with exponential backoff for browser ops (RES-01) |
| `github.com/playwright-community/playwright-go` | v0.5700.1 | `page.Screenshot()` for debug screenshots (OBS-03) |

### Supporting (Standard Library Only)

| Package | Purpose |
|---------|---------|
| `math/rand` | Jitter for inter-operation delay (BROWSER-08) |
| `sync.RWMutex` + `map` | Credential cache with TTL (VAULT-03) |
| `time` | TTL expiry comparison for credential cache |

### New Dependencies to Add

```bash
go get github.com/hashicorp/vault/api@v1.22.0
go get github.com/hashicorp/vault/api/auth/approle@v0.11.0
go get github.com/sony/gobreaker/v2@v2.4.0
go get github.com/go-chi/httprate@v0.15.0
```

Note: `sethvargo/go-retry` is already present as indirect; promote to direct if used explicitly.

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `vault/api` | `vault-client-go` (OpenAPI-generated) | vault-client-go has more type-safe API but fewer community examples; vault/api is battle-tested and has the KVv2 helper type |
| `sony/gobreaker/v2` | `go-kit/kit/circuitbreaker` | go-kit is heavier (full microservice framework); gobreaker is purpose-built |
| `go-chi/httprate` | `golang.org/x/time/rate` manually | x/time/rate requires manual Retry-After header; httprate handles headers automatically and integrates with chi |
| `sethvargo/go-retry` | Hand-rolled retry loop | go-retry is already in go.mod, well-tested, context-aware |

---

## Architecture Patterns

### Recommended Project Structure (New Files)

```
internal/
├── credential/
│   ├── provider.go          # (existing) Provider interface
│   ├── env.go               # (existing) EnvProvider
│   └── vault.go             # NEW: VaultProvider (VAULT-01..06)
├── resilience/
│   ├── retry.go             # NEW: WithRetry wrapper for browser ops (RES-01)
│   └── circuitbreaker.go    # NEW: per-account CircuitBreaker registry (RES-03)
└── api/
    └── middleware/
        ├── auth.go           # (existing)
        ├── rbac.go           # (existing)
        └── ratelimit.go      # NEW: per-token + global rate limiting (RES-02)
```

Screenshot directory is configurable via env var `SCREENSHOT_DIR` (default: `./screenshots/`).

### Pattern 1: VaultProvider — Lazy Fetch with TTL Cache

**What:** Implements `credential.Provider` interface, fetches from Vault KV v2 on first access per account, caches with TTL, serves from cache on Vault outage.

**When to use:** Replace `EnvProvider` in `main.go` when `VAULT_ADDR` is configured.

```go
// Source: internal/credential/vault.go (to be created)
// Pattern verified against: https://pkg.go.dev/github.com/hashicorp/vault/api v1.22.0

type cachedCred struct {
    cred      *Credential
    fetchedAt time.Time
}

type VaultProvider struct {
    client    *api.Client
    mountPath string  // e.g. "secret"
    pathTmpl  string  // e.g. "dns-he-net/%s"
    ttl       time.Duration
    mu        sync.RWMutex
    cache     map[string]*cachedCred
}

func (p *VaultProvider) GetCredential(ctx context.Context, accountID string) (*Credential, error) {
    // 1. Check cache (read lock)
    p.mu.RLock()
    if cached, ok := p.cache[accountID]; ok && time.Since(cached.fetchedAt) < p.ttl {
        p.mu.RUnlock()
        return cached.cred, nil
    }
    p.mu.RUnlock()

    // 2. Fetch from Vault
    path := fmt.Sprintf(p.pathTmpl, accountID)
    secret, err := p.client.KVv2(p.mountPath).Get(ctx, path)
    if err != nil {
        // VAULT-05: serve from stale cache on Vault outage
        p.mu.RLock()
        if cached, ok := p.cache[accountID]; ok {
            p.mu.RUnlock()
            slog.WarnContext(ctx, "vault unreachable, using stale credential cache", "account", accountID)
            return cached.cred, nil
        }
        p.mu.RUnlock()
        return nil, fmt.Errorf("vault fetch for account %q: %w", accountID, err)
    }

    // 3. Extract credentials
    username, _ := secret.Data["username"].(string)
    password, _ := secret.Data["password"].(string)
    cred := &Credential{AccountID: accountID, Username: username, Password: password}

    // 4. Update cache (write lock)
    p.mu.Lock()
    p.cache[accountID] = &cachedCred{cred: cred, fetchedAt: time.Now()}
    p.mu.Unlock()

    return cred, nil
}
```

### Pattern 2: Vault Auth — Token vs AppRole Selection

**What:** Config-driven auth method selection at startup.

```go
// Source: based on https://pkg.go.dev/github.com/hashicorp/vault/api/auth/approle v0.11.0
// and https://github.com/hashicorp/vault-examples/blob/main/examples/auth-methods/approle/go/example.go

func NewVaultClient(cfg *VaultConfig) (*api.Client, error) {
    config := api.DefaultConfig()
    config.Address = cfg.VaultAddr  // VAULT_ADDR env var

    client, err := api.NewClient(config)
    if err != nil {
        return nil, err
    }

    switch cfg.AuthMethod {
    case "token":
        // VAULT-06: token auth — simplest, suitable for dev/trusted environments
        client.SetToken(cfg.VaultToken)  // VAULT_TOKEN env var

    case "approle":
        // VAULT-06: AppRole — platform-agnostic, suitable for Docker/bare-metal
        secretID := &auth.SecretID{FromString: cfg.AppRoleSecretID}
        appRoleAuth, err := auth.NewAppRoleAuth(
            cfg.AppRoleRoleID,
            secretID,
            auth.WithMountPath(cfg.AppRoleMountPath), // default "approle"
        )
        if err != nil {
            return nil, err
        }
        authInfo, err := client.Auth().Login(context.Background(), appRoleAuth)
        if err != nil {
            return nil, fmt.Errorf("vault approle login: %w", err)
        }
        _ = authInfo
    }
    return client, nil
}
```

### Pattern 3: Retry with Exponential Backoff (go-retry)

**What:** Wrap browser operations with retry logic using already-present `sethvargo/go-retry`.

```go
// Source: https://pkg.go.dev/github.com/sethvargo/go-retry v0.3.0
// go-retry is already in go.mod as indirect dependency

func WithRetry(ctx context.Context, op func(context.Context) error) error {
    b := retry.NewExponential(500 * time.Millisecond)
    b = retry.WithJitter(200*time.Millisecond, b)
    b = retry.WithMaxRetries(3, b)

    return retry.Do(ctx, b, func(ctx context.Context) error {
        err := op(ctx)
        if err == nil {
            return nil
        }
        // Only retry transient errors; permanent errors return immediately
        if isTransient(err) {
            return retry.RetryableError(err)
        }
        return err // non-retryable: stop immediately
    })
}

// isTransient distinguishes timeout/session errors (retryable) from logic errors (not retryable)
func isTransient(err error) bool {
    return errors.Is(err, browser.ErrSessionUnhealthy) ||
        errors.Is(err, context.DeadlineExceeded) ||
        strings.Contains(err.Error(), "timeout")
}
```

### Pattern 4: Circuit Breaker per Account (gobreaker v2)

**What:** Per-account circuit breaker registry. After N consecutive failures, breaker opens and requests fail fast for backoff duration.

```go
// Source: https://pkg.go.dev/github.com/sony/gobreaker/v2 v2.4.0

type BreakerRegistry struct {
    mu       sync.RWMutex
    breakers map[string]*gobreaker.CircuitBreaker[error]
    settings gobreaker.Settings
}

func NewBreakerRegistry(maxFailures uint32, timeout time.Duration) *BreakerRegistry {
    return &BreakerRegistry{
        breakers: make(map[string]*gobreaker.CircuitBreaker[error]),
        settings: gobreaker.Settings{
            MaxRequests: 1,  // one probe in half-open state
            Timeout:     timeout,
            ReadyToTrip: func(counts gobreaker.Counts) bool {
                return counts.ConsecutiveFailures >= maxFailures
            },
            OnStateChange: func(name string, from, to gobreaker.State) {
                slog.Warn("circuit breaker state change",
                    "account", name, "from", from.String(), "to", to.String())
            },
        },
    }
}

func (r *BreakerRegistry) Execute(accountID string, req func() (error, error)) (error, error) {
    cb := r.getOrCreate(accountID)
    return cb.Execute(req)
}
```

### Pattern 5: Rate Limiting Middleware (httprate)

**What:** Chi middleware with per-token keying and global fallback.

```go
// Source: https://pkg.go.dev/github.com/go-chi/httprate v0.15.0

// internal/api/middleware/ratelimit.go

// PerTokenRateLimit returns middleware limiting requests per bearer token.
// Returns 429 with Retry-After header automatically.
func PerTokenRateLimit(requestsPerMin int) func(http.Handler) http.Handler {
    return httprate.Limit(
        requestsPerMin,
        time.Minute,
        httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
            // Extract token from Authorization: Bearer <token>
            header := r.Header.Get("Authorization")
            if strings.HasPrefix(header, "Bearer ") {
                return strings.TrimPrefix(header, "Bearer "), nil
            }
            return r.RemoteAddr, nil  // fallback to IP for unauthenticated
        }),
        httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
            response.WriteError(w, http.StatusTooManyRequests, "rate_limited",
                "Request rate limit exceeded. Check Retry-After header.")
        }),
    )
}

// GlobalRateLimit returns middleware limiting total request rate across all clients.
func GlobalRateLimit(requestsPerMin int) func(http.Handler) http.Handler {
    return httprate.LimitAll(requestsPerMin, time.Minute)
}
```

### Pattern 6: Debug Screenshot on Browser Failure (OBS-03)

**What:** Capture page state on error for post-mortem analysis.

```go
// Source: playwright-go v0.5700.1 Page.Screenshot API (verified via pkg.go.dev)

func saveDebugScreenshot(page playwright.Page, accountID, operation string, screenshotDir string) {
    if screenshotDir == "" {
        return
    }
    filename := fmt.Sprintf("%s_%s_%s.png",
        time.Now().Format("20060102-150405"),
        accountID,
        operation,
    )
    path := filepath.Join(screenshotDir, filename)
    _, err := page.Screenshot(&playwright.PageScreenshotOptions{
        Path:     playwright.String(path),
        FullPage: playwright.Bool(true),
    })
    if err != nil {
        slog.Warn("failed to save debug screenshot", "path", path, "err", err)
        return
    }
    slog.Info("debug screenshot saved", "path", path, "account", accountID, "operation", operation)
}
```

### Pattern 7: Jitter for Inter-Operation Delay (BROWSER-08)

**What:** The existing `minOpDelay` in `SessionManager` is a fixed delay. Add jitter to spread operations.

```go
// The existing SessionManager.minOpDelay is deterministic.
// Replace the fixed sleep with a jittered sleep.
// Uses math/rand (no import needed beyond standard library).

// In session.go, replace:
//   time.After(sm.minOpDelay - elapsed)
// With:
//   jitter := time.Duration(rand.Int63n(int64(sm.maxOpDelay - sm.minOpDelay)))
//   delay := sm.minOpDelay + jitter - elapsed
//   if delay > 0 { time.After(delay) }
```

Config: `MIN_OPERATION_DELAY_SEC` (already exists) + `MAX_OPERATION_DELAY_SEC` (new, default 3s).

### Anti-Patterns to Avoid

- **Logging credentials from Vault responses:** `secret.Data["password"]` must never appear in log statements. Follow existing SEC-03 pattern from `EnvProvider`.
- **Fetching Vault at startup for all accounts:** Violates VAULT-02. Always lazy-fetch on first use per account.
- **Sharing a single circuit breaker across accounts:** Circuit breakers are per-account; a failure storm on one account must not trip the breaker for a healthy account.
- **Using `interface{}` in gobreaker v2:** v2 uses generics — use `gobreaker.CircuitBreaker[error]` for operations that return only an error.
- **Rate-limiting by IP behind a reverse proxy:** Use token-based keying to correctly identify individual API clients, not IP (which may be the proxy).
- **Blocking on Vault at every request:** Cache hits must never call Vault. Cache miss calls Vault. Vault outage serves stale cache if available.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Circuit breaker state machine | Custom open/half-open/closed logic | `sony/gobreaker/v2` | Half-open probe logic, thread safety, state callbacks are complex; 10 years of production hardening |
| Retry with backoff and jitter | `time.Sleep` loops with rand | `sethvargo/go-retry` | Already in go.mod; handles context cancellation, jitter algorithms, max duration caps |
| HTTP 429 with Retry-After header | Manual header calculation | `go-chi/httprate` | Window reset calculation, sliding window counter, header RFC compliance are non-trivial |
| Vault KV v2 path construction | Manual `logical.Read("secret/data/...")` | `client.KVv2(mount).Get(ctx, path)` | KV v2 uses `data/` prefix for reads, `metadata/` for metadata — easy to get wrong manually |
| AppRole token renewal | Custom TTL tracking goroutine | `api.LifetimeWatcher` (v2 requirements only) | Phase 4 requirement is token + AppRole auth only; renewal is V2-VAULT-01 and out of scope |

**Key insight:** Every item in this table represents a class of subtle bugs that have caused production incidents in other projects. The libraries have been hardened over years; the hand-rolled alternatives inevitably miss edge cases.

---

## Common Pitfalls

### Pitfall 1: Vault KV v2 Path Structure
**What goes wrong:** Calling `client.Logical().Read("secret/dns-he-net/prod")` on a KV v2 mount returns nothing or a confusing error. KV v2 requires the path to include `data/` (read) or `metadata/` (metadata) sub-prefix.
**Why it happens:** KV v1 and v2 have different path schemas. The `client.KVv2(mount).Get(ctx, path)` helper handles this automatically.
**How to avoid:** Always use `client.KVv2(mountPath).Get(ctx, secretPath)` — never construct raw `secret/data/...` paths manually.
**Warning signs:** `client.Logical().Read()` returns nil secret without error on a KV v2 mount.

### Pitfall 2: Circuit Breaker Shared Across Accounts
**What goes wrong:** A circuit breaker shared across all accounts opens when account A has failures, blocking operations for healthy account B.
**Why it happens:** Treating circuit breaker as a global singleton.
**How to avoid:** Use a registry (map[accountID]*CircuitBreaker) with lazy initialization. Each account gets its own independent circuit breaker.
**Warning signs:** One account's DNS operations timing out blocks all other accounts.

### Pitfall 3: Vault Credential Cache Race on Expiry
**What goes wrong:** Multiple goroutines detect cache miss simultaneously, all call Vault, creating N parallel fetches for the same account.
**Why it happens:** Check-then-act is not atomic with just RLock. Cache check and cache write must be atomic.
**How to avoid:** Use double-checked locking: RLock for read, if miss then Lock for write with re-check. Or use `sync.Map` with `LoadOrStore`.
**Warning signs:** Vault audit logs show burst of identical KV reads for the same path.

### Pitfall 4: Rate Limiting Applied Before Auth Middleware
**What goes wrong:** Rate limiter runs before auth, cannot key by token (no claims available yet), falls back to IP-based limiting.
**Why it happens:** Middleware order in chi router matters.
**How to avoid:** Register global rate limit before auth (DDoS protection), but per-token rate limit AFTER BearerAuth middleware (needs token in context).
**Warning signs:** All tokens share the same rate limit bucket, or unauthenticated requests are counted against authenticated limits.

### Pitfall 5: Retry Amplifying Vault Outage
**What goes wrong:** Retry logic wraps credential fetch from Vault; on Vault outage, 3 retries × N concurrent requests = thundering herd against Vault.
**Why it happens:** Retry is applied indiscriminately to all errors.
**How to avoid:** Only retry transient browser errors (timeouts, session expiry). Do NOT retry Vault fetch errors — use stale cache instead (VAULT-05 pattern).
**Warning signs:** Vault logs show burst of requests during an outage event.

### Pitfall 6: Screenshot Directory Not Created
**What goes wrong:** `page.Screenshot()` fails because the directory doesn't exist, logging an error about a missing screenshot instead of the actual browser failure.
**Why it happens:** `os.WriteFile` and similar don't create parent directories.
**How to avoid:** Call `os.MkdirAll(screenshotDir, 0750)` at startup if `SCREENSHOT_DIR` is configured.
**Warning signs:** "no such file or directory" errors in screenshot logs on first failure.

### Pitfall 7: Docker Image — playwright-go vs chromedp/headless-shell
**What goes wrong:** The requirement says `chromedp/headless-shell` as base image, but `chromedp/headless-shell` is designed for the `chromedp` Go library, not playwright-go. Using it as a playwright-go base requires manual playwright browser installation which defeats the purpose.
**Why it happens:** The requirement was written before the project settled on playwright-go over chromedp.
**How to avoid:** Keep the existing `ubuntu:noble` base image approach (already working in Dockerfile). The requirement text should be updated to read "ubuntu:noble with playwright chromium" — functionally equivalent to `chromedp/headless-shell` at similar image size.
**Warning signs:** Playwright failing to find Chromium executable in `chromedp/headless-shell` image.

---

## Code Examples

Verified patterns from official sources:

### VaultProvider: Token Auth Startup

```go
// Source: https://pkg.go.dev/github.com/hashicorp/vault/api v1.22.0

config := api.DefaultConfig()  // reads VAULT_ADDR env automatically
client, err := api.NewClient(config)
if err != nil {
    return fmt.Errorf("vault client init: %w", err)
}
client.SetToken(os.Getenv("VAULT_TOKEN"))

// Health check at startup (VAULT-04)
health, err := client.Sys().Health()
if err != nil || !health.Initialized || health.Sealed {
    slog.Warn("vault not healthy at startup", "sealed", health.Sealed)
}
```

### VaultProvider: AppRole Auth

```go
// Source: https://pkg.go.dev/github.com/hashicorp/vault/api/auth/approle v0.11.0
// and https://github.com/hashicorp/vault-examples/blob/main/examples/auth-methods/approle/go/example.go

secretID := &approle.SecretID{FromString: os.Getenv("VAULT_APPROLE_SECRET_ID")}
appRoleAuth, err := approle.NewAppRoleAuth(
    os.Getenv("VAULT_APPROLE_ROLE_ID"),
    secretID,
)
if err != nil {
    return fmt.Errorf("vault approle init: %w", err)
}
authInfo, err := client.Auth().Login(ctx, appRoleAuth)
if err != nil {
    return fmt.Errorf("vault approle login: %w", err)
}
// Token is automatically set on the client after Login
_ = authInfo
```

### VaultProvider: KV v2 Read

```go
// Source: https://pkg.go.dev/github.com/hashicorp/vault/api v1.22.0
// client.KVv2(mountPath) handles "data/" prefix automatically

secret, err := client.KVv2("secret").Get(ctx, fmt.Sprintf("dns-he-net/%s", accountID))
if err != nil {
    return nil, fmt.Errorf("vault kv get for account %q: %w", accountID, err)
}
if secret == nil || secret.Data == nil {
    return nil, fmt.Errorf("no secret found at path for account %q", accountID)
}
username, _ := secret.Data["username"].(string)
password, _ := secret.Data["password"].(string)
// SECURITY (SEC-03): Never log password value
```

### Circuit Breaker: Execute

```go
// Source: https://pkg.go.dev/github.com/sony/gobreaker/v2 v2.4.0

cb := gobreaker.NewCircuitBreaker[error](gobreaker.Settings{
    Name:        "account-" + accountID,
    MaxRequests: 1,
    Timeout:     30 * time.Second,
    ReadyToTrip: func(counts gobreaker.Counts) bool {
        return counts.ConsecutiveFailures >= 5  // configurable
    },
    OnStateChange: func(name string, from, to gobreaker.State) {
        slog.Warn("circuit breaker state change", "account", name,
            "from", from.String(), "to", to.String())
    },
})

// Execute wraps the operation
result, err := cb.Execute(func() (error, error) {
    opErr := sm.WithAccount(ctx, accountID, op)
    return opErr, opErr
})
if errors.Is(err, gobreaker.ErrOpenState) {
    return ErrCircuitOpen  // maps to 503
}
```

### Retry: Browser Operation

```go
// Source: https://pkg.go.dev/github.com/sethvargo/go-retry v0.3.0
// Already in go.mod as indirect dependency

b := retry.NewExponential(500 * time.Millisecond)
b = retry.WithJitter(200*time.Millisecond, b)
b = retry.WithMaxRetries(3, b)

err := retry.Do(ctx, b, func(ctx context.Context) error {
    if err := sm.WithAccount(ctx, accountID, op); err != nil {
        if isTransientBrowserError(err) {
            return retry.RetryableError(err)
        }
        return err  // permanent error: stop immediately
    }
    return nil
})
```

### Rate Limiting: chi Router Registration

```go
// Source: https://pkg.go.dev/github.com/go-chi/httprate v0.15.0

r := chi.NewRouter()

// Global rate limit (before auth — DDoS protection)
r.Use(httprate.LimitAll(1000, time.Minute))

// Auth middleware
r.Use(middleware.BearerAuth(db, secret))

// Per-token rate limit (after auth — needs token in request)
r.Use(httprate.Limit(
    100,  // configurable
    time.Minute,
    httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
        header := r.Header.Get("Authorization")
        return strings.TrimPrefix(header, "Bearer "), nil
    }),
    httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
        response.WriteError(w, http.StatusTooManyRequests, "rate_limited",
            "Too many requests. Check the Retry-After header.")
    }),
))
```

### Debug Screenshot

```go
// Source: playwright-go v0.5700.1 Page interface (verified via pkg.go.dev)

func SaveDebugScreenshot(page playwright.Page, dir, accountID, operation string) {
    if dir == "" || page == nil {
        return
    }
    if err := os.MkdirAll(dir, 0750); err != nil {
        slog.Warn("cannot create screenshot dir", "dir", dir, "err", err)
        return
    }
    name := fmt.Sprintf("%s-%s-%s.png",
        time.Now().Format("20060102-150405"),
        accountID,
        operation,
    )
    path := filepath.Join(dir, name)
    if _, err := page.Screenshot(&playwright.PageScreenshotOptions{
        Path:     playwright.String(path),
        FullPage: playwright.Bool(true),
    }); err != nil {
        slog.Warn("debug screenshot failed", "path", path, "err", err)
        return
    }
    slog.Info("debug screenshot saved", "path", path)
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `vault/api` v1 manual path construction | `client.KVv2(mount).Get/Put` helper | Vault API v1.14+ | Handles `data/` prefix, version handling automatically |
| `gobreaker` v1 `interface{}` | `gobreaker/v2` generics `CircuitBreaker[T]` | v2.4.0 (Jan 2026) | Type-safe, no casting |
| `NewRenewer` Vault token renewal | `NewLifetimeWatcher` | Vault API v1.x | NewRenewer deprecated; LifetimeWatcher is current (V2 scope) |
| `ubuntu:latest` in Dockerfile | `ubuntu:noble` (24.04 LTS) | 2024 | Pinned LTS base for reproducibility |

**Deprecated/outdated:**
- `vault.NewRenewer()`: deprecated, use `vault.NewLifetimeWatcher()` — but token renewal is V2-VAULT-01 (out of scope for Phase 4)
- `gobreaker` v1: superseded by v2 with generics; v1 remains on pkg.go.dev but v2 is the current module

---

## Configuration Additions

New environment variables needed for Phase 4:

| Env Var | Default | Purpose | Requirement |
|---------|---------|---------|-------------|
| `VAULT_ADDR` | (none) | Vault server URL e.g. `https://vault:8200` | VAULT-01 |
| `VAULT_AUTH_METHOD` | `token` | `token` or `approle` | VAULT-06 |
| `VAULT_TOKEN` | (none) | Token for token auth method | VAULT-06 |
| `VAULT_APPROLE_ROLE_ID` | (none) | Role ID for AppRole auth | VAULT-06 |
| `VAULT_APPROLE_SECRET_ID` | (none) | Secret ID for AppRole auth | VAULT-06 |
| `VAULT_MOUNT_PATH` | `secret` | KV v2 mount path | VAULT-01 |
| `VAULT_SECRET_PATH_TMPL` | `dns-he-net/%s` | Path template (account ID substituted) | VAULT-01 |
| `VAULT_CREDENTIAL_TTL_SEC` | `300` | Credential cache TTL in seconds | VAULT-03 |
| `MAX_OPERATION_DELAY_SEC` | `3.0` | Max jitter upper bound for inter-op delay | BROWSER-08 |
| `CIRCUIT_BREAKER_MAX_FAILURES` | `5` | Consecutive failures before open | RES-03 |
| `CIRCUIT_BREAKER_TIMEOUT_SEC` | `30` | Open state duration before half-open probe | RES-03 |
| `RATE_LIMIT_PER_TOKEN_RPM` | `100` | Per-token requests per minute | RES-02 |
| `RATE_LIMIT_GLOBAL_RPM` | `1000` | Global requests per minute | RES-02 |
| `SCREENSHOT_DIR` | `` (disabled) | Directory for debug screenshots; empty = disabled | OBS-03 |

The existing `HE_ACCOUNTS` env var becomes optional when Vault is configured. Both should remain supported for backward compatibility during migration.

---

## Docker Image Decision

The existing Dockerfile uses `ubuntu:noble` as the final stage and installs playwright + Chromium via `playwright install --with-deps chromium`. This is tested and working.

The requirement OPS-05 mentions `chromedp/headless-shell` as the base, but:
- `chromedp/headless-shell` is designed for the `chromedp` library, not playwright-go
- playwright-go needs to download/manage its own Chromium browser binaries
- The existing ubuntu:noble approach produces a ~350-400MB image (similar to `microsoft/playwright:noble`)
- Switching to `chromedp/headless-shell` would require complex workarounds to make playwright-go find the pre-installed Chromium

**Recommendation for planner:** Keep the existing `ubuntu:noble` Dockerfile approach. Add a `docker-build` target to the Makefile. Update the requirement description to note "ubuntu:noble + playwright chromium" as the production Docker base. The ~150MB estimate in the requirement was optimistic; actual size is 350-400MB with Chromium.

---

## Open Questions

1. **Vault credential path for account registration**
   - What we know: VAULT-01 says `secret/data/dns-he-net/{account-id}`. Accounts are registered via `POST /api/v1/accounts` which currently only stores metadata in SQLite.
   - What's unclear: Does account registration (ACCT-01) also write credentials to Vault? Or is Vault purely read-only from the service's perspective (operator pre-populates Vault)?
   - Recommendation: Vault should be read-only from the service. The operator pre-populates credentials in Vault using Vault CLI/UI. The service only reads. This avoids needing write permission on the Vault path and keeps credential management out of the API surface.

2. **HE_ACCOUNTS backward compatibility after Vault**
   - What we know: Current config requires `HE_ACCOUNTS,required,notEmpty`. Phases 1-3 all use this.
   - What's unclear: Should `HE_ACCOUNTS` remain required when `VAULT_ADDR` is set?
   - Recommendation: Make `HE_ACCOUNTS` optional when `VAULT_ADDR` is configured. The credential provider becomes Vault-backed. Document the migration path in config loading.

3. **Circuit breaker wrapping layer**
   - What we know: `SessionManager.WithAccount` is the central browser operation entry point.
   - What's unclear: Should circuit breaker wrap `WithAccount` in a new middleware layer, or be embedded in `WithAccount` itself?
   - Recommendation: New `resilience` package wraps `SessionManager.WithAccount`. Keeps circuit breaker logic separate from session management. `handlers` call the resilience wrapper, which calls `SessionManager`.

---

## Sources

### Primary (HIGH confidence)

- `pkg.go.dev/github.com/hashicorp/vault/api` v1.22.0 — KVv2 API, Client, Auth, SetToken
- `pkg.go.dev/github.com/hashicorp/vault/api/auth/approle` v0.11.0 — NewAppRoleAuth, SecretID, LoginOption
- `github.com/hashicorp/vault-examples/blob/main/examples/auth-methods/approle/go/example.go` — AppRole + KVv2 complete example
- `pkg.go.dev/github.com/sony/gobreaker/v2` v2.4.0 — Settings, CircuitBreaker[T], States, ErrOpenState
- `pkg.go.dev/github.com/go-chi/httprate` v0.15.0 — LimitAll, Limit, WithKeyFuncs, Retry-After behavior
- `pkg.go.dev/github.com/sethvargo/go-retry` v0.3.0 — NewExponential, WithJitter, WithMaxRetries, RetryableError
- `pkg.go.dev/github.com/playwright-community/playwright-go` v0.5700.1 — Page.Screenshot, PageScreenshotOptions

### Secondary (MEDIUM confidence)

- `playwright.dev/docs/docker` — Official Playwright Docker recommendations (ubuntu:noble, not Alpine)
- `developer.hashicorp.com/vault/docs/concepts/lease` — Vault lease/TTL behavior confirmed
- WebSearch: "golang vault api token renewal LifetimeWatcher" — confirmed `NewLifetimeWatcher` is current API, `NewRenewer` deprecated

### Tertiary (LOW confidence)

- Docker image size (~350-400MB) — estimated from known Chromium binary sizes, not measured directly

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries verified via pkg.go.dev with exact versions
- Architecture: HIGH — patterns derived from official examples and existing project conventions
- Pitfalls: MEDIUM — some from official docs, some from cross-referencing community sources
- Docker image decision: MEDIUM — based on official playwright docs + existing working Dockerfile

**Research date:** 2026-02-28
**Valid until:** 2026-03-30 (libraries are stable; Vault API changes slowly)

# Phase 2: API Layer + Authentication - Research

**Researched:** 2026-02-28
**Domain:** Go REST API — chi router, JWT bearer tokens, RBAC, slog, graceful shutdown
**Confidence:** HIGH

---

## Summary

Phase 2 adds the HTTP API layer on top of the Phase 1 foundation. The three plans cover:
bearer token issuance and validation (02-01), chi router with auth middleware and account/token
endpoints (02-02), and health endpoint, structured request logging, and graceful HTTP server
shutdown (02-03).

The Go ecosystem has a clear, stable standard stack for this domain. `github.com/go-chi/chi/v5`
(v5.2.5) is the idiomatic lightweight router with zero external dependencies beyond stdlib.
`github.com/golang-jwt/jwt/v5` (v5.3.1) is the community-maintained successor to the original
`dgrijalva/jwt-go` and is the de-facto Go JWT library. Both are absent from the current `go.mod`
and must be added. `github.com/go-chi/httplog/v3` (v3.3.0) provides slog-based HTTP request
logging with a RequestLogger middleware; however, since the project already uses chi's built-in
`middleware.RequestID` and the project's slog handler is configured in main.go, httplog/v3 is an
optional convenience — the research documents both approaches.

The token design uses JWT HS256 with embedded claims (account_id, role, jti) + a per-request
revocation check against the SQLite `tokens` table. This satisfies TOKEN-05's instant revocation
requirement without adding a cache layer. Raw token bytes are generated with `crypto/rand.Read`
(32 bytes → hex-encoded 64-char string displayed once), and the SHA-256 hash of the raw token
is stored — never the raw token itself (TOKEN-02, SEC-02).

**Primary recommendation:** Add chi v5.2.5 and golang-jwt/jwt v5.3.1 to go.mod. Use a custom
typed context key for JWT claims injection. Store tokens as SHA-256 hashes. Use chi's built-in
`middleware.RequestID` + `httplog.SetAttrs` for structured request-scoped logging.

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| TOKEN-01 | Operator can issue a bearer token scoped to a specific account with role admin or viewer | golang-jwt/jwt/v5: NewWithClaims with custom claims struct; token issuance handler pattern |
| TOKEN-02 | Tokens are cryptographically random (32 bytes), displayed once at creation, stored as SHA-256 hash | crypto/rand.Read(32 bytes) + hex.EncodeToString; crypto/sha256.Sum256; never store plaintext |
| TOKEN-03 | Multiple tokens per account, each with optional human-readable label | tokens table schema; label column nullable TEXT |
| TOKEN-04 | Tokens have optional expiry date; expired tokens rejected by auth middleware | jwt.RegisteredClaims.ExpiresAt; also check expires_at in DB row for non-JWT expiry path |
| TOKEN-05 | Tokens can be revoked by ID; revoked tokens immediately rejected on next request | jti in JWT claims; DB lookup on every request; tokens table revoked_at nullable column |
| TOKEN-06 | Operator can list all tokens for an account (label, role, dates — never token value) | SELECT query on tokens table; never return token_hash in API response |
| TOKEN-07 | viewer role tokens: GET only; admin tokens: all methods | RBAC middleware: check role from context claims; allow GET for viewer, all for admin |
| ACCT-01 | Operator can register an account (name + Vault path); metadata in SQLite only | POST /api/v1/accounts handler; INSERT into accounts table |
| ACCT-02 | Operator can list all registered accounts (metadata only, no credentials) | GET /api/v1/accounts; SELECT from accounts; never return password |
| ACCT-03 | Operator can remove an account, closing its browser session and deleting metadata | DELETE /api/v1/accounts/{id}; sm.Close(accountID) + DELETE from accounts table |
| ACCT-04 | Token scoped to account A cannot access account B | Auth middleware: extract account_id from JWT claims; handlers validate param matches claim |
| API-01 | All endpoints prefixed with /api/v1/ | chi Route group: r.Route("/api/v1", ...) |
| API-02 | All request/response bodies are JSON with Content-Type: application/json | chi middleware.AllowContentType; json.NewEncoder(w).Encode(resp) pattern |
| API-03 | Proper HTTP status codes returned | Documented status code map; writeError helper returning JSON |
| API-04 | Error responses: {"error": "...", "code": "..."} | writeError(w, status, code, message) helper function |
| API-07 | All authenticated endpoints require Authorization: Bearer <token> header | Auth middleware: strings.TrimPrefix(header, "Bearer ") |
| OPS-01 | GET /healthz returns JSON: service status, browser pool, Vault, SQLite | db.PingContext; launcher.IsConnected(); fixed JSON struct |
| OPS-02 | All log output uses log/slog in structured JSON format with request_id, account_id | chi middleware.RequestID + httplog.SetAttrs for account_id injection |
| OPS-04 | Service handles SIGTERM/SIGINT: stop accepting, drain in-flight, close sessions, close SQLite | http.Server.Shutdown(ctx with timeout); then defer launcher.Close() + defer sm.Close() + defer db.Close() |
| SEC-01 | dns.he.net credentials never in SQLite, logs, API responses, error messages | account handlers write only metadata; never log HEAccountsJSON |
| SEC-02 | Bearer tokens stored as SHA-256 hashes; plaintext returned only at creation | sha256.Sum256([]byte(rawToken)); hex.EncodeToString; return raw only in Create response |
| SEC-04 | All API input validated and sanitized before use in browser form fields | Input validation in handlers: check non-empty, no special chars; return 400 on invalid |
</phase_requirements>

---

## Standard Stack

### Core — MUST ADD to go.mod

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/go-chi/chi/v5` | v5.2.5 | HTTP router: middleware chain, route groups, URL params | De-facto idiomatic Go router; zero external deps; 100% net/http compatible |
| `github.com/golang-jwt/jwt/v5` | v5.3.1 | JWT sign + validate HS256; custom claims struct | Official successor to dgrijalva/jwt-go; well-maintained; v5 API cleaner than v4 |

### Supporting — Already Available in stdlib (Go 1.25)

| Package | Purpose | Note |
|---------|---------|------|
| `crypto/rand` | Secure random token bytes (32 bytes) | `rand.Read(b)` — always use this, not `math/rand` |
| `crypto/sha256` | Hash raw token for DB storage | `sha256.Sum256([]byte(rawToken))` |
| `encoding/hex` | Encode random bytes and hash to printable string | `hex.EncodeToString(b)` |
| `encoding/json` | Encode/decode request and response bodies | Use `json.NewDecoder(r.Body).Decode(&req)` + `json.NewEncoder(w).Encode(resp)` |
| `net/http` | HTTP server, `http.Server.Shutdown` for graceful stop | Phase 2 wires the server |
| `log/slog` | Structured JSON logging (already configured in main.go) | Already set up; OPS-02 adds request-scoped attributes |

### Optional (Avoid Adding Unless Needed)

| Library | Version | Purpose | Decision |
|---------|---------|---------|----------|
| `github.com/go-chi/httplog/v3` | v3.3.0 | slog-based HTTP request logger middleware | SKIP for now — chi's `middleware.Logger` + chi's `middleware.RequestID` + manual slog calls are sufficient. httplog adds value if ECS/OTEL schema is needed (Phase 5+). |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| golang-jwt/jwt/v5 | Opaque tokens (crypto/rand → SHA-256 → DB lookup) | Opaque tokens are simpler but put ALL metadata in DB; JWT embeds account_id + role in token so auth middleware doesn't need a full DB round-trip for every field — only jti revocation lookup |
| go-chi/chi/v5 | gorilla/mux, httprouter, stdlib mux | chi is the community standard for new Go services; gorilla is maintenance-mode; httprouter lacks middleware chain; stdlib mux (Go 1.22) lacks built-in middleware composition |
| crypto/rand.Read | crypto/rand.Text() | Text() is cleaner but returns base32 (26 chars = 130 bits). Read(32 bytes) → hex gives 256 bits, which is the TOKEN-02 specified "32 bytes". Use Read(32 bytes). |

### Installation

```bash
cd C:/Users/vladimir/Documents/Development/dns-he-net-automation
go get github.com/go-chi/chi/v5@v5.2.5
go get github.com/golang-jwt/jwt/v5@v5.3.1
```

---

## Architecture Patterns

### Recommended Package Structure (Phase 2 additions)

```
internal/
├── api/
│   ├── router.go          # chi router setup, middleware stack, route registration
│   ├── middleware/
│   │   ├── auth.go        # BearerAuth middleware: extract → parse JWT → revocation check → inject claims
│   │   └── rbac.go        # RequireRole middleware: read claims from context, enforce admin/viewer
│   ├── handler/
│   │   ├── accounts.go    # POST/GET/DELETE /api/v1/accounts handlers
│   │   ├── tokens.go      # POST/GET/DELETE /api/v1/accounts/{id}/tokens handlers
│   │   └── health.go      # GET /healthz handler
│   └── response/
│       └── errors.go      # writeError(w, status, code, msg) helper; JSON error structs
├── token/
│   └── service.go         # IssueToken, ValidateToken, RevokeToken business logic
└── store/
    ├── sqlite.go          # (existing)
    ├── migrations/
    │   ├── 001_init.sql   # (existing)
    │   └── 002_tokens.sql # NEW: tokens table migration
    └── account_store.go   # SQL helpers for accounts CRUD
    └── token_store.go     # SQL helpers for tokens CRUD
```

### Pattern 1: Custom Claims Struct (JWT)

Use a custom claims struct embedding `jwt.RegisteredClaims`. This gives type-safe access to
claims in the auth middleware without map key lookups.

```go
// Source: verified against pkg.go.dev/github.com/golang-jwt/jwt/v5
// internal/token/claims.go

import "github.com/golang-jwt/jwt/v5"

// Claims is the JWT payload for dns-he-net-automation bearer tokens.
type Claims struct {
    AccountID string `json:"account_id"`
    Role      string `json:"role"` // "admin" or "viewer"
    Label     string `json:"label,omitempty"`
    jwt.RegisteredClaims
}
// RegisteredClaims provides: ID (jti), ExpiresAt, IssuedAt, Issuer, Subject
```

### Pattern 2: Token Issuance (IssueToken)

```go
// Source: verified against pkg.go.dev/github.com/golang-jwt/jwt/v5
// internal/token/service.go

import (
    "crypto/rand"
    "crypto/sha256"
    "encoding/hex"
    "time"

    "github.com/golang-jwt/jwt/v5"
    "github.com/google/uuid"
)

func IssueToken(accountID, role, label string, expiresAt *time.Time, secret []byte) (rawToken string, tokenHash string, jti string, err error) {
    // 1. Generate 32 cryptographically random bytes for the raw token value.
    // The raw token is the bearer credential shown to the user once.
    rawBytes := make([]byte, 32)
    if _, err = rand.Read(rawBytes); err != nil {
        return
    }
    rawToken = hex.EncodeToString(rawBytes) // 64-char hex string

    // 2. Hash the raw token for DB storage (SEC-02, TOKEN-02).
    hash := sha256.Sum256([]byte(rawToken))
    tokenHash = hex.EncodeToString(hash[:])

    // 3. Build JWT claims. jti is a UUID used for revocation lookup.
    jti = uuid.New().String()
    claims := Claims{
        AccountID: accountID,
        Role:      role,
        Label:     label,
        RegisteredClaims: jwt.RegisteredClaims{
            ID:       jti,
            IssuedAt: jwt.NewNumericDate(time.Now()),
        },
    }
    if expiresAt != nil {
        claims.ExpiresAt = jwt.NewNumericDate(*expiresAt)
    }

    // 4. Sign token with HS256.
    t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    // NOTE: The raw token IS the signed JWT string. We store the SHA-256 of this JWT string.
    rawToken, err = t.SignedString(secret)
    if err != nil {
        return
    }
    hash = sha256.Sum256([]byte(rawToken))
    tokenHash = hex.EncodeToString(hash[:])
    return
}
```

**Important design note:** The "raw token" that is shown to the user once IS the signed JWT
string. The SHA-256 hash of the signed JWT string is what gets stored in the DB. On each request,
the middleware hashes the incoming bearer value and looks it up in the DB to check for revocation.
JWT signature validation handles expiry and tampering. DB lookup handles revocation.

### Pattern 3: Auth Middleware (BearerAuth)

```go
// Source: chi middleware pattern verified against github.com/go-chi/chi docs
// internal/api/middleware/auth.go

type contextKey string
const ClaimsKey contextKey = "jwt_claims"

func BearerAuth(db *sql.DB, secret []byte) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authHeader := r.Header.Get("Authorization")
            if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
                writeError(w, http.StatusUnauthorized, "missing_token", "Authorization header required")
                return
            }
            rawToken := strings.TrimPrefix(authHeader, "Bearer ")

            // Parse and validate JWT signature + expiry.
            var claims Claims
            token, err := jwt.ParseWithClaims(rawToken, &claims,
                func(t *jwt.Token) (any, error) {
                    if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
                        return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
                    }
                    return secret, nil
                },
                jwt.WithValidMethods([]string{"HS256"}),  // Prevent algorithm switching attack
            )
            if err != nil || !token.Valid {
                writeError(w, http.StatusUnauthorized, "invalid_token", "Token is invalid or expired")
                return
            }

            // Revocation check: SHA-256 hash lookup in tokens table (TOKEN-05).
            h := sha256.Sum256([]byte(rawToken))
            tokenHash := hex.EncodeToString(h[:])
            var revokedAt sql.NullTime
            err = db.QueryRowContext(r.Context(),
                `SELECT revoked_at FROM tokens WHERE jti = ? AND token_hash = ?`,
                claims.ID, tokenHash,
            ).Scan(&revokedAt)
            if errors.Is(err, sql.ErrNoRows) {
                writeError(w, http.StatusUnauthorized, "token_not_found", "Token not found")
                return
            }
            if err != nil {
                writeError(w, http.StatusInternalServerError, "db_error", "Internal error")
                return
            }
            if revokedAt.Valid {
                writeError(w, http.StatusUnauthorized, "token_revoked", "Token has been revoked")
                return
            }

            // Inject claims into context.
            ctx := context.WithValue(r.Context(), ClaimsKey, &claims)
            // Add account_id to structured log for this request (OPS-02).
            // httplog.SetAttrs(ctx, slog.String("account_id", claims.AccountID))
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### Pattern 4: RBAC Middleware (RequireRole)

```go
// internal/api/middleware/rbac.go

func RequireAdmin(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        claims, ok := r.Context().Value(ClaimsKey).(*Claims)
        if !ok || claims == nil {
            writeError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
            return
        }
        if claims.Role != "admin" {
            writeError(w, http.StatusForbidden, "insufficient_role", "Admin role required")
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**RBAC rule (TOKEN-07):**
- `GET` endpoints: require `BearerAuth` only (viewer and admin both allowed)
- `POST`, `PUT`, `DELETE` endpoints: require `BearerAuth` + `RequireAdmin`
- Account isolation (ACCT-04): after middleware injects claims, each handler checks
  `chi.URLParam(r, "accountID") == claims.AccountID` (unless role is superadmin — not in v1)

### Pattern 5: chi Router Setup

```go
// Source: verified against pkg.go.dev/github.com/go-chi/chi/v5
// internal/api/router.go

func NewRouter(db *sql.DB, sm *browser.SessionManager, secret []byte) http.Handler {
    r := chi.NewRouter()

    // Global middleware (applied to all routes including /healthz).
    r.Use(middleware.RequestID)       // Injects X-Request-Id; accessible via middleware.GetReqID(ctx)
    r.Use(middleware.RealIP)          // Sets RemoteAddr from X-Real-IP / X-Forwarded-For
    r.Use(middleware.Logger)          // chi built-in request log (uses chi's own logger, not slog)
    r.Use(middleware.Recoverer)       // Recover from panics, return 500

    // Health check — no auth required.
    r.Get("/healthz", healthHandler(db, sm))

    // Authenticated API routes.
    r.Route("/api/v1", func(r chi.Router) {
        r.Use(BearerAuth(db, secret))  // All /api/v1/* require valid token

        // Account management — admin only for mutations.
        r.Route("/accounts", func(r chi.Router) {
            r.Get("/", listAccountsHandler(db))
            r.With(RequireAdmin).Post("/", createAccountHandler(db))
            r.Route("/{accountID}", func(r chi.Router) {
                r.With(RequireAdmin).Delete("/", deleteAccountHandler(db, sm))

                // Token management — admin only for mutations.
                r.Route("/tokens", func(r chi.Router) {
                    r.Get("/", listTokensHandler(db))
                    r.With(RequireAdmin).Post("/", issueTokenHandler(db, secret))
                    r.With(RequireAdmin).Delete("/{tokenID}", revokeTokenHandler(db))
                })
            })
        })
    })

    return r
}
```

### Pattern 6: Error Response Helper

```go
// Source: API-04 requirement; JSON schema {"error": "...", "code": "..."}
// internal/api/response/errors.go

type ErrorResponse struct {
    Error string `json:"error"`
    Code  string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(ErrorResponse{Error: message, Code: code})
}
```

### Pattern 7: Graceful Shutdown with chi

```go
// Source: chi official example github.com/go-chi/chi/blob/master/_examples/graceful/main.go
// cmd/server/main.go — replaces the current <-ctx.Done() block

// Phase 2 replaces the TODO comment in main.go with this pattern:

srv := &http.Server{
    Addr:    fmt.Sprintf(":%d", cfg.Port),
    Handler: api.NewRouter(db, sm, []byte(cfg.JWTSecret)),
}

// Run HTTP server in background goroutine.
go func() {
    slog.Info("http server listening", "port", cfg.Port)
    if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        slog.Error("http server error", "error", err)
        os.Exit(1)
    }
}()

// Block until signal.
<-ctx.Done()
stop() // release signal resources

slog.Info("shutting down http server")
shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := srv.Shutdown(shutdownCtx); err != nil {
    slog.Error("http server shutdown error", "error", err)
}
// After Shutdown() returns, the deferred db.Close(), sm.Close(), launcher.Close() run.
```

### Pattern 8: Health Endpoint

```go
// Source: OPS-01 requirement; db.PingContext + launcher.IsConnected()
// internal/api/handler/health.go

type HealthResponse struct {
    Status   string            `json:"status"`  // "ok" or "degraded"
    Checks   map[string]string `json:"checks"`
}

func healthHandler(db *sql.DB, launcher *browser.Launcher) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        checks := map[string]string{}
        status := "ok"

        // SQLite connectivity check.
        if err := db.PingContext(r.Context()); err != nil {
            checks["sqlite"] = "error: " + err.Error()
            status = "degraded"
        } else {
            checks["sqlite"] = "ok"
        }

        // Browser launcher connectivity check.
        if launcher.IsConnected() {
            checks["browser"] = "ok"
        } else {
            checks["browser"] = "not connected"
            status = "degraded"
        }

        httpStatus := http.StatusOK
        if status == "degraded" {
            httpStatus = http.StatusServiceUnavailable
        }

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(httpStatus)
        _ = json.NewEncoder(w).Encode(HealthResponse{Status: status, Checks: checks})
    }
}
```

### Anti-Patterns to Avoid

- **Storing raw JWT string in DB:** Store SHA-256 hash only; the plaintext token must be
  unreconstructable from DB contents (SEC-02, TOKEN-02).
- **Trusting JWT claims without revocation check:** A valid JWT signature does not mean the
  token is still active. Always check `revoked_at` in the DB (TOKEN-05).
- **Algorithm confusion attacks:** Always pass `jwt.WithValidMethods([]string{"HS256"})` when
  parsing. Without this, an attacker could forge tokens using the "none" algorithm or RS256.
- **Using `interface{}` (untyped) as context key:** Use a typed `type contextKey string` to
  prevent key collisions between packages. chi's middleware uses its own unexported key types.
- **Returning 500 with error details to the client:** Wrap `err.Error()` in server logs; only
  the `code` field in error responses reaches the client.
- **Double-writing headers:** Call `w.WriteHeader(status)` exactly once per request; writing
  to `w` after `WriteHeader` silently drops the body in some cases.
- **Using `middleware.Logger` with slog:** chi's built-in Logger uses `log.Printf` (not slog).
  For slog-compatible request logging, use `httplog.RequestLogger` from `go-chi/httplog/v3` or
  write a custom middleware that calls slog.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| JWT sign + parse | Custom HMAC signing + base64url encoding | `golang-jwt/jwt/v5` | Header/payload/signature encoding, `exp` claim validation, error types — all complex |
| Route parameter extraction | Manual URL string splitting | `chi.URLParam(r, "accountID")` | chi stores URL params in request context already |
| Panic recovery | `defer recover()` in every handler | `middleware.Recoverer` | chi's Recoverer logs stack traces and returns clean 500 |
| Request ID generation | `uuid.New()` in every handler | `middleware.RequestID` | Generates once per request; accessible anywhere via `middleware.GetReqID(ctx)` |
| Content-type enforcement | `r.Header.Get("Content-Type") == ...` in each handler | `middleware.AllowContentType("application/json")` | Chi middleware handles 415 automatically |
| Constant-time comparison | `rawTokenInDB == incomingToken` | `crypto/subtle.ConstantTimeCompare` (or skip: use SHA-256 hash equality in SQL) | Direct string comparison is vulnerable to timing attacks; SHA-256 hash comparison via SQL avoids it entirely |

**Key insight:** The combination of JWT signature validation (golang-jwt/jwt) + SQLite revocation
lookup via jti replaces what would otherwise require a Redis cache, a token introspection service,
and a custom signing system. Keep it simple.

---

## SQLite Schema Additions

### Migration 002: tokens table

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS tokens (
    jti        TEXT PRIMARY KEY,                   -- JWT ID claim; used for revocation lookup
    account_id TEXT NOT NULL
               REFERENCES accounts(id)
               ON DELETE CASCADE,                  -- Remove tokens when account deleted
    role       TEXT NOT NULL CHECK (role IN ('admin', 'viewer')),
    label      TEXT,                               -- Optional human-readable description (TOKEN-03)
    token_hash TEXT NOT NULL UNIQUE,               -- SHA-256(raw_jwt_string) hex-encoded (SEC-02)
    expires_at DATETIME,                           -- NULL means no expiry (TOKEN-04)
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at DATETIME                            -- NULL means not revoked (TOKEN-05)
);

-- Index for revocation lookup by jti (primary key already indexed).
-- Additional index for account-scoped token listing.
CREATE INDEX IF NOT EXISTS idx_tokens_account_id ON tokens(account_id);

-- +goose Down
DROP INDEX IF EXISTS idx_tokens_account_id;
DROP TABLE IF EXISTS tokens;
```

**Key schema decisions:**
- `jti` as PRIMARY KEY: fast O(log n) revocation check without additional index.
- `token_hash UNIQUE`: prevents duplicate token registrations.
- `ON DELETE CASCADE`: deleting an account (ACCT-03) automatically removes all its tokens.
- `expires_at` stores the same expiry as the JWT `exp` claim — allows querying expired tokens
  without parsing JWTs in listing endpoints.
- `role CHECK` constraint: DB-level enforcement prevents invalid role values.

### Config additions needed

The `Config` struct in `internal/config/config.go` is missing `JWTSecret`. Phase 2 must add:

```go
// JWTSecret is the HMAC-SHA256 signing secret for JWT bearer tokens.
// SECURITY: Must be at least 32 characters. Never log this value (SEC-02).
JWTSecret string `env:"JWT_SECRET,required,notEmpty"`
```

**Note:** The existing `Config` has: Port, DBPath, HEAccountsJSON, PlaywrightHeadless,
PlaywrightSlowMo, OperationTimeoutSec, OperationQueueTimeoutSec, LogLevel,
MinOperationDelaySec, SessionMaxAgeSec. None of these cover JWT signing secret.

---

## Common Pitfalls

### Pitfall 1: JWT Algorithm Confusion Attack

**What goes wrong:** Attacker switches algorithm in JWT header from "HS256" to "none" or
"RS256" with the HMAC public key as payload — library validates an unsigned token as valid.

**Why it happens:** jwt/v5 defaults to accepting any algorithm unless restricted.

**How to avoid:** Always pass `jwt.WithValidMethods([]string{"HS256"})` to `jwt.ParseWithClaims`.

**Warning signs:** Any test that omits `WithValidMethods` and still passes.

### Pitfall 2: Skipping Revocation Check for Performance

**What goes wrong:** Auth middleware validates JWT signature but skips DB revocation lookup,
so revoked tokens continue to work until expiry (violates TOKEN-05).

**Why it happens:** "Every request hits DB" feels slow. It is not: SQLite with WAL + indexed
jti lookup is sub-millisecond on the local filesystem.

**How to avoid:** Always do the DB lookup. If performance becomes an issue in Phase 4+,
add an in-memory LRU cache for non-revoked jti values.

**Warning signs:** Revoked tokens not rejected in integration tests.

### Pitfall 3: Writing Headers After Body

**What goes wrong:** Handler calls `json.NewEncoder(w).Encode(data)` (which triggers implicit
`WriteHeader(200)`) before calling `w.WriteHeader(201)` for a Created response — the 201 is
silently dropped.

**Why it happens:** `http.ResponseWriter` sends headers on first write to body.

**How to avoid:** Always set headers and call `WriteHeader` before writing body. Pattern:
```go
w.Header().Set("Content-Type", "application/json")
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(response)
```

**Warning signs:** `201 Created` responses actually return `200 OK` in tests.

### Pitfall 4: Context Value Type Collision

**What goes wrong:** Two packages both store values with context key `"claims"` — one overwrites
the other silently.

**Why it happens:** Using untyped string or int as context key.

**How to avoid:** Use a private type: `type contextKey string; const claimsKey contextKey = "jwt_claims"`.
chi's internal middleware uses unexported types for the same reason.

**Warning signs:** Context value unexpectedly nil despite middleware running.

### Pitfall 5: http.Server.Shutdown vs ListenAndServe Error

**What goes wrong:** `ListenAndServe` returns `http.ErrServerClosed` after `Shutdown()` is
called — if not handled, this looks like a fatal error.

**Why it happens:** `Shutdown()` closes the server cleanly, causing `ListenAndServe` to return
`http.ErrServerClosed`.

**How to avoid:** Check `errors.Is(err, http.ErrServerClosed)` in the goroutine that runs
`ListenAndServe`:
```go
if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
    slog.Error("server error", "error", err)
    os.Exit(1)
}
```

**Warning signs:** Shutdown appears to succeed but logs show "unexpected server error".

### Pitfall 6: Account Isolation Bypass (ACCT-04)

**What goes wrong:** Handler accepts `{accountID}` URL parameter and queries DB without
checking that `accountID == claims.AccountID`.

**Why it happens:** Auth middleware validates the token but doesn't enforce account scope —
that's the handler's job.

**How to avoid:** In every handler that takes an account URL parameter, add:
```go
if chi.URLParam(r, "accountID") != claims.AccountID {
    writeError(w, http.StatusForbidden, "account_mismatch", "Token not scoped to this account")
    return
}
```

**Warning signs:** Integration test where token for account-A accesses account-B endpoints
succeeds when it should return 403.

### Pitfall 7: modernc.org/sqlite DSN with WAL and :memory:

**What goes wrong:** Test uses `:memory:` database and expects WAL mode — always returns
"memory" journal mode. (Already documented in Phase 1 01-01 SUMMARY, repeating for Phase 2.)

**How to avoid:** Token store integration tests that need to verify DB state should use a
temp file database (`t.TempDir()`), not `:memory:`.

---

## Code Examples

### Issue Token (complete flow)

```go
// Source: based on verified golang-jwt/jwt/v5 pkg.go.dev API

func IssueToken(db *sql.DB, accountID, role, label string, expiresAt *time.Time, secret []byte) (string, error) {
    // 32 random bytes = 256 bits of entropy.
    rawBytes := make([]byte, 32)
    if _, err := rand.Read(rawBytes); err != nil {
        return "", fmt.Errorf("generate token bytes: %w", err)
    }

    jti := uuid.New().String()
    claims := Claims{
        AccountID: accountID,
        Role:      role,
        Label:     label,
        RegisteredClaims: jwt.RegisteredClaims{
            ID:       jti,
            IssuedAt: jwt.NewNumericDate(time.Now()),
        },
    }
    if expiresAt != nil {
        claims.ExpiresAt = jwt.NewNumericDate(*expiresAt)
    }

    rawToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
    if err != nil {
        return "", fmt.Errorf("sign token: %w", err)
    }

    // Hash the signed JWT string for DB storage (SEC-02).
    h := sha256.Sum256([]byte(rawToken))
    tokenHash := hex.EncodeToString(h[:])

    _, err = db.ExecContext(context.Background(),
        `INSERT INTO tokens (jti, account_id, role, label, token_hash, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
        jti, accountID, role, label, tokenHash, expiresAt,
    )
    if err != nil {
        return "", fmt.Errorf("store token: %w", err)
    }

    return rawToken, nil // Display to user once; never stored again.
}
```

### Parse JWT and Extract Claims

```go
// Source: pkg.go.dev/github.com/golang-jwt/jwt/v5 — ParseWithClaims example

func parseToken(rawToken string, secret []byte) (*Claims, error) {
    var claims Claims
    token, err := jwt.ParseWithClaims(rawToken, &claims,
        func(t *jwt.Token) (any, error) {
            if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
                return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
            }
            return secret, nil
        },
        jwt.WithValidMethods([]string{"HS256"}),
    )
    if err != nil {
        return nil, err
    }
    if !token.Valid {
        return nil, errors.New("token not valid")
    }
    return &claims, nil
}
```

### chi Route Group with Middleware Chain

```go
// Source: pkg.go.dev/github.com/go-chi/chi/v5 — Route + Use pattern

r.Route("/api/v1", func(r chi.Router) {
    r.Use(BearerAuth(db, secret))

    r.Route("/accounts/{accountID}", func(r chi.Router) {
        r.Get("/tokens", listTokensHandler)             // viewer + admin
        r.With(RequireAdmin).Post("/tokens", issueTokenHandler)  // admin only
        r.With(RequireAdmin).Delete("/tokens/{tokenID}", revokeTokenHandler)
    })
})
```

### SHA-256 Token Hash

```go
// stdlib only — no extra import needed
import (
    "crypto/sha256"
    "encoding/hex"
)

func hashToken(rawToken string) string {
    h := sha256.Sum256([]byte(rawToken))
    return hex.EncodeToString(h[:])
}
```

### Structured Logging with Request ID (OPS-02)

```go
// Source: chi middleware docs — middleware.GetReqID(ctx)

// In auth middleware, after injecting claims:
reqID := middleware.GetReqID(r.Context())
slog.InfoContext(r.Context(), "token validated",
    "request_id", reqID,
    "account_id", claims.AccountID,
    "role", claims.Role,
    "jti", claims.ID,
)
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `dgrijalva/jwt-go` | `golang-jwt/jwt/v5` | 2021 (v4), 2023 (v5) | v4 added context; v5 cleaner error types, `RegisteredClaims` replaces `StandardClaims` |
| `gorilla/mux` | `go-chi/chi/v5` | 2022 (gorilla maintenance-mode) | chi is now the idiomatic lightweight router |
| `log` / `logrus` / `zap` | `log/slog` (stdlib) | Go 1.21 (Aug 2023) | Structured logging in stdlib; no external dep needed |
| `jwt.StandardClaims` | `jwt.RegisteredClaims` | golang-jwt/jwt v4 | `StandardClaims` was deprecated; `RegisteredClaims` has proper typed fields |
| `crypto/rand.Read` + base64 | `crypto/rand.Text()` | Go 1.24 | `Text()` gives 26-char base32 with 130 bits entropy; `Read(32 bytes)` gives 256 bits — use `Read` for TOKEN-02's "32 bytes" requirement |

**Deprecated / outdated — do not use:**
- `dgrijalva/jwt-go`: abandoned, use `golang-jwt/jwt/v5`
- `jwt.StandardClaims`: deprecated in golang-jwt/jwt v4+, use `jwt.RegisteredClaims`
- `chi.URLParam` with `.WithContext`: not needed — chi automatically stores params in context

---

## Open Questions

1. **JWT secret rotation strategy**
   - What we know: `JWT_SECRET` signs all tokens; if it changes, all existing tokens become invalid.
   - What's unclear: The requirements don't specify rotation. Phase 2 uses a single secret.
   - Recommendation: Single `JWT_SECRET` env var for Phase 2. Note in config comment that rotation
     requires re-issuance of all tokens. Do not over-engineer for Phase 2.

2. **Admin token bootstrap (chicken-and-egg)**
   - What we know: Account and token management endpoints require a valid admin token (TOKEN-01,
     ACCT-01). But there are no tokens at startup.
   - What's unclear: How does the first admin token get created?
   - Recommendation: Provide a CLI subcommand (`cmd/server/main.go` flag or separate
     `cmd/admin/main.go`) that prints a bootstrap token directly to stdout without going through
     the HTTP API. Alternatively, accept a `BOOTSTRAP_TOKEN` env var at startup that creates one
     token in the DB if no tokens exist. This is a Phase 2 design decision that affects 02-02.

3. **Account isolation enforcement granularity (ACCT-04)**
   - What we know: TOKEN-01 says tokens are "scoped to a specific account". ACCT-04 says token
     for A cannot see B.
   - What's unclear: Should a superadmin role exist that can manage all accounts? Requirements
     say only `admin` and `viewer` — no superadmin.
   - Recommendation: Strict account scoping in Phase 2. Admin = all operations on their account.
     No cross-account access. Platform-level operations (register account) are a separate concern
     — require a separate bootstrap mechanism (see question 2).

---

## Sources

### Primary (HIGH confidence)

- `pkg.go.dev/github.com/go-chi/chi/v5` — Router interface, middleware chain, URL params, route groups
- `pkg.go.dev/github.com/go-chi/chi/v5/middleware` — RequestID, Recoverer, Timeout, Logger middleware
- `pkg.go.dev/github.com/golang-jwt/jwt/v5` — NewWithClaims, ParseWithClaims, RegisteredClaims, error types
- `github.com/go-chi/chi/blob/master/_examples/graceful/main.go` — Official graceful shutdown example
- `pkg.go.dev/github.com/go-chi/httplog/v3` — RequestLogger, Options, SetAttrs
- `pkg.go.dev/crypto/rand` — Text() (Go 1.24+), Read()
- `pkg.go.dev/crypto/sha256` — Sum256
- Phase 1 SUMMARY files — Config struct fields, store.Open pattern, main.go structure, session manager API

### Secondary (MEDIUM confidence)

- `github.com/go-chi/chi/releases` — Confirmed v5.2.5 as latest (February 5, 2025)
- `github.com/golang-jwt/jwt/releases` — Confirmed v5.3.1 as latest (January 28, 2025)
- `github.com/go-chi/httplog` GitHub — Confirmed v3.3.0 as latest (October 9, 2025)

### Tertiary (LOW confidence)

- WebSearch findings on opaque vs JWT token tradeoffs — cross-verified with requirement TEXT
  (TOKEN-05 requires revocation → DB lookup needed → opaque vs JWT difference is moot for this project)

---

## Metadata

**Confidence breakdown:**
- Standard stack (chi, golang-jwt): HIGH — versions confirmed from official GitHub releases pages
- Token issuance pattern: HIGH — verified against pkg.go.dev API docs
- Auth middleware pattern: HIGH — verified against chi and golang-jwt/jwt/v5 docs
- SQLite schema: HIGH — derived from requirements + existing Phase 1 schema patterns
- Graceful shutdown: HIGH — verified against official chi example
- Health endpoint: HIGH — derived from existing `launcher.IsConnected()` API (Phase 1 SUMMARY)
- httplog/v3 usage: MEDIUM — API verified but library is optional; chi's built-in Logger may suffice

**Research date:** 2026-02-28
**Valid until:** 2026-04-28 (stable libraries; chi and jwt move slowly)

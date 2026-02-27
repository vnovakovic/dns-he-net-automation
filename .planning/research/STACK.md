# Technology Stack

**Project:** dns-he-net-automation
**Researched:** 2026-02-26
**Overall confidence:** HIGH (versions verified via pkg.go.dev)

## Recommended Stack

### Go Version

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| Go | 1.23+ (target 1.24) | Language runtime | Go 1.21+ required for slog; 1.23+ for latest module features. Go 1.24 is current stable as of Feb 2026. |

**Rationale:** Go 1.21 introduced `log/slog` in stdlib. Go 1.23+ gives us range-over-func and improved tooling. Target Go 1.24 for the project, set `go 1.23` as minimum in go.mod for broader compatibility.

---

### Core Framework: HTTP Router

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| go-chi/chi | v5.2.5 | HTTP router and middleware | Lightweight, idiomatic, `net/http` compatible, excellent middleware ecosystem |

**Confidence:** HIGH (verified v5.2.5 published Feb 5, 2026 on pkg.go.dev)

**Rationale:** Chi is the standard choice for Go REST APIs that value stdlib compatibility. It implements `http.Handler` natively, meaning all stdlib middleware works. Unlike Gin (which uses its own context) or Echo, Chi does not lock you into a framework-specific abstraction. For a service that needs clean middleware chains (auth, rate limiting, logging, CORS), Chi's composable router is ideal.

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| Gin (gin-gonic/gin) | Custom `gin.Context` breaks stdlib compatibility. Performance difference irrelevant at this scale. More opinionated than needed. |
| Echo (labstack/echo) | Similar to Gin -- custom context, less stdlib-aligned. Smaller ecosystem than Chi. |
| stdlib `net/http` only | Go 1.22+ ServeMux has pattern matching, but no middleware composition, no route groups, no path parameter extraction as clean as Chi. Would reinvent middleware chaining. |
| Fiber (gofiber/fiber) | Built on fasthttp, not `net/http`. Incompatible with stdlib ecosystem. Overkill for this use case. |

---

### Browser Automation

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| go-rod/rod | v0.116.2 | Headless Chromium automation | Pure Go CDP client, auto-manages Chromium lifecycle, excellent API for form interactions |

**Confidence:** HIGH (verified v0.116.2 is latest on pkg.go.dev, published Jul 2024. Stable despite pre-v1 semver.)

**Rationale:** Rod is the correct choice for this project. It communicates directly with Chromium via the Chrome DevTools Protocol (CDP), is written in pure Go with minimal dependencies (ysmood/goob, gson, got), and has a clean API for the kind of web form automation dns.he.net requires (filling login forms, navigating menus, reading tables, submitting forms).

**Chromium Distribution Strategy (CRITICAL):**

Rod uses a `launcher` package that auto-downloads a compatible Chromium revision to `~/.cache/rod/browser/` on first run. This is the default behavior and works for development, but for production deployment you need explicit control:

| Deployment | Chromium Strategy |
|------------|-------------------|
| **Docker container** | Use `chromedp/headless-shell` as base or multi-stage build with Chromium installed via apt. Set `BROWSER_BIN` or use `launcher.New().Bin("/usr/bin/chromium")` to point Rod at system Chromium. This avoids runtime downloads. |
| **Standalone binary** | Rod auto-downloads on first run to `~/.cache/rod/browser/`. Acceptable for on-prem. Alternatively, document that Chromium/Chrome must be installed and configure Rod via `launcher.New().Bin(path)`. |
| **CI/testing** | Use Rod's default auto-download or provide Chromium via CI image. |

**Docker Chromium base image recommendation:**

```dockerfile
# Multi-stage: use chromedp/headless-shell for minimal Chromium
FROM chromedp/headless-shell:latest AS chrome
FROM golang:1.24-alpine AS builder
# ... build Go binary ...

FROM alpine:3.21
COPY --from=chrome /headless-shell /headless-shell
COPY --from=builder /app/dns-he-net-api /usr/local/bin/
# Rod config: launcher.New().Bin("/headless-shell/headless-shell")
```

The `chromedp/headless-shell` image provides a minimal headless Chromium (~100MB) without full Chrome dependencies. This is the standard approach in the Go headless browser ecosystem.

**Rod configuration for production:**

```go
// Production launcher setup
l := launcher.New().
    Bin("/usr/bin/chromium").        // or /headless-shell/headless-shell in Docker
    Headless(true).
    Set("disable-gpu").
    Set("no-sandbox").               // required in Docker
    Set("disable-dev-shm-usage").    // prevent /dev/shm exhaustion
    Set("disable-software-rasterizer")

url := l.MustLaunch()
browser := rod.New().ControlURL(url).MustConnect()
```

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| chromedp/chromedp | Lower-level CDP bindings. Rod provides a higher-level API better suited for form automation (page.Element, .Input, .Click patterns). chromedp requires more boilerplate for the same tasks. |
| playwright-go | Go port of Playwright. Heavier dependency, requires Node.js runtime for browser management. Unnecessary complexity for this use case. |
| Direct HTTP scraping (net/http + goquery) | dns.he.net may use JavaScript for form handling, CSRF tokens, or dynamic rendering. Headless browser is more resilient to UI changes. PROJECT.md explicitly chose Rod for this reason. |

---

### Database

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| modernc.org/sqlite | v1.46.1 | SQLite driver (pure Go, CGo-free) | No CGo dependency means clean cross-compilation and simpler Docker builds |

**Confidence:** HIGH (verified v1.46.1 published Feb 18, 2026, embeds SQLite 3.51.2)

**Rationale:** `modernc.org/sqlite` is a pure Go translation of SQLite, meaning zero CGo dependency. This is critical for this project because:

1. **Cross-compilation works** -- build Linux binaries on Windows/Mac without CGo toolchain
2. **Docker builds are simpler** -- no gcc/musl-dev in builder stage
3. **Static binary** -- the Go binary is fully self-contained
4. **Performance is sufficient** -- for a service managing DNS records (low write volume), the ~20% CGo performance gap is irrelevant

Use it via `database/sql` with the driver name `"sqlite"`:

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
)

db, err := sql.Open("sqlite", "file:dns-he-net.db?_journal_mode=WAL&_busy_timeout=5000")
```

**Always enable WAL mode and busy timeout** for concurrent access from HTTP handlers + browser workers.

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| mattn/go-sqlite3 | Requires CGo. Breaks cross-compilation. Complicates Docker multi-stage builds. Slightly faster but irrelevant for this workload. |
| GORM | ORM is overkill. This project has ~5 tables (accounts, tokens, zones_cache, records_cache, audit_log). Raw SQL with database/sql is cleaner and more transparent. |
| sqlx (jmoiron/sqlx) | Nice convenience layer over database/sql (StructScan, NamedExec). **Optional addition** -- consider adding if boilerplate becomes tedious, but start with plain database/sql. |
| Bun/Ent/sqlc | Over-engineered for ~5 tables. sqlc is good for larger projects but adds a code generation step that is not justified here. |

---

### Authentication (JWT)

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| golang-jwt/jwt | v5.3.1 | JWT token creation and validation | De facto standard Go JWT library, 13,400+ importers |

**Confidence:** HIGH (verified v5.3.1 published Jan 28, 2026 on pkg.go.dev)

**Rationale:** `golang-jwt/jwt/v5` is the maintained successor to `dgrijalva/jwt-go`. It supports HMAC, RSA, ECDSA signing. For this project, use HS256 (HMAC-SHA256) with a server-side secret since tokens are validated only by this service.

**Important design note from PROJECT.md:** The project uses "JWT opaque-style tokens" -- meaning the JWT is essentially an opaque bearer token whose validity is checked against the database (token exists + not revoked + not expired). The JWT claims carry the account ID and role, but revocation is handled by DB lookup, not by JWT expiry alone. This is the correct approach for revocable API tokens.

```go
import jwt "github.com/golang-jwt/jwt/v5"

type Claims struct {
    AccountID string `json:"account_id"`
    Role      string `json:"role"`  // "admin" or "viewer"
    jwt.RegisteredClaims
}
```

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| dgrijalva/jwt-go | Unmaintained, archived. golang-jwt/jwt is the direct successor. |
| lestrrat-go/jwx | More comprehensive (JWK, JWS, JWE) but overkill. We need basic JWT signing, not a full JOSE implementation. |
| paseto | Not JWT. Would require custom client tooling. JWT is universally understood by API consumers (Ansible, Terraform, curl). |

---

### Secrets Management (Vault)

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| hashicorp/vault/api | v1.22.0 | Vault KV v2 client | Official Vault Go client, stable, well-documented |

**Confidence:** HIGH (verified v1.22.0 published Oct 2025 on pkg.go.dev)

**Rationale:** Use the official `hashicorp/vault/api` package (not the newer `vault-client-go` which is still v0.x and unstable). The vault/api package is mature, stable at v1.22.0, and is the battle-tested client used by Terraform and other HashiCorp tooling.

For this project, we need only KV v2 operations:
- `PUT secret/data/dns-he-net/{account-id}` -- store credentials
- `GET secret/data/dns-he-net/{account-id}` -- retrieve credentials
- `DELETE secret/data/dns-he-net/{account-id}` -- remove credentials

```go
import vault "github.com/hashicorp/vault/api"

func NewVaultClient() (*vault.Client, error) {
    config := vault.DefaultConfig() // reads VAULT_ADDR, VAULT_TOKEN env vars
    return vault.NewClient(config)
}
```

**Vault auth strategy:** Use AppRole or Token auth. The service reads `VAULT_ADDR` and `VAULT_TOKEN` (or `VAULT_ROLE_ID`/`VAULT_SECRET_ID` for AppRole) from environment. In Docker, inject via environment variables or mounted files.

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| hashicorp/vault-client-go | New generated client, still v0.x (v0.4.3), unstable API, only 113 importers vs the main api package. Not production-ready. |
| SOPS / age encryption | Encrypts files at rest, not runtime secret retrieval. Different use case. |
| Environment variables only | Credentials in env vars are visible in /proc, Docker inspect, etc. Vault provides audit logging, rotation, access control. |

---

### Frontend (Embedded Web UI)

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| a-h/templ | v0.3.x (latest ~v0.3.977+) | Type-safe HTML templating | Compiles to Go code, type-checked at build time, superior to html/template |
| htmx | 2.0.x (CDN or embedded) | Frontend interactivity | Server-driven UI without JavaScript framework, perfect for admin panels |

**Confidence:** MEDIUM for templ (verified v0.3.977 published Dec 31, 2025; pre-v1 but actively developed and widely adopted). HIGH for htmx (stable, well-established).

**Rationale:** templ + htmx is the standard Go stack for embedded web UIs in 2025/2026. templ provides type-safe, composable templates that compile to Go functions. htmx provides AJAX interactions (form submissions, partial page updates) without a JavaScript build step.

The admin UI needs:
- Account management (add/edit/delete dns.he.net accounts)
- Token management (create/revoke JWT tokens)
- Audit log viewer
- Zone/record browser (read-only view of cached state)

This is a perfect fit for htmx -- server-rendered HTML with AJAX form submissions.

**Embed strategy:**

```go
//go:embed static/*
var staticFS embed.FS

// Serve embedded static assets (htmx.min.js, CSS)
r.Handle("/static/*", http.FileServer(http.FS(staticFS)))
```

Download htmx.min.js and embed it in the binary. No CDN dependency at runtime.

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| html/template (stdlib) | No type safety. Template errors at runtime, not compile time. templ catches errors during `templ generate`. |
| React/Vue SPA | Requires separate build step, Node.js toolchain, CORS configuration. Defeats the single-binary deployment goal. |
| Svelte/SvelteKit | Same as React -- separate build, separate tooling. |

---

### Logging

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| log/slog (stdlib) | Go 1.21+ | Structured logging | Standard library, zero dependencies, JSON output, context-aware |

**Confidence:** HIGH (stdlib, verified available since Go 1.21)

**Rationale:** Use `log/slog` from the standard library. It provides structured, leveled logging with JSON output. No need for external logging libraries in 2025+ Go projects.

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
slog.SetDefault(logger)

slog.Info("dns record created",
    "zone", "example.com",
    "type", "A",
    "name", "www",
    "value", "1.2.3.4",
)
```

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| rs/zerolog | Excellent library (v1.34.0, 28K importers), but slog is now in stdlib and sufficient. Adding zerolog means an external dependency for no clear benefit. |
| uber-go/zap | Same argument as zerolog. Was the gold standard before slog existed. Now unnecessary for new projects. |
| logrus | Effectively deprecated in favor of slog. Maintainer recommends migration. |

---

### Configuration

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| Environment variables | N/A | Runtime configuration | 12-factor app, Docker-native, simple |
| caarlos0/env | v11.x | Struct-based env parsing | Clean struct tags, validation, defaults |

**Confidence:** MEDIUM for caarlos0/env (well-established but version not individually verified against pkg.go.dev)

**Rationale:** For a service deployed in Docker, environment variables are the standard configuration mechanism. Use `caarlos0/env` to parse env vars into a typed Go struct:

```go
type Config struct {
    ListenAddr    string `env:"LISTEN_ADDR" envDefault:":8080"`
    VaultAddr     string `env:"VAULT_ADDR,required"`
    VaultToken    string `env:"VAULT_TOKEN"`
    VaultRoleID   string `env:"VAULT_ROLE_ID"`
    VaultSecretID string `env:"VAULT_SECRET_ID"`
    DBPath        string `env:"DB_PATH" envDefault:"dns-he-net.db"`
    JWTSecret     string `env:"JWT_SECRET,required"`
    LogLevel      string `env:"LOG_LEVEL" envDefault:"info"`
    ChromiumBin   string `env:"CHROMIUM_BIN"` // optional, Rod auto-detects
}
```

**Alternatives Considered:**

| Alternative | Why Not |
|-------------|---------|
| Viper | Massively over-featured for env-only config. Pulls in huge dependency tree (YAML, TOML, etcd, consul watchers). We do not need config files. |
| kelseyhightower/envconfig | Good but less actively maintained than caarlos0/env. Similar API. |
| stdlib os.Getenv | Works but requires manual parsing, validation, defaults for each variable. Error-prone. |

---

### Testing

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| testing (stdlib) | N/A | Test framework | Standard Go testing, no framework needed |
| testify/assert | v1.x | Test assertions | Cleaner assertions than raw if/t.Error |
| testcontainers-go | v0.x | Integration testing | Spin up real Chromium container for browser tests |

**Confidence:** HIGH for testing + testify. MEDIUM for testcontainers-go.

**Rationale:** Use stdlib `testing` as the base. Add `testify/assert` for cleaner assertions. For integration tests that need a real browser, consider `testcontainers-go` to spin up a headless Chromium container, or use Rod's built-in test helpers.

---

### Middleware and Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| go-chi/cors | v1.x | CORS middleware | If API is called from browser clients |
| go-chi/httprate | v0.x | Rate limiting middleware | Rate limit API endpoints |
| google/uuid | v1.x | UUID generation | Token IDs, account IDs |
| pressly/goose | v3.x | Database migrations | Schema versioning for SQLite |

**Confidence:** MEDIUM (versions not individually verified, but all are well-established in Go ecosystem)

**Rationale:**
- **CORS**: Only needed if the embedded UI or external browser clients call the API cross-origin. Chi's CORS middleware is the natural choice.
- **Rate limiting**: Essential to prevent abuse of token creation and DNS operations. `go-chi/httprate` integrates cleanly with the Chi router.
- **UUID**: For generating unique token IDs and account identifiers. `google/uuid` is the standard.
- **Migrations**: `pressly/goose` for versioned SQL migrations. Supports embedding migrations via `embed.FS`. Simple and effective for SQLite.

---

## What NOT to Use

| Technology | Why Avoid |
|------------|-----------|
| **GORM / any ORM** | Over-abstraction for ~5 tables. Hides SQL, makes debugging harder, adds dependency weight. Use `database/sql` directly. |
| **Viper** | Massive dependency tree for config files we do not need. Environment variables are sufficient. |
| **Gin** | Custom context breaks stdlib compatibility. Chi is more idiomatic. |
| **Fiber** | Built on fasthttp, not net/http. Incompatible ecosystem. |
| **Colly** | Web scraping framework for HTTP-based scraping. We need headless browser automation, not HTTP scraping. |
| **playwright-go** | Requires Node.js runtime. Rod is pure Go and sufficient. |
| **mattn/go-sqlite3** | CGo dependency breaks cross-compilation and complicates Docker builds. |
| **Swagger/OpenAPI codegen** | Over-engineering for a focused API with ~15 endpoints. Write handlers directly. Document API in markdown or with a lightweight approach. |
| **gRPC** | REST is the right choice for this API. Consumers are curl, Ansible, Terraform -- all HTTP/JSON native. |
| **Redis** | No caching layer needed. SQLite is the cache. Browser sessions are in-memory. |

---

## Full Dependency Summary

### go.mod Dependencies

```
module github.com/yourusername/dns-he-net-automation

go 1.23

require (
    github.com/go-chi/chi/v5          v5.2.5
    github.com/go-chi/cors             v1.2.1
    github.com/go-chi/httprate         v0.14.1
    github.com/go-rod/rod              v0.116.2
    github.com/golang-jwt/jwt/v5       v5.3.1
    github.com/hashicorp/vault/api     v1.22.0
    github.com/a-h/templ               v0.3.977
    github.com/google/uuid             v1.6.0
    github.com/pressly/goose/v3        v3.24.1
    github.com/caarlos0/env/v11        v11.3.1
    modernc.org/sqlite                 v1.46.1
)

require (
    // test dependencies
    github.com/stretchr/testify        v1.10.0
)
```

### Installation

```bash
# Initialize module
go mod init github.com/yourusername/dns-he-net-automation

# Core dependencies
go get github.com/go-chi/chi/v5@v5.2.5
go get github.com/go-rod/rod@v0.116.2
go get github.com/golang-jwt/jwt/v5@v5.3.1
go get github.com/hashicorp/vault/api@v1.22.0
go get modernc.org/sqlite@v1.46.1
go get github.com/a-h/templ@latest
go get github.com/google/uuid@latest
go get github.com/pressly/goose/v3@latest
go get github.com/caarlos0/env/v11@latest

# Middleware
go get github.com/go-chi/cors@latest
go get github.com/go-chi/httprate@latest

# Dev / test dependencies
go get -t github.com/stretchr/testify@latest

# templ CLI (code generation tool)
go install github.com/a-h/templ/cmd/templ@latest
```

---

## Build and Deploy

### Standalone Binary

```bash
# Build (pure Go, no CGo needed thanks to modernc.org/sqlite)
CGO_ENABLED=0 go build -o dns-he-net-api ./cmd/server

# Run (Rod will auto-download Chromium on first run)
VAULT_ADDR=https://vault.example.com \
VAULT_TOKEN=s.xxxxx \
JWT_SECRET=your-secret-here \
./dns-he-net-api
```

### Docker

```dockerfile
# Stage 1: Build
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o dns-he-net-api ./cmd/server

# Stage 2: Runtime with headless Chromium
FROM chromedp/headless-shell:stable
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata && \
    rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/dns-he-net-api /usr/local/bin/
ENV CHROMIUM_BIN=/headless-shell/headless-shell
EXPOSE 8080
ENTRYPOINT ["dns-he-net-api"]
```

**Key Docker notes:**
- `chromedp/headless-shell` provides minimal headless Chromium (~100MB)
- `CGO_ENABLED=0` works because we use modernc.org/sqlite (pure Go)
- No Node.js, no NPM, no build toolchain in final image
- Final image is ~150MB (Chromium + Go binary + CA certs)

---

## Sources

- go-rod/rod v0.116.2: https://pkg.go.dev/github.com/go-rod/rod (verified Jul 2024)
- go-chi/chi v5.2.5: https://pkg.go.dev/github.com/go-chi/chi/v5 (verified Feb 5, 2026)
- golang-jwt/jwt v5.3.1: https://pkg.go.dev/github.com/golang-jwt/jwt/v5 (verified Jan 28, 2026)
- modernc.org/sqlite v1.46.1: https://pkg.go.dev/modernc.org/sqlite (verified Feb 18, 2026, SQLite 3.51.2)
- hashicorp/vault/api v1.22.0: https://pkg.go.dev/github.com/hashicorp/vault/api (verified Oct 2025)
- a-h/templ v0.3.977: https://pkg.go.dev/github.com/a-h/templ (verified Dec 31, 2025)
- rs/zerolog v1.34.0: https://pkg.go.dev/github.com/rs/zerolog (verified, considered but not recommended)
- log/slog: https://pkg.go.dev/log/slog (stdlib since Go 1.21)
- chromedp/headless-shell Docker image: https://hub.docker.com/r/chromedp/headless-shell

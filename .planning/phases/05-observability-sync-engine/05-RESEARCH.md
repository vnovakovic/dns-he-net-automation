# Phase 5: Observability + Sync Engine - Research

**Researched:** 2026-02-28
**Domain:** Prometheus metrics instrumentation in Go, SQLite audit logging, DNS desired-state reconciliation
**Confidence:** HIGH

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| OBS-01 | `GET /metrics` exposes Prometheus-format metrics: request count/duration by endpoint, browser op count/duration by type, active sessions gauge, queue depth per account, error counts by type | prometheus/client_golang v1.23.2 CounterVec + HistogramVec + GaugeVec registered on a custom registry; promhttp.HandlerFor serves /metrics; chi middleware instruments per-endpoint request metrics with routing pattern labels to avoid cardinality explosion |
| OBS-02 | Audit log in SQLite records timestamp, token_id, account_id, action (create/update/delete/sync), resource identifier, result (success/failure) for every mutation | New goose migration adds `audit_log` table; a WriteAuditLog() helper inserts a row; each mutating handler (CreateRecord, UpdateRecord, DeleteRecord, CreateZone, DeleteZone, SyncRecords) calls it after the operation completes (or fails) |
| SYNC-01 | `POST /api/v1/zones/{zone_id}/sync` accepts desired-state record set and computes diff against live state | New handler calls ListRecords (scrape live), then runs DiffRecords() comparing desired vs current sets; returns diff plan |
| SYNC-02 | Diff produces three sets: records to add, records to update, records to delete | DiffRecords() returns a SyncPlan struct with Add/Update/Delete slices; matching key is (Type, Name, Content/normalized-value) |
| SYNC-03 | Changes applied in safe order: deletes first, then updates, then adds | Apply() iterates: for _, del := range plan.Delete first, then plan.Update, then plan.Add; each step calls the existing page-object browser operations |
| SYNC-04 | Sync supports partial success: if one operation fails, remaining still execute and response reports per-operation results | Apply() collects []SyncResult{Op, RecordID, Status, Error}; errors never short-circuit the loop; final response is HTTP 207 (or 200 for pure dry-run) with per-result array |
| SYNC-05 | Sync is idempotent: running twice with same desired state produces no changes on second run | DiffRecords() equality check is (Type, Name, Content) + type-specific fields; if all three sets are empty, no operations execute; second run produces empty diff → no-op |
| SYNC-06 | Sync supports `dry_run=true` query parameter: returns diff/plan without applying | Handler checks `r.URL.Query().Get("dry_run") == "true"`; if true, returns SyncPlan directly without calling Apply() |
</phase_requirements>

---

## Summary

Phase 5 adds two independent capabilities: Prometheus-format observability for the running service, and a declarative DNS sync endpoint that reconciles desired state against live HE.net records.

For observability, the standard approach in Go is `github.com/prometheus/client_golang` v1.23.2. The library provides CounterVec, HistogramVec, and GaugeVec metric types. A custom registry (not the global default) is the current best practice — it avoids polluting the default registry and makes unit testing trivial. HTTP request metrics are layered via `promhttp.InstrumentHandler*` middleware wrapping the chi router. Browser operation metrics require explicit instrumentation hooks inside `SessionManager.WithAccount()` and the record/zone handlers. The `GET /metrics` endpoint serves the custom registry via `promhttp.HandlerFor`. No third-party chi-prometheus middleware is needed; the project's existing handler-function pattern is thin enough that inline instrumentation is cleaner.

For the audit log, the existing goose migration pattern (new `.sql` file under `internal/store/migrations/`) handles schema evolution cleanly. A lightweight `WriteAuditLog()` function does a single INSERT per mutation and is called from each mutating handler after the operation completes. The audit log is append-only and never updated — no transactions needed beyond the INSERT itself.

The sync engine is custom-built (not a library) because the matching logic is DNS-record-specific and the apply path reuses existing CreateRecord/UpdateRecord/DeleteRecord page-object operations already built in Phase 3. The diff algorithm is a two-pass map lookup: (1) build a map of current records keyed by (Type, Name, normalized-content); (2) iterate desired records — if missing from current it's an Add, if present but fields differ it's an Update; (3) remaining current records not matched by any desired record are Deletes. Partial failure is handled by collecting results without short-circuiting the apply loop.

**Primary recommendation:** Use `github.com/prometheus/client_golang` v1.23.2 with a custom registry, instrumentation middleware for HTTP layer, explicit instrumentation inside SessionManager for browser ops. Sync engine is hand-rolled using existing page objects — no library needed. Audit log is a new SQLite table via a goose migration.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/prometheus/client_golang` | v1.23.2 | Prometheus metric types, registry, HTTP handler | Official Go Prometheus client; only maintained client for Go; used universally |
| `modernc.org/sqlite` | v1.46.1 (already in go.mod) | Audit log storage via new goose migration | Already the project's SQLite driver; no new dependency |
| `github.com/pressly/goose/v3` | v3.27.0 (already in go.mod) | Schema migration for audit_log table | Already manages all schema changes in this project |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/prometheus/client_golang/prometheus/promhttp` | (same module) | HTTP handler that serves /metrics | Mount at `GET /metrics` before auth middleware |
| `github.com/prometheus/client_golang/prometheus/promauto` | (same module) | Auto-register metrics with a registry via `promauto.With(reg)` | Reduces boilerplate; use for all metric definitions |
| `github.com/prometheus/client_golang/prometheus/collectors` | (same module) | Go runtime and process metrics | Add to custom registry for standard Go operational metrics |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Custom sync engine | `sigs.k8s.io/external-dns/plan` | external-dns Plan/Calculate is designed for Kubernetes endpoint types, not model.Record; adapting it adds more complexity than a 50-line hand-rolled diff |
| goose migration for audit log | Separate audit DB file | Same DB is simpler, SQLite WAL mode handles concurrent reads; separate file adds operational complexity |
| Custom registry | `prometheus.DefaultRegisterer` | Default registry includes process/Go collector by default and causes test panics on duplicate registration; custom registry is cleaner |

**Installation:**
```bash
go get github.com/prometheus/client_golang@v1.23.2
```

---

## Architecture Patterns

### Recommended Project Structure

```
internal/
├── metrics/
│   └── metrics.go           # Registry, all metric vars, NewRegistry(), Register() func
├── audit/
│   └── audit.go             # WriteAuditLog() function, AuditEntry struct
├── sync/
│   ├── diff.go              # DiffRecords(), SyncPlan struct, record matching logic
│   └── diff_test.go         # Unit tests for diff algorithm (no browser needed)
internal/store/migrations/
└── 003_audit_log.sql        # New goose migration: CREATE TABLE audit_log
internal/api/handlers/
└── sync.go                  # SyncRecords handler (POST /zones/{zone_id}/sync)
internal/api/
└── router.go                # Add /metrics route, /zones/{zone_id}/sync route
```

### Pattern 1: Custom Prometheus Registry with promauto

**What:** Create all metrics in a dedicated package with a custom registry. All metrics are defined at package init time using `promauto.With(reg)`.
**When to use:** Always in Go services — avoids global default registry pollution, makes tests run in isolation.

```go
// Source: https://pkg.go.dev/github.com/prometheus/client_golang/prometheus
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/collectors"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

// Registry holds all application metrics for dns-he-net-automation.
type Registry struct {
    reg *prometheus.Registry

    // HTTP layer (OBS-01)
    HTTPRequestsTotal    *prometheus.CounterVec
    HTTPRequestDuration  *prometheus.HistogramVec

    // Browser operations (OBS-01)
    BrowserOpsTotal     *prometheus.CounterVec
    BrowserOpDuration   *prometheus.HistogramVec

    // Session state (OBS-01)
    ActiveSessions      prometheus.Gauge

    // Per-account queue depth (OBS-01)
    QueueDepth          *prometheus.GaugeVec

    // Error counts (OBS-01)
    ErrorsTotal         *prometheus.CounterVec
}

func NewRegistry() *Registry {
    reg := prometheus.NewRegistry()

    // Standard Go process/runtime metrics
    reg.MustRegister(
        collectors.NewGoCollector(),
        collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
    )

    f := promauto.With(reg)

    return &Registry{
        reg: reg,

        HTTPRequestsTotal: f.NewCounterVec(prometheus.CounterOpts{
            Namespace: "dnshe",
            Subsystem: "http",
            Name:      "requests_total",
            Help:      "Total HTTP requests by method, route pattern, and status code.",
        }, []string{"method", "route", "status"}),

        HTTPRequestDuration: f.NewHistogramVec(prometheus.HistogramOpts{
            Namespace: "dnshe",
            Subsystem: "http",
            Name:      "request_duration_seconds",
            Help:      "HTTP request duration in seconds by method and route pattern.",
            // DNS scraping can take 2–10s; extend default buckets
            Buckets:   []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 15, 30},
        }, []string{"method", "route"}),

        BrowserOpsTotal: f.NewCounterVec(prometheus.CounterOpts{
            Namespace: "dnshe",
            Subsystem: "browser",
            Name:      "operations_total",
            Help:      "Total browser operations by type and result.",
        }, []string{"op_type", "result"}),

        BrowserOpDuration: f.NewHistogramVec(prometheus.HistogramOpts{
            Namespace: "dnshe",
            Subsystem: "browser",
            Name:      "operation_duration_seconds",
            Help:      "Browser operation duration in seconds by type.",
            Buckets:   []float64{.5, 1, 2.5, 5, 10, 15, 30},
        }, []string{"op_type"}),

        ActiveSessions: f.NewGauge(prometheus.GaugeOpts{
            Namespace: "dnshe",
            Subsystem: "browser",
            Name:      "active_sessions",
            Help:      "Number of active browser sessions.",
        }),

        QueueDepth: f.NewGaugeVec(prometheus.GaugeOpts{
            Namespace: "dnshe",
            Subsystem: "browser",
            Name:      "queue_depth",
            Help:      "Number of requests waiting for the per-account browser mutex.",
        }, []string{"account_id"}),

        ErrorsTotal: f.NewCounterVec(prometheus.CounterOpts{
            Namespace: "dnshe",
            Subsystem: "app",
            Name:      "errors_total",
            Help:      "Total errors by type.",
        }, []string{"error_type"}),
    }
}

// Handler returns the promhttp handler for /metrics.
func (r *Registry) Handler() http.Handler {
    return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}
```

### Pattern 2: HTTP Middleware Instrumentation with Chi Routing Pattern

**What:** Wrap the chi router with Prometheus middleware. Use `chi.RouteContext(r.Context()).RoutePattern()` to get the pattern (`/api/v1/zones/{zoneID}/records`) not the actual URL — critical for avoiding label cardinality explosion.
**When to use:** All HTTP endpoints.

```go
// Source: https://pkg.go.dev/github.com/go-chi/chi/v5, https://pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp
// In router.go — wrap the router after all chi routes are registered

func PrometheusMiddleware(reg *metrics.Registry) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()

            // Use a response writer wrapper to capture status code
            ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
            next.ServeHTTP(ww, r)

            // Get the route pattern (NOT the actual path — avoids cardinality explosion)
            routePattern := chi.RouteContext(r.Context()).RoutePattern()
            if routePattern == "" {
                routePattern = "unknown"
            }

            status := strconv.Itoa(ww.Status())
            reg.HTTPRequestsTotal.WithLabelValues(r.Method, routePattern, status).Inc()
            reg.HTTPRequestDuration.WithLabelValues(r.Method, routePattern).Observe(time.Since(start).Seconds())
        })
    }
}
```

Note: `chiMiddleware.NewWrapResponseWriter` is already available from `github.com/go-chi/chi/v5/middleware` — already a project dependency.

### Pattern 3: Browser Operation Instrumentation

**What:** Record browser op metrics inside `SessionManager.WithAccount()` by wrapping the op function. ActiveSessions gauge is updated in `createBrowserSession` (Inc on success) and `closeBrowserContext` (Dec on close).
**When to use:** Every browser operation must be measured.

```go
// Source: verified from project's internal/browser/session.go structure
// Modify SessionManager to accept a *metrics.Registry

// In WithAccount():
start := time.Now()
opType := "generic"  // caller passes op type via context or parameter
err := op(session.page)
duration := time.Since(start).Seconds()

result := "success"
if err != nil {
    result = "error"
}
sm.metrics.BrowserOpsTotal.WithLabelValues(opType, result).Inc()
sm.metrics.BrowserOpDuration.WithLabelValues(opType).Observe(duration)
```

Queue depth can be tracked with Inc/Dec around the goroutine-based lock acquisition in `WithAccount()`:
```go
sm.metrics.QueueDepth.WithLabelValues(accountID).Inc()
// ... wait for lock ...
sm.metrics.QueueDepth.WithLabelValues(accountID).Dec()
```

### Pattern 4: Audit Log Table and WriteAuditLog()

**What:** A goose migration adds an `audit_log` table. A package-level `WriteAuditLog()` function inserts one row per mutation.
**When to use:** Every mutating handler (CreateRecord, UpdateRecord, DeleteRecord, CreateZone, DeleteZone, SyncRecords) calls `audit.Write(db, ...)` after the operation completes.

```go
// Source: based on project's goose migration pattern (internal/store/migrations/)
// Migration file: 003_audit_log.sql
-- +goose Up
CREATE TABLE IF NOT EXISTS audit_log (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    token_id     TEXT     NOT NULL,
    account_id   TEXT     NOT NULL,
    action       TEXT     NOT NULL CHECK (action IN ('create','update','delete','sync')),
    resource     TEXT     NOT NULL,   -- e.g. "record:12345" or "zone:67890"
    result       TEXT     NOT NULL CHECK (result IN ('success','failure')),
    error_msg    TEXT                 -- NULL on success, message on failure
);

CREATE INDEX IF NOT EXISTS idx_audit_log_account_id  ON audit_log(account_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at  ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_token_id    ON audit_log(token_id);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_token_id;
DROP INDEX IF EXISTS idx_audit_log_created_at;
DROP INDEX IF EXISTS idx_audit_log_account_id;
DROP TABLE IF EXISTS audit_log;
```

```go
// internal/audit/audit.go
package audit

type Entry struct {
    TokenID   string
    AccountID string
    Action    string  // "create" | "update" | "delete" | "sync"
    Resource  string  // "record:<id>" | "zone:<id>"
    Result    string  // "success" | "failure"
    ErrorMsg  string  // empty on success
}

func Write(ctx context.Context, db *sql.DB, e Entry) error {
    var errMsg *string
    if e.ErrorMsg != "" {
        errMsg = &e.ErrorMsg
    }
    _, err := db.ExecContext(ctx,
        `INSERT INTO audit_log (token_id, account_id, action, resource, result, error_msg)
         VALUES (?, ?, ?, ?, ?, ?)`,
        e.TokenID, e.AccountID, e.Action, e.Resource, e.Result, errMsg,
    )
    return err
}
```

The handler calls this after the browser op returns:
```go
auditResult := "success"
auditErr := ""
if err != nil {
    auditResult = "failure"
    auditErr = err.Error()
}
_ = audit.Write(r.Context(), db, audit.Entry{
    TokenID:   claims.ID,
    AccountID: claims.AccountID,
    Action:    "create",
    Resource:  "record:" + newRecord.ID,
    Result:    auditResult,
    ErrorMsg:  auditErr,
})
```

### Pattern 5: Sync Engine Diff Algorithm

**What:** Two-pass map-based diff. First pass builds current record map. Second pass iterates desired records and classifies each. Third pass collects unmatched current records as deletes.
**When to use:** `POST /api/v1/zones/{zone_id}/sync`.

```go
// internal/sync/diff.go
package sync

import "github.com/vnovakovic/dns-he-net-automation/internal/model"

// RecordKey uniquely identifies a DNS record for matching purposes.
// Used as map key for O(n) diff instead of O(n²) nested loop.
type RecordKey struct {
    Type    model.RecordType
    Name    string
    Content string
}

type SyncPlan struct {
    Add    []model.Record
    Update []model.Record  // desired state to write (use record ID from current)
    Delete []model.Record  // current records to remove
}

// SyncResult reports the outcome of one sync operation.
type SyncResult struct {
    Op       string  // "add" | "update" | "delete"
    Record   model.Record
    Status   string  // "ok" | "error" | "skipped"
    ErrorMsg string
}

// SyncResponse is the full response body for POST /sync.
type SyncResponse struct {
    DryRun  bool         `json:"dry_run"`
    Plan    SyncPlan     `json:"plan"`
    Results []SyncResult `json:"results,omitempty"` // nil on dry_run
}

func keyOf(r model.Record) RecordKey {
    return RecordKey{Type: r.Type, Name: r.Name, Content: r.Content}
}

// DiffRecords computes the minimal changes needed to move current → desired.
func DiffRecords(current, desired []model.Record) SyncPlan {
    currentMap := make(map[RecordKey]model.Record, len(current))
    for _, r := range current {
        currentMap[keyOf(r)] = r
    }

    plan := SyncPlan{}
    matchedKeys := make(map[RecordKey]bool)

    for _, d := range desired {
        k := keyOf(d)
        cur, exists := currentMap[k]
        if !exists {
            plan.Add = append(plan.Add, d)
        } else if !recordsEqual(cur, d) {
            // Same key but fields differ (TTL, priority, etc.) — update
            d.ID = cur.ID  // carry over the HE internal ID for UpdateRecord
            plan.Update = append(plan.Update, d)
            matchedKeys[k] = true
        } else {
            // Identical — no action needed
            matchedKeys[k] = true
        }
    }

    for k, r := range currentMap {
        if !matchedKeys[k] {
            plan.Delete = append(plan.Delete, r)
        }
    }
    return plan
}

// recordsEqual checks if two records are identical in all meaningful fields.
func recordsEqual(a, b model.Record) bool {
    return a.TTL == b.TTL &&
        a.Priority == b.Priority &&
        a.Weight == b.Weight &&
        a.Port == b.Port &&
        a.Target == b.Target &&
        a.Dynamic == b.Dynamic
}
```

### Pattern 6: Sync Apply with Partial Success

**What:** Apply changes in safe order (delete → update → add), collect per-result outcomes, never short-circuit on error.
**When to use:** `dry_run=false` path in the sync handler.

```go
// Continues in internal/sync/diff.go or a separate apply.go
func Apply(ctx context.Context, zoneID string, plan SyncPlan,
    deleteFn, updateFn, createFn func(context.Context, string, model.Record) error,
) []SyncResult {
    var results []SyncResult

    // Safe order: deletes first (prevents transient conflicts during updates/adds)
    for _, r := range plan.Delete {
        err := deleteFn(ctx, zoneID, r)
        results = append(results, opResult("delete", r, err))
    }
    for _, r := range plan.Update {
        err := updateFn(ctx, zoneID, r)
        results = append(results, opResult("update", r, err))
    }
    for _, r := range plan.Add {
        err := createFn(ctx, zoneID, r)
        results = append(results, opResult("add", r, err))
    }
    return results
}

func opResult(op string, r model.Record, err error) SyncResult {
    if err != nil {
        return SyncResult{Op: op, Record: r, Status: "error", ErrorMsg: err.Error()}
    }
    return SyncResult{Op: op, Record: r, Status: "ok"}
}
```

### Pattern 7: Sync Handler (dry_run + apply)

```go
// internal/api/handlers/sync.go
func SyncRecords(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        claims := middleware.ClaimsFromContext(r.Context())
        zoneID := chi.URLParam(r, "zoneID")
        dryRun := r.URL.Query().Get("dry_run") == "true"

        // Decode desired state from request body
        var desiredRecords []model.Record
        if err := json.NewDecoder(r.Body).Decode(&desiredRecords); err != nil {
            response.WriteError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
            return
        }

        // Scrape current live state
        var currentRecords []model.Record
        err := breakers.Execute(r.Context(), claims.AccountID, func() error {
            return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
                return sm.WithAccount(ctx, claims.AccountID, func(page playwright.Page) error {
                    rp := pages.NewRecordPage(page)
                    recs, err := rp.ListRecords(zoneID)
                    currentRecords = recs
                    return err
                })
            })
        })
        if err != nil {
            handleBrowserError(w, err)
            return
        }

        // Compute diff
        plan := sync.DiffRecords(currentRecords, desiredRecords)

        if dryRun {
            response.WriteJSON(w, http.StatusOK, sync.SyncResponse{
                DryRun: true,
                Plan:   plan,
            })
            return
        }

        // Apply changes — partial failure is captured, not returned as HTTP error
        results := sync.Apply(r.Context(), zoneID, plan,
            func(ctx context.Context, zid string, rec model.Record) error {
                return breakers.Execute(ctx, claims.AccountID, func() error {
                    return sm.WithAccount(ctx, claims.AccountID, func(page playwright.Page) error {
                        return pages.NewRecordPage(page).DeleteRecord(rec.ID)
                    })
                })
            },
            // ... updateFn, createFn similarly
        )

        // Audit log the sync operation
        _ = audit.Write(r.Context(), db, audit.Entry{
            TokenID:   claims.ID,
            AccountID: claims.AccountID,
            Action:    "sync",
            Resource:  "zone:" + zoneID,
            Result:    "success", // partial failures are in results, not top-level
        })

        response.WriteJSON(w, http.StatusOK, sync.SyncResponse{
            DryRun:  false,
            Plan:    plan,
            Results: results,
        })
    }
}
```

### Anti-Patterns to Avoid

- **Using raw URL path as Prometheus label:** `r.URL.Path` gives `/api/v1/zones/12345/records` — the number 12345 creates unbounded cardinality. Always use `chi.RouteContext(r.Context()).RoutePattern()` which returns `/api/v1/zones/{zoneID}/records`.
- **Using prometheus.DefaultRegisterer for tests:** The default registry panics on duplicate metric registration. All tests that call code using the registry must pass a freshly-created `prometheus.NewRegistry()`.
- **Short-circuiting sync on first error:** SYNC-04 explicitly requires remaining operations to continue. `return err` inside the apply loop violates the requirement.
- **Using `sync` as the Go package name:** `sync` is a standard library package; name the project package `internalsync` or `reconcile` to avoid import collisions.
- **Keying sync diff only on Type+Name:** Two A records for the same name but different IPs are both valid (e.g., round-robin). The matching key must include Content (value) to distinguish them.
- **Audit log blocking the response:** If `audit.Write()` fails, log the error with slog but do not fail the HTTP response. Audit log is observability infrastructure, not business logic.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Prometheus text format generation | Custom metric serialization | `prometheus/client_golang` + `promhttp` | Text format has edge cases (escaping, timestamps, histogram _sum/_count/_bucket suffix generation) |
| HTTP response status code capture | Custom ResponseWriter wrapper | `chiMiddleware.NewWrapResponseWriter` (already in project) | chi's WrapResponseWriter already implements `http.Flusher`, `http.Hijacker`, `http.CloseNotifier` correctly |
| Exponential histogram buckets | `[]float64{0.1, 0.2, 0.4, ...}` manual | `prometheus.ExponentialBuckets(start, factor, count)` | Avoids off-by-one in bucket boundary calculation |
| Schema migration for audit_log | Manual `db.Exec("CREATE TABLE ...")` at startup | New goose migration file `003_audit_log.sql` | Already the established pattern; handles versioning, rollback, idempotency |
| Record equality for diff | String comparison of entire struct | Field-by-field comparison of semantically-relevant fields | JSON marshaling and string comparison is fragile for floating-point TTL values |

**Key insight:** The Prometheus client library handles all the complexity of the text exposition format, concurrent-safe metric updates, and HTTP content negotiation (OpenMetrics vs. classic format). Never serialize metrics manually.

---

## Common Pitfalls

### Pitfall 1: Label Cardinality Explosion on /metrics
**What goes wrong:** Request counter has a label for the full URL path. After 1000 unique zone IDs are queried, there are 1000 distinct label sets, consuming hundreds of MB in Prometheus server memory.
**Why it happens:** Raw `r.URL.Path` includes path parameters, not route patterns.
**How to avoid:** Always use `chi.RouteContext(r.Context()).RoutePattern()`. Call the label `route` not `path`. Verify by inspecting the metrics output after a few requests — patterns should look like `/api/v1/zones/{zoneID}/records`, never `/api/v1/zones/actual-zone-id/records`.
**Warning signs:** `dnshe_http_requests_total` metric has more label sets than there are route definitions in the router.

### Pitfall 2: Duplicate Metric Registration Panics in Tests
**What goes wrong:** Test file creates a metric with `promauto.NewCounter(...)` using the default registry. The second test run (or parallel tests) panics: `panic: duplicate metrics collector registration attempted`.
**Why it happens:** `promauto` registers with `prometheus.DefaultRegisterer` by default, which is a process-global singleton.
**How to avoid:** Always use `promauto.With(customRegistry)` where `customRegistry = prometheus.NewRegistry()` is scoped to the application lifecycle. Test code passes a fresh registry per test.
**Warning signs:** Tests pass when run individually but panic when run with `go test ./...`.

### Pitfall 3: SessionManager Metrics Injection via Global Variable
**What goes wrong:** SessionManager calls a package-level `metrics.ActiveSessions.Inc()` directly. This creates an import cycle if `metrics` package imports `browser` (which it shouldn't, but the coupling is fragile) and makes `SessionManager` untestable without a real Prometheus registry.
**Why it happens:** Convenient-looking global metric variables.
**How to avoid:** Pass `*metrics.Registry` to `browser.NewSessionManager(...)` as a parameter (consistent with how the project already passes `breakers *resilience.BreakerRegistry`). For tests, pass `nil` registry and guard all metric calls with a nil check: `if sm.metrics != nil { sm.metrics.ActiveSessions.Inc() }`.

### Pitfall 4: /metrics Endpoint Behind Auth Middleware
**What goes wrong:** Prometheus scraper gets 401 Unauthorized because `/metrics` is registered inside the `/api/v1/*` route group that has `BearerAuth` middleware.
**Why it happens:** Metrics endpoint added in the wrong route group.
**How to avoid:** Register `GET /metrics` at the root router level, not inside `/api/v1`. In `router.go`, add `r.Get("/metrics", reg.Handler())` alongside `r.Get("/healthz", ...)` — both are unauthenticated operational endpoints.
**Warning signs:** `curl http://localhost:8080/metrics` returns `{"error":"Authorization header required","code":"missing_token"}`.

### Pitfall 5: Sync Diff Matching on Name+Type Only (Missing Content)
**What goes wrong:** A zone has two A records for the same name (round-robin: `api.example.com A 1.2.3.4` and `api.example.com A 5.6.7.8`). Sync with `desired=[]` triggers only one delete because the second record's key collides with the first in the current map.
**Why it happens:** Map key is `(Type, Name)` — not unique for multi-value records.
**How to avoid:** Map key is `(Type, Name, Content)`. This is the correct identity for DNS records that are not MX/SRV (which have priority in Content already). For SRV, Content is the target; Priority+Weight+Port distinguish instances — consider including those in the key too.

### Pitfall 6: Sync Deletes Records It Shouldn't (Unmanaged Records)
**What goes wrong:** Sync deletes SOA, NS, and other HE-managed records that were not in the desired set but exist in the live zone.
**Why it happens:** DiffRecords naively treats everything in current but not in desired as a Delete.
**How to avoid:** In the diff algorithm, filter `current` records through the same locked-row/SOA exclusion logic already used in `ListRecords` in `internal/browser/pages/recordform.go`. Records that `ListRecords` returns as "skipped" (SOA, system) should not appear in the current set passed to DiffRecords. Or, filter them out in the sync handler before calling DiffRecords.

### Pitfall 7: Browser Op Metrics Observed Before Operation Completes
**What goes wrong:** The histogram records 0 duration for some operations because `time.Since(start)` is measured before the op func is called.
**Why it happens:** `start := time.Now()` declared after the lock was acquired but the observe call is misplaced.
**How to avoid:** Pattern is exactly: `start := time.Now(); err := op(session.page); reg.BrowserOpDuration.Observe(time.Since(start))`. Keep the observe call immediately after `op()` returns.

### Pitfall 8: Audit Log Error Silently Discarded
**What goes wrong:** Audit log INSERT fails (disk full, WAL timeout) and the error is silently ignored. The audit log has gaps that are not detectable.
**How to avoid:** Log audit log failures with `slog.ErrorContext(ctx, "audit log write failed", "error", err)` even if the HTTP response succeeds. Do NOT return HTTP error on audit log failure — it is observability infrastructure, not the primary response path.

---

## Code Examples

Verified patterns from official sources:

### Prometheus Custom Registry Setup
```go
// Source: https://pkg.go.dev/github.com/prometheus/client_golang/prometheus
// Source: https://prometheus.io/docs/guides/go-application/
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/collectors"
    "github.com/prometheus/client_golang/prometheus/promauto"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

reg := prometheus.NewRegistry()
reg.MustRegister(collectors.NewGoCollector())
reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

f := promauto.With(reg)
counter := f.NewCounterVec(prometheus.CounterOpts{
    Namespace: "dnshe",
    Name:      "http_requests_total",
}, []string{"method", "route", "status"})

// Mount the handler (unauthenticated, root-level)
r.Get("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP)
```

### Chi Route Pattern Label (Cardinality-Safe)
```go
// Source: https://pkg.go.dev/github.com/go-chi/chi/v5 — RouteContext
import "github.com/go-chi/chi/v5"

routePattern := chi.RouteContext(r.Context()).RoutePattern()
// Produces: "/api/v1/zones/{zoneID}/records" not "/api/v1/zones/example.com/records"
```

### WrapResponseWriter for Status Capture (chi built-in)
```go
// Source: https://pkg.go.dev/github.com/go-chi/chi/v5/middleware — WrapResponseWriter
// Already in go.mod as go-chi/chi/v5
import chiMiddleware "github.com/go-chi/chi/v5/middleware"

ww := chiMiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
next.ServeHTTP(ww, r)
status := ww.Status() // int — 200, 404, 500, etc.
```

### Goose Migration File Format
```sql
-- Source: existing project pattern (001_init.sql, 002_tokens.sql)
-- File: internal/store/migrations/003_audit_log.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS audit_log (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    token_id     TEXT     NOT NULL,
    account_id   TEXT     NOT NULL,
    action       TEXT     NOT NULL CHECK (action IN ('create','update','delete','sync')),
    resource     TEXT     NOT NULL,
    result       TEXT     NOT NULL CHECK (result IN ('success','failure')),
    error_msg    TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_log_account_id ON audit_log(account_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at  ON audit_log(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_created_at;
DROP INDEX IF EXISTS idx_audit_log_account_id;
DROP TABLE IF EXISTS audit_log;
```

### Sync Diff with Map Key
```go
// Source: inspired by sigs.k8s.io/external-dns/plan (Changes struct concept)
// Adapted to model.Record with (Type, Name, Content) key
type RecordKey struct{ Type model.RecordType; Name, Content string }

currentMap := make(map[RecordKey]model.Record)
for _, r := range current { currentMap[RecordKey{r.Type, r.Name, r.Content}] = r }
```

### Partial Success Apply Loop
```go
// Source: project-specific pattern — SYNC-04 requirement
var results []SyncResult
for _, r := range plan.Delete {
    err := deleteFn(ctx, r)  // NEVER return here — always collect
    results = append(results, opResult("delete", r, err))
}
// Same pattern for Update and Add
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `prometheus.MustRegister(myMetric)` with default registry | `promauto.With(customReg).NewCounter(...)` | ~2020, best practice solidified ~2022 | Tests no longer panic on duplicate registration |
| Gorilla Mux Prometheus middleware | chi-native `RouteContext().RoutePattern()` in custom middleware | chi v5 (2021+) | Eliminates third-party middleware dependency; pattern available natively |
| `prom/client_golang` v0.x flat API | v1.x with Namespace/Subsystem/Name opts | 2016–2017, stable since | Metric naming now enforces conventions |
| Write raw Prometheus text format | Use `promhttp.HandlerFor()` | Since v1.0 | Handles gzip, content negotiation, OpenMetrics format automatically |

**Deprecated/outdated:**
- `prometheus.InstrumentHandler()`: Deprecated in favor of `promhttp.InstrumentHandler*` family (InstrumentHandlerCounter, InstrumentHandlerDuration, etc.).
- Using `init()` for metric registration: Replaced by `promauto` which registers at variable declaration time with clear registry association.

---

## Open Questions

1. **Op type label granularity for browser metrics**
   - What we know: OBS-01 requires "browser operation count/duration by type"; the project has operations like ListZones, ListRecords, CreateRecord, UpdateRecord, DeleteRecord, Login
   - What's unclear: Should op_type be coarse ("read"/"write") or fine-grained ("list_records", "create_record", etc.)? Fine-grained is more useful but adds ~10 label values.
   - Recommendation: Use fine-grained op types ("list_zones", "list_records", "create_record", "update_record", "delete_record", "sync", "login", "health_check") — bounded set, high diagnostic value, no cardinality risk.

2. **SRV record matching in sync diff**
   - What we know: SRV records have Priority, Weight, Port, Target in addition to Name+Content. The content field in model.Record for SRV is the target hostname; Priority/Weight/Port are separate fields.
   - What's unclear: Should the RecordKey include Priority+Weight+Port for SRV records to allow multiple SRV records with same name but different port/weight?
   - Recommendation: Use a type-specific key function: for SRV, the key includes `Priority`, `Weight`, `Port` in addition to `Type`, `Name`, `Content`. For all other types, key is `(Type, Name, Content)`.

3. **Sync response HTTP status code on partial failure**
   - What we know: SYNC-04 says "remaining operations still execute and the response reports per-operation results" — does not specify 207 vs 200.
   - What's unclear: Should the HTTP status be 200 (always) or 207 Multi-Status when at least one operation failed?
   - Recommendation: Use HTTP 200 with a results array. HTTP 207 is technically correct (RFC 4918) but uncommon in REST APIs and surprises clients. Include a top-level `"had_errors": true` field in the response body when any result has `"status": "error"`.

4. **Metrics for the sync endpoint itself**
   - What we know: OBS-01 does not specifically call out sync metrics, but the "request count/duration by endpoint" requirement covers it via the HTTP middleware.
   - What's unclear: Should sync also record per-operation metrics (add_count, update_count, delete_count per sync call)?
   - Recommendation: Add a sync-specific counter: `dnshe_sync_operations_total{op_type="add|update|delete", result="ok|error"}`. This is low-cost and gives Prometheus-level visibility into sync behavior without requiring log parsing.

---

## Sources

### Primary (HIGH confidence)
- `pkg.go.dev/github.com/prometheus/client_golang/prometheus` — Registry, CounterVec, HistogramVec, GaugeVec, promauto pattern; verified v1.23.2 is current
- `pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp` — InstrumentHandler* functions, HandlerFor() signature
- `prometheus.io/docs/guides/go-application/` — Official Prometheus Go instrumentation guide; custom registry pattern
- Existing project source: `internal/store/migrations/001_init.sql`, `002_tokens.sql` — goose migration format confirmed
- Existing project source: `internal/api/router.go`, `internal/api/middleware/auth.go` — route structure, context key pattern confirmed
- Existing project source: `internal/browser/session.go` — WithAccount() structure for metrics injection points confirmed
- Existing project source: `go.mod` — confirmed `go-chi/chi/v5` in go.mod (WrapResponseWriter available)

### Secondary (MEDIUM confidence)
- `pkg.go.dev/sigs.k8s.io/external-dns/plan` — Plan/Changes struct; confirmed Changes struct with Create/UpdateOld/UpdateNew/Delete slices; adapted concept for model.Record
- `github.com/go-chi/metrics` — go-chi official metrics package overview; confirmed RoutePattern label cardinality guidance
- `betterstack.com/community/guides/monitoring/prometheus-golang/` — Multi-source verified prometheus instrumentation guide

### Tertiary (LOW confidence)
- WebSearch results about DNS sync partial failure response patterns — industry convention for HTTP 200 vs 207; not from official spec

---

## Metadata

**Confidence breakdown:**
- Standard stack (prometheus/client_golang): HIGH — verified version v1.23.2 from pkg.go.dev
- Architecture (metrics registry pattern): HIGH — verified from official Prometheus docs + project source
- Architecture (audit log): HIGH — goose migration pattern confirmed from existing project migrations
- Architecture (sync engine): HIGH — diff algorithm design verified against external-dns concept; adapted to project's model.Record
- Pitfalls: MEDIUM-HIGH — cardinality explosion, duplicate registration pitfalls are well-documented in official guides

**Research date:** 2026-02-28
**Valid until:** 2026-03-30 (prometheus/client_golang stable; sync engine is project-specific so no expiry)

# Phase 6: BIND Import/Export + Admin UI - Research

**Researched:** 2026-02-28
**Domain:** miekg/dns zone file I/O, a-h/templ server-side rendering, htmx 2.x, Go session cookies
**Confidence:** HIGH (core libraries), MEDIUM (admin auth pattern), HIGH (integration with existing codebase)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Import behavior**
- Additive only — Records present in the live zone but absent from the import file are kept untouched. Import never deletes.
- No `?mode=replace` toggle. Full-replace is out of scope for this phase.
- Unsupported record types (e.g., HTTPS, SVCB — not in v1RecordTypes): skip them, do not fail. The response includes a dry-run-style report listing which records were applied, which were skipped (with reason), and which failed.
- Response shape mirrors `POST /sync`: `{ "applied": [...], "skipped": [...], "had_errors": bool }`. HTTP 200 always (same pattern as sync handler).
- `?dry_run=true` supported — returns what would change without applying, consistent with sync engine behavior.

**Export format**
- User-created records only — A, AAAA, CNAME, MX, TXT, SRV, CAA, NS (user-added delegation NS, not HE's authoritative NS), and any other record in the zone that maps to a v1RecordType.
- SOA record is excluded — HE manages it and it is not exposed in the REST API.
- HE's own authoritative NS records are excluded.
- Output: standard BIND zone file with `$ORIGIN`, `$TTL` (derived from first record or 3600 default), and per-record lines. Generated via `miekg/dns` `dns.RR` serialization.

**Admin UI authentication**
- Two auth mechanisms, both protecting `/admin`:
  1. Session cookie — Admin submits username + password on a login form (`POST /admin/login`). Credentials come from env vars (`ADMIN_USERNAME`, `ADMIN_PASSWORD`). On success, server issues a signed session cookie (HttpOnly, SameSite=Strict). Logout clears the cookie.
  2. HTTP Basic Auth — Also accepted on all `/admin` routes for scripted/curl access. Same `ADMIN_USERNAME` / `ADMIN_PASSWORD` env vars. Checked before the cookie path.
- The REST API's Bearer JWT is NOT used for `/admin` — admin UI has its own auth layer.
- Both mechanisms must be active simultaneously (Basic Auth checked first, then cookie session).

**Admin UI scope**
- Accounts: list, register, remove
- Tokens: list (per account), issue, revoke
- Zones: list zones per account (read-only view — zone create/delete still require curl; zone CRUD via browser is expensive and out of scope for UI)
- Sync: trigger `POST /api/v1/zones/{zoneID}/sync` with dry_run toggle from UI; display result diff
- Audit log: paginated table of recent audit_log entries (account, zone, action, timestamp, success/error)

Claude's Discretion: pagination size (default 50), exact table columns, form layout within pages.

**Admin UI style**
- Layout: Fixed sidebar (240px) + header bar (56px) + scrollable main area — app-shell pattern
- Color tokens: `--background: #fff` / `#242424` (dark), `--foreground: #213547` / `rgba(255,255,255,0.87)`, `--border: #e4e4e7` / `#27272a`, `--accent: #f4f4f5`, `--primary: #18181b`. Accent color: `#646cff` (purple, active nav, primary buttons, focus rings).
- Typography: `system-ui, Avenir, Helvetica, Arial, sans-serif`; `line-height: 1.5`; `font-size: 14px` body, `16px` headings
- Border radius: `0.5rem` for cards, buttons, nav items, badges
- Single embedded CSS file. No Tailwind build step. No external CDN dependency.
- htmx for inline CRUD without full-page reloads; hx-confirm for destructive actions.

**Implementation stack**
- templ + htmx, embedded in Go binary with `//go:embed`, no separate build step
- miekg/dns for zone file parsing and serialization

### Claude's Discretion
- Pagination size (default 50), exact table columns, form layout within pages.
- Session signing approach (HMAC-SHA256 using standard library or minimal library).

### Deferred Ideas (OUT OF SCOPE)
- Zone create/delete via admin UI
- Full BIND-format record editing in UI
- RBAC for admin UI (viewer vs admin role within /admin)
- Multi-account sync trigger from UI
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BIND-01 | `GET /api/v1/zones/{zone_id}/export` returns zone records in standard BIND zone file format (RFC 1035 compatible), generated using `miekg/dns` | miekg/dns v1.1.72 ZoneParser, dns.RR.String() serialization, RR_Header struct patterns documented below |
| BIND-02 | `POST /api/v1/zones/{zone_id}/import` accepts a BIND zone file body, parses it using `miekg/dns`, creates/updates records to match file contents | miekg/dns NewZoneParser, Next() iteration, type assertion patterns; SOA/NS filter approach documented |
| BIND-03 | BIND import uses the sync engine internally (diff + apply), not blind create — existing matching records are left untouched | reconcile.DiffRecords + Apply already exist (Phase 5); import feeds additive-only desired set to DiffRecords |
| UI-01 | Embedded web UI at `/admin`, built with `templ` templates and `htmx` for dynamic updates | templ v0.3.1001 + htmx 2.0.8 patterns, go:embed for assets, chi sub-router for /admin |
| UI-02 | Admin UI allows listing, registering, and removing dns.he.net accounts | templ component patterns, htmx hx-delete with hx-confirm, chi handler composition |
| UI-03 | Admin UI allows issuing, listing, and revoking bearer tokens per account | Same templ/htmx pattern; calls existing API handlers internally via in-process function calls |
| UI-04 | Admin UI is protected by admin-level authentication (HTTP Basic Auth + session cookie) | HMAC-SHA256 signed cookie + Basic Auth middleware pattern documented below |
| UI-05 | Admin UI is optional for operation — all functionality available via REST API | Architectural constraint: admin UI calls existing service/store functions directly, adds no new REST dependencies |
</phase_requirements>

---

## Summary

Phase 6 has two technically distinct sub-problems. The BIND I/O work is primarily a data-mapping exercise: translating between `model.Record` structs (already defined) and `dns.RR` concrete types from `miekg/dns`, then wiring two new HTTP handlers (`GET /export`, `POST /import`) that use the existing `reconcile` package for import. The admin UI work is a complete new layer: a chi sub-router at `/admin`, templ component files that compile to Go, an htmx-driven frontend, and a session auth middleware that operates independently of the Bearer JWT system.

The key integration insight for BIND import is that it feeds a *desired subset* (only the records from the file, with v1-unsupported types skipped) into `reconcile.DiffRecords`, but the *current* records used for diffing come from the live zone. Because import is additive-only, the Delete slice of the resulting `SyncPlan` must be cleared before calling `reconcile.Apply`. This is the only deviation from the standard sync pattern.

For the admin UI, the project decisions lock in templ + htmx with a single embedded CSS file — meaning no Tailwind build, no npm. The `_templ.go` generated files should be committed to the repository so `go build` works without requiring the `templ` CLI in the build environment. htmx 2.0.8 (`htmx.min.js`) should be downloaded and embedded as a static asset alongside the CSS file. The session cookie auth layer is fully independent of the existing Bearer JWT middleware and should live in a separate `internal/api/admin` package.

**Primary recommendation:** Implement in three sequential tasks — (1) BIND export handler, (2) BIND import handler, (3) admin UI (auth middleware, layouts, account/token/zone/sync/audit pages). `miekg/dns` v1.1.72 is already the right choice; add `github.com/a-h/templ` as the only new dependency (htmx is a self-hosted static file, not a Go module).

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/miekg/dns` | v1.1.72 | BIND zone file parsing (`ZoneParser`) and RR serialization (`rr.String()`) | De-facto DNS library for Go; RFC 1035 compliant; all record types including CAA, SRV |
| `github.com/a-h/templ` | v0.3.1001 | Server-side HTML components compiled to Go functions | Type-safe, no runtime template parsing, works with `go:embed`, compiles to plain Go |
| htmx | 2.0.8 | Progressive HTML enhancement without JavaScript build chain | Eliminates JS build step; hx-delete/hx-post/hx-swap covers all CRUD interactions needed |

### Supporting (already in project)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/go-chi/chi/v5` | v5.2.5 | Sub-router at `/admin` | Already used for all existing routes; `r.Route("/admin", ...)` |
| `crypto/hmac` + `crypto/sha256` | stdlib | Signing session cookies | Standard library; no external dependency needed for simple signed cookie |
| `embed` | stdlib | Embedding static assets (CSS, htmx.min.js) | Standard library since Go 1.16; already used conceptually in project |
| `github.com/caarlos0/env/v11` | v11.4.0 | Reading `ADMIN_USERNAME` / `ADMIN_PASSWORD` env vars | Already used for all other config |

### New Dependency Required
```bash
go get github.com/a-h/templ@latest
go install github.com/a-h/templ/cmd/templ@latest
```

**No npm, no Node.js.** htmx.min.js is downloaded once and embedded.

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| templ | html/template | html/template has no type checking, runtime errors, no IDE support — templ generates Go code |
| HMAC-SHA256 stdlib | gorilla/securecookie | gorilla adds a dep for a 20-line equivalent; stdlib is sufficient for single-user admin cookie |
| miekg/dns v1 | miekg/dnsv2 (Codeberg) | v2 is still stabilizing (Jan 2026 note: "should be good replacement"); v1.1.72 is battle-tested |

---

## Architecture Patterns

### Recommended Project Structure

```
internal/
├── bindio/                  # New package for BIND zone file I/O
│   ├── export.go            # model.Record slice → BIND zone file string
│   └── import.go            # BIND zone file string → []model.Record + SkippedRecord list
internal/api/
├── admin/                   # New package for admin UI
│   ├── middleware.go        # AdminAuth middleware (Basic Auth + session cookie)
│   ├── handlers.go          # Admin HTTP handlers (accounts, tokens, zones, sync, audit)
│   └── router.go            # RegisterAdminRoutes(r chi.Router, ...) function
internal/api/admin/templates/
│   ├── layout.templ         # Base layout: sidebar + header + content slot
│   ├── login.templ          # Login form page
│   ├── accounts.templ       # Accounts list + register/remove forms
│   ├── tokens.templ         # Tokens list + issue/revoke forms
│   ├── zones.templ          # Zones read-only list
│   ├── sync.templ           # Sync page: zone picker, dry-run result, apply button
│   └── audit.templ          # Audit log paginated table
static/
│   ├── admin.css            # Single embedded CSS file (CSS custom properties, no Tailwind)
│   └── htmx.min.js          # htmx 2.0.8, downloaded once, embedded
```

The `bindio` package is self-contained (no browser, no HTTP) so it can be unit-tested without mocks.
The `admin` package imports `bindio` indirectly through the sync handler.

### Pattern 1: miekg/dns RR Type-Switch for Export

**What:** Convert each `model.Record` to the appropriate concrete `dns.RR` type, then call `rr.String()` for BIND output.
**When to use:** In `bindio/export.go` — one function per record type.

```go
// Source: pkg.go.dev/github.com/miekg/dns (verified)
import "github.com/miekg/dns"

func recordToRR(rec model.Record, origin string) (dns.RR, error) {
    // All names must be FQDN (ending in '.') for miekg/dns to serialize correctly.
    // If rec.Name is relative (e.g. "www"), append the zone origin.
    name := rec.Name
    if !dns.IsFqdn(name) {
        name = dns.Fqdn(name + "." + origin)
    }

    hdr := dns.RR_Header{
        Name:   name,
        Class:  dns.ClassINET,
        Ttl:    uint32(rec.TTL),
    }

    switch rec.Type {
    case model.RecordTypeA:
        hdr.Rrtype = dns.TypeA
        return &dns.A{Hdr: hdr, A: net.ParseIP(rec.Content)}, nil
    case model.RecordTypeAAAA:
        hdr.Rrtype = dns.TypeAAAA
        return &dns.AAAA{Hdr: hdr, AAAA: net.ParseIP(rec.Content)}, nil
    case model.RecordTypeCNAME:
        hdr.Rrtype = dns.TypeCNAME
        return &dns.CNAME{Hdr: hdr, Cname: dns.Fqdn(rec.Content)}, nil
    case model.RecordTypeMX:
        hdr.Rrtype = dns.TypeMX
        return &dns.MX{Hdr: hdr, Preference: uint16(rec.Priority), Mx: dns.Fqdn(rec.Content)}, nil
    case model.RecordTypeTXT:
        hdr.Rrtype = dns.TypeTXT
        return &dns.TXT{Hdr: hdr, Txt: []string{rec.Content}}, nil
    case model.RecordTypeNS:
        hdr.Rrtype = dns.TypeNS
        return &dns.NS{Hdr: hdr, Ns: dns.Fqdn(rec.Content)}, nil
    case model.RecordTypeSRV:
        hdr.Rrtype = dns.TypeSRV
        return &dns.SRV{Hdr: hdr, Priority: uint16(rec.Priority),
            Weight: uint16(rec.Weight), Port: uint16(rec.Port),
            Target: dns.Fqdn(rec.Target)}, nil
    case model.RecordTypeCAA:
        // CAA content from the API is stored as "flags tag value"
        // e.g. "0 issue letsencrypt.org" — must be parsed back into 3 fields.
        parts := strings.Fields(rec.Content)
        flags, _ := strconv.ParseUint(parts[0], 10, 8)
        hdr.Rrtype = dns.TypeCAA
        return &dns.CAA{Hdr: hdr, Flag: uint8(flags), Tag: parts[1], Value: parts[2]}, nil
    default:
        return nil, fmt.Errorf("unsupported type %s", rec.Type)
    }
}
```

### Pattern 2: miekg/dns ZoneParser for Import

**What:** Stream-parse a BIND zone file, filter unsupported/SOA/HE-NS records, accumulate desired slice.
**When to use:** In `bindio/import.go`.

```go
// Source: pkg.go.dev/github.com/miekg/dns#ZoneParser (verified)
func ParseZoneFile(body string, origin string) (desired []model.Record, skipped []SkippedRecord, err error) {
    zp := dns.NewZoneParser(strings.NewReader(body), dns.Fqdn(origin), "import")
    zp.SetDefaultTTL(3600)

    for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
        hdr := rr.Header()

        // Skip SOA — HE manages it, not exposed in API.
        if hdr.Rrtype == dns.TypeSOA {
            skipped = append(skipped, SkippedRecord{Name: hdr.Name, Type: "SOA", Reason: "SOA is managed by HE"})
            continue
        }

        rec, convErr := rrToRecord(rr, origin)
        if convErr != nil {
            // Unsupported type — skip, do not fail.
            skipped = append(skipped, SkippedRecord{Name: hdr.Name, Type: dns.TypeToString[hdr.Rrtype], Reason: convErr.Error()})
            continue
        }
        desired = append(desired, rec)
    }

    return desired, skipped, zp.Err()
}
```

**Critical:** `dns.TypeToString[hdr.Rrtype]` converts uint16 type code to human-readable string (e.g. "HTTPS") for skip reports.

### Pattern 3: Import Handler Wiring (Additive-Only)

**What:** BIND import reuses `reconcile.DiffRecords` but clears the Delete slice before calling `Apply`.
**When to use:** In `handlers/bindimport.go`.

```go
// The import is additive-only (CONTEXT.md decision: no deletes).
// DiffRecords computes what the sync engine WOULD do, then we suppress deletions.
plan := reconcile.DiffRecords(currentRecords, desiredFromFile)
plan.Delete = nil  // Additive only — records absent from import file are kept
```

The import response shape matches sync:
```go
type importHTTPResponse struct {
    DryRun    bool                   `json:"dry_run"`
    Applied   []reconcile.SyncResult `json:"applied"`
    Skipped   []bindio.SkippedRecord `json:"skipped"`
    HadErrors bool                   `json:"had_errors"`
}
```

### Pattern 4: templ Component + htmx Render

**What:** templ components are called like Go functions; rendered to `http.ResponseWriter` via the `Component.Render(ctx, w)` method.
**When to use:** All admin UI handlers.

```go
// admin/templates/accounts.templ — component definition
templ AccountsPage(accounts []model.Account, data PageData) {
    @Layout(data) {
        <div class="page-header">
            <h1>Accounts</h1>
            <button hx-get="/admin/accounts/new" hx-target="#modal" hx-swap="innerHTML">
                Register Account
            </button>
        </div>
        <table id="accounts-table">
            for _, acc := range accounts {
                @AccountRow(acc)
            }
        </table>
        <div id="modal"></div>
    }
}

templ AccountRow(acc model.Account) {
    <tr id={ "row-" + acc.ID }>
        <td>{ acc.Username }</td>
        <td>{ acc.CreatedAt.Format("2006-01-02") }</td>
        <td>
            <button
                hx-delete={ "/admin/accounts/" + acc.ID }
                hx-target={ "#row-" + acc.ID }
                hx-swap="outerHTML swap:1s"
                hx-confirm="Delete account? All associated tokens will be revoked.">
                Remove
            </button>
        </td>
    </tr>
}
```

```go
// admin/handlers.go — render in chi handler
func ListAccountsHandler(db *sql.DB) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        accounts, err := store.ListAccounts(db)
        if err != nil {
            http.Error(w, "internal error", 500)
            return
        }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        _ = templates.AccountsPage(accounts, pageData(r)).Render(r.Context(), w)
    }
}
```

**Before using component in HTTP handler:** Run `templ generate` once. Commit generated `*_templ.go` files to repository.

### Pattern 5: Admin Auth Middleware (Basic Auth + Session Cookie)

**What:** Middleware checks Basic Auth header first, then signed session cookie. If neither validates, redirects to `/admin/login`.
**When to use:** As `r.Use(adminAuth)` on the `/admin` chi sub-router.

```go
// Source: alexedwards.net/blog/working-with-cookies-in-go (verified pattern)
// HMAC-SHA256 signed cookie — no external dep.
const sessionCookieName = "admin_session"

func AdminAuth(username, password string, signingKey []byte) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // 1. Check HTTP Basic Auth first (for curl/scripted access).
            if u, p, ok := r.BasicAuth(); ok {
                if u == username && p == password {
                    next.ServeHTTP(w, r)
                    return
                }
                // Basic Auth provided but wrong — 401 not redirect.
                w.Header().Set("WWW-Authenticate", `Basic realm="admin"`)
                http.Error(w, "Unauthorized", http.StatusUnauthorized)
                return
            }

            // 2. Check signed session cookie.
            if validateSessionCookie(r, signingKey) {
                next.ServeHTTP(w, r)
                return
            }

            // 3. Neither — redirect to login form.
            http.Redirect(w, r, "/admin/login", http.StatusFound)
        })
    }
}

func validateSessionCookie(r *http.Request, key []byte) bool {
    c, err := r.Cookie(sessionCookieName)
    if err != nil {
        return false
    }
    // Cookie value format: hex(HMAC-SHA256("admin_session" + username)) + ":" + username
    // Split and verify HMAC.
    parts := strings.SplitN(c.Value, ":", 2)
    if len(parts) != 2 {
        return false
    }
    mac := hmac.New(sha256.New, key)
    mac.Write([]byte(sessionCookieName + parts[1]))
    expected := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(parts[0]), []byte(expected))
}
```

### Pattern 6: go:embed for Static Assets

**What:** Embed CSS and htmx.min.js into the binary so no filesystem dependency at runtime.
**When to use:** In `internal/api/admin/static.go`.

```go
package admin

import "embed"

//go:embed static/admin.css static/htmx.min.js
var staticFS embed.FS
```

Register in the router:
```go
r.Handle("/admin/static/*",
    http.StripPrefix("/admin/static/",
        http.FileServer(http.FS(staticFS))),
)
```

**Critical note:** The `//go:embed` directive path is relative to the Go source file. Static assets must live inside `internal/api/admin/static/` or a sibling directory.

### Pattern 7: BIND Export Response

**What:** Export generates zone file text with `$ORIGIN` and `$TTL` headers.
**When to use:** `GET /api/v1/zones/{zoneID}/export` handler.

```go
// Content-Type for BIND zone files is text/plain or application/dns-zone
w.Header().Set("Content-Type", "text/plain; charset=utf-8")
w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zone"`, zoneName))
fmt.Fprintf(w, "$ORIGIN %s.\n", zoneName)
fmt.Fprintf(w, "$TTL %d\n", defaultTTL) // 3600 if no records or first record's TTL
for _, rr := range rrList {
    fmt.Fprintln(w, rr.String())
}
```

### Anti-Patterns to Avoid

- **Using `r.URL.Path` directly in Prometheus middleware** — already addressed in project; admin routes must also use chi RoutePattern().
- **Running `templ generate` in Docker build** — commit `_templ.go` files instead; keeps the binary build self-contained.
- **Using `dns.NewRR(string)` for export** — inefficient string round-trip; construct typed RR structs directly (Pattern 1).
- **Storing ADMIN_PASSWORD plaintext in session cookie** — store only the signed username; never the password.
- **Calling REST API over HTTP for admin UI actions** — admin handlers should call store functions and session manager directly, not make HTTP requests to localhost (avoids auth token management in UI layer).
- **Forgetting to clear `plan.Delete` in import handler** — the additive-only decision means Delete must be explicitly set to nil or empty before Apply.
- **Names not FQDN in miekg/dns** — failing to append the trailing `.` causes miekg/dns `String()` to produce relative names in the zone file output. Always use `dns.Fqdn()`.
- **TXT record multi-string encoding** — `dns.TXT.Txt` is `[]string`, each element max 255 chars. For records from HE, the content is a single string. Wrap in `[]string{rec.Content}` — do not split manually.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| BIND zone file parsing | Custom string parser | `miekg/dns` ZoneParser | Zone files have complex escaping, $INCLUDE, $GENERATE, multi-line TXT, relative/absolute names — custom parsers miss edge cases |
| DNS RR to string serialization | fmt.Sprintf formatting | `rr.String()` from miekg/dns | BIND format has record-type-specific whitespace, quoting rules (TXT escaping), priority ordering (SRV) that are easy to get wrong |
| HTML component rendering | `html/template` with `Execute` | `templ` generated components | html/template has no type-checking; templ catches data binding errors at compile time |
| Session signing | Custom base64+hash scheme | HMAC-SHA256 over `crypto/hmac` + `crypto/sha256` | The standard library pattern is well-established; gorilla/securecookie adds a dep for no benefit at this scale |
| Type code to string conversion | Custom map | `dns.TypeToString[hdr.Rrtype]` | Already defined in miekg/dns; covers all 80+ DNS types including future ones |

**Key insight:** The zone file format is deceptively complex. Even simple cases like a TXT record with embedded quotes require specific escaping that `rr.String()` handles correctly and a hand-rolled `fmt.Sprintf` does not.

---

## Common Pitfalls

### Pitfall 1: Non-FQDN Names Break miekg/dns Serialization
**What goes wrong:** `rr.String()` produces `www` instead of `www.example.com.` — the zone file is syntactically invalid.
**Why it happens:** miekg/dns requires fully-qualified domain names (ending in `.`). HE scraper returns relative names without trailing dot.
**How to avoid:** Always call `dns.Fqdn(name)` when constructing RR_Header.Name. Use `dns.IsFqdn(name)` to check first.
**Warning signs:** Zone file output has records without trailing dots; BIND rejects the file.

### Pitfall 2: CAA Content Needs Parsing Before Export
**What goes wrong:** `panic: index out of range` or invalid CAA record in zone file.
**Why it happens:** The API stores CAA content as a single string `"0 issue letsencrypt.org"` (3 space-separated fields). miekg/dns `dns.CAA` struct has separate `Flag uint8`, `Tag string`, `Value string` fields.
**How to avoid:** Split `rec.Content` with `strings.Fields()` in the recordToRR() type switch, parse `parts[0]` as uint8 flags.
**Warning signs:** CAA records export as blank or cause index out of range panics.

### Pitfall 3: Import Skipped-Record Response Needs Careful HTTP 200 Handling
**What goes wrong:** Caller thinks the import failed when unsupported types were present.
**Why it happens:** The CONTEXT.md decision is HTTP 200 always (mirrors sync handler), with `had_errors` in body for partial failures. Skipped unsupported types are NOT errors — they go in `skipped[]`, not as HTTP 4xx.
**How to avoid:** Distinguish `skipped` (informational, expected) from `had_errors` (something that was attempted and failed). Only set `had_errors=true` when `reconcile.Apply` returns a result with Status=="error".
**Warning signs:** Importing a zone with HTTPS records returns 422 instead of 200 with skipped entry.

### Pitfall 4: templ Generate Must Run Before go build
**What goes wrong:** `go build` fails with "undefined: templates.AccountsPage".
**Why it happens:** templ source files (`*.templ`) are not valid Go; `templ generate` produces `*_templ.go` that is compiled by `go build`.
**How to avoid:** Run `templ generate` once after writing `.templ` files. Commit the generated `*_templ.go` files to the repository. Add `go generate ./internal/api/admin/templates/...` to Makefile.
**Warning signs:** CI fails with undefined symbol referencing a templ component.

### Pitfall 5: Admin Cookie Must Use SameSite=Strict Not Lax
**What goes wrong:** CSRF vulnerability on destructive admin actions.
**Why it happens:** The admin panel performs destructive POST/DELETE mutations. SameSite=Lax allows cookies on top-level navigation (GET) but doesn't fully protect POST mutations from cross-site form attacks.
**How to avoid:** Set `SameSite: http.SameSiteStrictMode` on the session cookie. The admin UI is not a public-facing site that needs cross-site navigation support.
**Warning signs:** Using SameSite=Lax and having no additional CSRF protection.

### Pitfall 6: go:embed Path Is Relative to Source File
**What goes wrong:** `go build` fails: "pattern static/admin.css: no matching files found".
**Why it happens:** `//go:embed` paths resolve relative to the Go source file that contains the directive, not the working directory or module root.
**How to avoid:** Place `static.go` (with the embed directive) in the same directory that contains the `static/` folder. Example: `internal/api/admin/static.go` embeds `internal/api/admin/static/`.
**Warning signs:** Build fails with "no matching files found" despite the file existing.

### Pitfall 7: HE NS Records Must Be Filtered From Export
**What goes wrong:** Zone file contains `example.com. 86400 IN NS ns1.he.net.` which re-imports HE's own nameservers as user-created records, causing confusion.
**Why it happens:** ListRecords from the browser scrapes ALL records including SOA and HE's authoritative NS records. Export must filter these.
**How to avoid:** In the export handler, filter out: (1) any record where `r.Type == model.RecordTypeSOA`, (2) NS records where the content matches known HE NS hostnames (`ns1.he.net` through `ns5.he.net`). Also: only export records whose type is in `v1RecordTypes` (same filter used by BIND-01 requirement).
**Warning signs:** Re-importing an exported zone file duplicates NS records pointing at HE nameservers.

### Pitfall 8: ZoneParser Origin Must Include Trailing Dot
**What goes wrong:** All parsed record names are incorrect (relative instead of absolute).
**Why it happens:** `dns.NewZoneParser(r, origin, file)` — the origin parameter must be a FQDN with trailing dot. Passing `"example.com"` without dot causes all names to be interpreted incorrectly.
**How to avoid:** Always pass `dns.Fqdn(zoneName)` as the origin to `dns.NewZoneParser`.
**Warning signs:** Parsed record names have doubled domain suffixes or extra dots.

---

## Code Examples

Verified patterns from official sources:

### Building BIND Zone File Header
```go
// Source: miekg/dns docs — ZoneParser origin handling
func ExportZone(records []model.Record, zoneName string) (string, error) {
    var buf strings.Builder

    // Determine default TTL from first record, fall back to 3600.
    defaultTTL := 3600
    if len(records) > 0 {
        defaultTTL = records[0].TTL
    }

    fmt.Fprintf(&buf, "$ORIGIN %s\n", dns.Fqdn(zoneName))
    fmt.Fprintf(&buf, "$TTL %d\n\n", defaultTTL)

    for _, rec := range records {
        rr, err := recordToRR(rec, zoneName)
        if err != nil {
            continue // skip unmappable types
        }
        fmt.Fprintln(&buf, rr.String())
    }
    return buf.String(), nil
}
```

### Iterating ZoneParser Records
```go
// Source: pkg.go.dev/github.com/miekg/dns#ZoneParser (verified)
zp := dns.NewZoneParser(strings.NewReader(body), dns.Fqdn(origin), "")
zp.SetDefaultTTL(3600)

for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
    hdr := rr.Header()
    typeName := dns.TypeToString[hdr.Rrtype]
    // type-switch on hdr.Rrtype to extract fields
}
if err := zp.Err(); err != nil {
    return fmt.Errorf("zone file parse error: %w", err)
}
```

### templ Layout with Children Slot
```templ
// Source: templ.guide/syntax-and-usage/template-composition (verified)
templ Layout(data PageData) {
    <!DOCTYPE html>
    <html lang="en">
    <head>
        <meta charset="utf-8"/>
        <title>{ data.Title } | DNS Admin</title>
        <link rel="stylesheet" href="/admin/static/admin.css"/>
        <script src="/admin/static/htmx.min.js"></script>
    </head>
    <body>
        <nav class="sidebar">
            @Sidebar(data.ActivePage)
        </nav>
        <main class="main-content">
            { children... }
        </main>
    </body>
    </html>
}
```

### Calling Layout in a Page Component
```templ
templ AccountsPage(accounts []model.Account, data PageData) {
    @Layout(data) {
        <h1>Accounts</h1>
        // page content here
    }
}
```

### Rendering a templ Component in Chi Handler
```go
// Source: pkg.go.dev/github.com/a-h/templ (verified)
func (h *AdminHandlers) ListAccounts(w http.ResponseWriter, r *http.Request) {
    accounts, _ := store.ListAccounts(h.db)
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    _ = templates.AccountsPage(accounts, h.pageData(r, "accounts")).Render(r.Context(), w)
}
```

### htmx Delete Row Pattern
```html
<!-- Source: htmx.org/examples/delete-row/ (verified) -->
<tr id="row-{{ .ID }}">
    <td>{{ .Username }}</td>
    <td>
        <button
            hx-delete="/admin/accounts/{{ .ID }}"
            hx-target="#row-{{ .ID }}"
            hx-swap="outerHTML swap:500ms"
            hx-confirm="Delete this account and all its tokens?">
            Remove
        </button>
    </td>
</tr>
```

Server returns empty 200 body — htmx swaps the row out entirely.

### Session Cookie Issue on Login
```go
// Source: alexedwards.net/blog/working-with-cookies-in-go (verified pattern)
func issueSessionCookie(w http.ResponseWriter, username string, signingKey []byte) {
    mac := hmac.New(sha256.New, signingKey)
    mac.Write([]byte(sessionCookieName + username))
    sig := hex.EncodeToString(mac.Sum(nil))
    cookieVal := sig + ":" + username

    http.SetCookie(w, &http.Cookie{
        Name:     sessionCookieName,
        Value:    cookieVal,
        Path:     "/admin",
        HttpOnly: true,
        SameSite: http.SameSiteStrictMode,
        Secure:   false, // Set to true when behind TLS terminator
    })
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `html/template` for Go web UIs | `templ` compiled components | 2021-present (templ); mainstream 2023+ | Type-safe, IDE-supported, compile-time errors |
| React/Vue SPAs for admin panels | htmx + server-rendered HTML | 2020-present (htmx 1.x); htmx 2.0 June 2024 | No build chain, simpler deployment, Go binary includes everything |
| gorilla/mux + gorilla/sessions | chi + stdlib cookies | Gorilla archived 2022, revived but chi now standard | Less dependency surface; HMAC cookies trivial in stdlib |
| miekg/dns v1 only | miekg/dnsv2 available (Codeberg) | v2 beta stabilizing Jan 2026 | Use v1.1.72 now; v2 migration possible in future phase |

**Deprecated/outdated:**
- `gorilla/sessions` for simple admin cookies: Adds 3 deps for a 30-line stdlib equivalent.
- `dns.ParseZone()` function: Replaced by `ZoneParser` API in miekg/dns. `ParseZone` was the old synchronous function; `ZoneParser` with `Next()` is the current streaming API.

---

## Open Questions

1. **HE NS filtering: content-based vs count-based**
   - What we know: The export must exclude HE's own authoritative NS records. HE uses `ns1.he.net` through `ns5.he.net`.
   - What's unclear: Whether HE ever adds or changes their NS hostnames. Hardcoding `ns[1-5].he.net` is brittle if HE adds nameservers.
   - Recommendation: Filter NS records whose content matches the pattern `*.he.net.` (wildcard suffix match). This is safe because operators would never add a legitimate NS delegation pointing to `*.he.net`.

2. **ADMIN_SESSION_KEY env var or derived from JWT_SECRET**
   - What we know: Session cookie signing needs a secret key. The project already has `JWT_SECRET`.
   - What's unclear: Whether to reuse `JWT_SECRET` for session signing or introduce a separate `ADMIN_SESSION_KEY`.
   - Recommendation: Introduce a separate `ADMIN_SESSION_KEY` env var (32+ random bytes). Reusing `JWT_SECRET` creates coupling — rotating the JWT secret would invalidate all admin sessions.

3. **Import: ZoneParser origin for files without $ORIGIN directive**
   - What we know: `dns.NewZoneParser(r, origin, file)` — the origin parameter serves as fallback when no `$ORIGIN` is in the file.
   - What's unclear: What to use as the origin parameter when importing to a specific zone. The zone name (from the URL path `{zoneID}`) should be looked up to get the domain name.
   - Recommendation: Fetch zone name from the database (or from the ListZones scrape) before parsing. Pass `dns.Fqdn(zoneName)` as origin to ZoneParser.

---

## Sources

### Primary (HIGH confidence)
- `pkg.go.dev/github.com/miekg/dns` — ZoneParser API, RR_Header struct, all concrete RR types verified
- `pkg.go.dev/github.com/a-h/templ` — Component interface, templ.Handler, version v0.3.1001 verified (Feb 28, 2026)
- `htmx.org/docs/` — htmx version 2.0.8, hx-delete/hx-confirm/hx-swap documented
- `alexedwards.net/blog/working-with-cookies-in-go` — HMAC-SHA256 signed cookie pattern

### Secondary (MEDIUM confidence)
- `templ.guide/syntax-and-usage/template-composition` — children slot pattern, layout composition
- `templ.guide/core-concepts/template-generation` — commit `_templ.go` files recommendation
- `github.com/a-h/templ/discussions/419` — community consensus: commit generated files

### Tertiary (LOW confidence)
- HE NS filter approach (`*.he.net`) — inferred from HE's documented nameservers; not officially verified as complete list

---

## Metadata

**Confidence breakdown:**
- Standard stack (miekg/dns, templ, htmx): HIGH — all verified via official docs with version numbers
- Architecture (package layout, handler patterns): HIGH — consistent with existing project patterns
- BIND I/O patterns (RR struct fields, ZoneParser): HIGH — verified via pkg.go.dev
- Admin auth pattern: MEDIUM — HMAC-SHA256 cookie verified; SameSite=Strict recommendation is security best practice
- Pitfalls: HIGH — all derive from verified library behavior (FQDN requirement, CAA content format, go:embed paths)

**Research date:** 2026-02-28
**Valid until:** 2026-03-28 (stable libraries; templ may release minor updates but API is stable)

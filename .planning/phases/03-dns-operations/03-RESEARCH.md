# Phase 3: DNS Operations - Research

**Researched:** 2026-02-28
**Domain:** Go REST API handlers, browser page objects (Playwright), idempotency patterns, DNS record field validation
**Confidence:** HIGH — all findings derived from direct codebase inspection of Phases 1 and 2, plus verified HE.net selectors from 01-CONTEXT.md

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| ZONE-01 | `GET /api/v1/zones` — list all zones for the account, scraped live | ZoneListPage.ListZones() already exists; handler wires it up via WithAccount |
| ZONE-02 | Each zone response includes stable zone ID and domain name | model.Zone{ID, Name} already populated from delete-img value/name attributes |
| ZONE-03 | `POST /api/v1/zones` — add a new zone on dns.he.net | AddZonePanel selectors already in selectors.go; needs AddZone page method |
| ZONE-04 | `DELETE /api/v1/zones/{zone_id}` — remove a zone on dns.he.net | delete_dom() JS function pattern already understood from CONTEXT.md |
| REC-01 | `GET /api/v1/zones/{zone_id}/records` — list all records, scraped live | GetRecordRows() exists; needs column parsing to produce model.Record |
| REC-02 | Each record includes HE record ID, type, name, value, TTL, type-specific fields | Columns verified in CONTEXT.md: Name/Type/TTL/Priority/Data/DDNS/Delete |
| REC-03 | `POST /api/v1/zones/{zone_id}/records` — create record for 8 v1 types | FillAndSubmit() exists; needs idempotency pre-check + post-creation ID read |
| REC-04 | `PUT /api/v1/zones/{zone_id}/records/{record_id}` — update record | EditExistingRecord() + FillAndSubmit() exists; handler orchestration needed |
| REC-05 | `DELETE /api/v1/zones/{zone_id}/records/{record_id}` — delete record | DeleteRecord() JS call exists; needs idempotent 204-on-missing |
| REC-06 | `GET /api/v1/zones/{zone_id}/records/{record_id}` — get single record by ID | Filter GetRecordRows() result by ID; parse fields from row |
| REC-07 | Create is idempotent: type+name+content match → 200 with existing record | Pre-check scan before FillAndSubmit; return existing record on match |
| REC-08 | Delete is idempotent: missing record → 204, not 404 | Scan rows after navigate; if not found return 204 immediately |
| REC-09 | All record types enforce correct field validation | Per-type Go validation before any browser call |
| API-05 | Every record/zone response includes stable IDs for Terraform state tracking | Zone ID from delete-img value; record ID from tr[id] attribute |
| API-06 | GET records supports `type` and `name` query param filtering | Server-side filter on GetRecordRows() result after parsing |
| PERF-01 | Read operations under 10s | opTimeout=30s already configured; navigation+scrape typically 3-5s |
| PERF-02 | Write operations under 15s | opTimeout=30s is the ceiling; typical form submit takes 5-8s |
| PERF-03 | Queued requests get response within 60s | queueTimeout=60s already in Config (OPERATION_QUEUE_TIMEOUT_SEC=60) |
| COMPAT-01 | Stable IDs + full field state + consistent JSON schema | model.Zone and model.Record structs already define the schema |
| COMPAT-02 | Idempotent create (200 on conflict) and idempotent delete (204 on missing) | Same as REC-07 and REC-08 |
| COMPAT-03 | Binary compiles on Linux amd64 and arm64 | CGO-free stack: modernc.org/sqlite + playwright-go; GOARCH cross-compile |
</phase_requirements>

---

## Summary

Phase 3 builds on a solid foundation. The browser page objects for record CRUD (create, edit, delete) and zone listing already exist in `internal/browser/pages/`. The gaps are: (1) parsing structured field values out of record rows (currently `GetRecordRows()` only returns `DisplayText` and `ID`), (2) zone add/delete page methods, (3) the HTTP handler layer that wires page objects to API routes, and (4) per-type field validation.

The central design insight is that **all DNS operations go through `sm.WithAccount(ctx, accountID, func(page) error)`**. Inside the func, handlers create the appropriate page objects, navigate, scrape or mutate, and return. No caching layer is needed in Phase 3 — the requirements explicitly state "scraped live from dns.he.net" (REC-01, ZONE-01).

Idempotency is implemented at the handler layer via a navigate-then-scan pattern: before creating a record, scrape existing rows and check for type+name+content match. Before deleting, check if the row exists at all. This requires parsing structured fields from the `RecordRow.DisplayText`, which means the record row parser is the most critical new piece of code in this phase.

**Primary recommendation:** Implement a `ParseRecordRow()` function in the pages package that extracts typed fields from the zone records table columns, then build handlers on top of the existing page object methods.

---

## Standard Stack

All libraries are already in `go.mod`. No new dependencies are needed for Phase 3.

### Core (Already Present)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/playwright-community/playwright-go` | v0.5700.1 | Browser automation | Already chosen in Phase 1; all page objects use it |
| `github.com/go-chi/chi/v5` | v5.2.5 | HTTP router | Already the router; zones/records routes extend the same chi tree |
| `modernc.org/sqlite` | v1.46.1 | SQLite (CGO-free) | Already the DB driver; needed for COMPAT-03 cross-compile |
| `github.com/pressly/goose/v3` | v3.27.0 | DB migrations | Already the migration runner; Phase 3 adds no new DB tables |
| `github.com/caarlos0/env/v11` | v11.4.0 | Config from env vars | Config struct already handles all timeouts |

### Supporting (Already Present)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `net` (stdlib) | Go 1.25 | IPv4/IPv6 validation | `net.ParseIP()` for A/AAAA validation |
| `net/netip` (stdlib) | Go 1.25 | Typed IP parsing | `netip.ParseAddr().Is4()` / `.Is6()` for strict validation |
| `strconv` (stdlib) | Go 1.25 | Int parsing | Priority/weight/port field conversion |
| `regexp` (stdlib) | Go 1.25 | Domain name validation | Already used in accounts.go for ID patterns |

**No new dependencies needed for Phase 3.**

---

## Architecture Patterns

### Recommended Project Structure After Phase 3

```
internal/
├── browser/
│   └── pages/
│       ├── login.go          — existing
│       ├── zonelist.go       — existing; ADD AddZone(), DeleteZone()
│       ├── recordform.go     — existing; ADD ReadRecordFields()
│       ├── recordparser.go   — NEW: ParseRecordRows() structured parsing
│       └── selectors.go      — existing; complete, no changes needed
├── api/
│   ├── handlers/
│   │   ├── accounts.go       — existing
│   │   ├── tokens.go         — existing
│   │   ├── health.go         — existing
│   │   ├── zones.go          — NEW: ListZones, AddZone, DeleteZone handlers
│   │   └── records.go        — NEW: CRUD handlers for DNS records
│   ├── middleware/           — existing, no changes
│   ├── response/             — existing, no changes
│   └── router.go             — EXTEND: add zones + records routes
├── model/
│   └── types.go              — existing; model.Zone + model.Record already defined
├── validation/
│   └── record.go             — NEW: per-type field validation
└── store/
    └── migrations/
        └── 003_dns_cache.sql — NOT needed (no-cache decision; skip this migration)
```

### Pattern 1: WithAccount Handler Pattern

This is the established pattern from Phase 1/2. All zone and record handlers follow it exactly.

**What:** Handler calls `sm.WithAccount(ctx, accountID, func(page playwright.Page) error {...})`. Inside the func, page objects are created fresh, navigation happens, scraping or mutation runs, result is captured via closure.

**When to use:** Every handler that touches dns.he.net (all zone and record endpoints).

**Example (zone list — the model for all other handlers):**
```go
// Source: pattern derived from session.go WithAccount + zonelist.go ListZones
func ListZones(sm *browser.SessionManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        claims := middleware.ClaimsFromContext(r.Context())
        accountID := claims.AccountID

        var zones []model.Zone
        err := sm.WithAccount(r.Context(), accountID, func(page playwright.Page) error {
            zlp := pages.NewZoneListPage(page)
            if err := zlp.NavigateToZoneList(); err != nil {
                return err
            }
            var scanErr error
            zones, scanErr = zlp.ListZones()
            return scanErr
        })
        if err != nil {
            mapBrowserError(w, err)
            return
        }

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        _ = json.NewEncoder(w).Encode(map[string]interface{}{"zones": zones})
    }
}
```

**Key detail:** The `accountID` comes from `middleware.ClaimsFromContext(r.Context()).AccountID`. The token IS the account selector — no `{accountID}` URL param for zones/records (per Phase 1 CONTEXT.md decision: "no account ID in path" for DNS routes).

### Pattern 2: Browser Error Mapping

Every handler needs to map browser-layer errors to HTTP status codes consistently. A shared `mapBrowserError()` helper in the handlers package avoids duplication.

```go
// Source: browser/errors.go (ErrQueueTimeout, ErrSessionUnhealthy)
func mapBrowserError(w http.ResponseWriter, err error) {
    switch {
    case errors.Is(err, browser.ErrQueueTimeout):
        // Per PERF-03: queued requests get 429 with Retry-After
        w.Header().Set("Retry-After", "10")
        response.WriteError(w, http.StatusTooManyRequests, "queue_timeout",
            "Request queued too long; try again")
    case errors.Is(err, browser.ErrSessionUnhealthy):
        response.WriteError(w, http.StatusServiceUnavailable, "session_unhealthy",
            "Browser session is unavailable; try again")
    default:
        response.WriteError(w, http.StatusInternalServerError, "browser_error",
            "Browser operation failed")
    }
}
```

### Pattern 3: Idempotent Create (REC-07, COMPAT-02)

Navigate to zone → scan existing rows → check type+name+content match → if match found, return 200 with existing record → if no match, call FillAndSubmit → navigate back → find new row by scanning again.

```go
// Source: pattern derived from recordform.go FillAndSubmit + zonelist.go GetRecordRows
err := sm.WithAccount(ctx, accountID, func(page playwright.Page) error {
    zlp := pages.NewZoneListPage(page)
    if err := zlp.NavigateToZone(req.ZoneID); err != nil {
        return err
    }

    // Idempotency check: scan before create
    existingRows, err := zlp.GetRecordRows()
    if err != nil {
        return err
    }
    for _, row := range existingRows {
        parsed, err := pages.ParseRecordRow(row)
        if err != nil {
            continue // skip unparseable rows (locked SOA rows etc.)
        }
        if parsed.Type == req.Type && parsed.Name == req.Name && contentMatches(parsed, req) {
            result = parsed  // idempotency hit: return existing
            alreadyExists = true
            return nil
        }
    }

    // Not found: create
    rfp := pages.NewRecordFormPage(page)
    if err := rfp.OpenNewRecordForm(string(req.Type)); err != nil {
        return err
    }
    if err := rfp.FillAndSubmit(req); err != nil {
        return err
    }

    // Re-scan to find the newly created row (to get its ID)
    newRows, err := zlp.GetRecordRows()
    if err != nil {
        return err
    }
    result, err = findNewRow(existingRows, newRows, req)
    return err
})
```

**Why re-scan after create:** HE.net does not provide a redirect or response body with the new record ID. The only way to get the newly assigned `tr[id]` is to diff the before/after row sets and identify the new entry.

### Pattern 4: Idempotent Delete (REC-08, COMPAT-02)

Navigate to zone → scan rows → if record ID not found, return 204 immediately → if found, call DeleteRecord → return 204.

```go
err := sm.WithAccount(ctx, accountID, func(page playwright.Page) error {
    zlp := pages.NewZoneListPage(page)
    if err := zlp.NavigateToZone(zoneID); err != nil {
        return err
    }

    rows, err := zlp.GetRecordRows()
    if err != nil {
        return err
    }

    // Find the target row to get its type (needed for deleteRecord JS call)
    var targetRow *pages.RecordRow
    for i, row := range rows {
        if row.ID == recordID {
            targetRow = &rows[i]
            break
        }
    }
    if targetRow == nil {
        notFound = true  // idempotent: already deleted
        return nil
    }

    rfp := pages.NewRecordFormPage(page)
    return rfp.DeleteRecord(recordID, zoneName, string(targetRow.Type))
})
if notFound || err == nil {
    w.WriteHeader(http.StatusNoContent)
    return
}
```

**Critical detail:** `DeleteRecord()` in recordform.go calls `deleteRecord(id, zone, rtype)` JS function. The `zone` param is the zone NAME (not ID), and `rtype` is the record type string. The zone name must be passed from the caller — handlers need to look it up or pass it through. The zone name is available from `GetRecordRows()` row parsing (it's in the hidden `td[0]` which contains zone ID, but zone name comes from the URL path).

**Revised approach:** Handlers receive `zoneID` in the URL. To get the zone name for the JS delete call, do a `NavigateToZoneList()` + `ListZones()` to resolve name from ID, OR store the zone name in the `model.Zone` response (already done — Zone.Name is populated from the delete-img `name` attribute during ListZones). Zone name must be passed through from a preceding list call or resolved separately. The simplest approach: after NavigateToZone, the page URL contains the zone ID; the zone name must be obtained from a zones list call before navigation, or parsed from the page title/breadcrumb.

**Cleaner approach (recommended):** Add `GetZoneName(zoneID string)` to ZoneListPage — navigate to zone list, call `GetZoneID` inverse: scan delete-imgs for the one with `value=zoneID`, return its `name` attribute. This resolves zone name from zone ID in one pass.

### Pattern 5: Record Row Parsing

This is the core missing piece. `GetRecordRows()` currently returns only `ID`, `DisplayText`, and `IsLocked`. Phase 3 needs structured field extraction from each row.

**HTML structure (from CONTEXT.md):**
```
tr[id="RECORD_ID"][class="dns_tr"]
  td[0] hidden  = zone ID (text content of first hidden td)
  td[1] hidden  = record ID (redundant with tr.id)
  td[2]         = formatted display: "Name | Type | TTL | Priority | Data | DDNS"
  td.dns_delete = delete button
```

The "formatted display" is the key. Based on CONTEXT.md, the columns are: Name | Type | TTL | Priority | Data | DDNS | Delete. These are separate `<td>` elements, not a single merged string.

**Two implementation options:**

Option A — Column-by-column locator (HIGH confidence):
```go
// In ParseRecordRows, for each row locator:
tds := row.Locator("td")
name, _ := tds.Nth(2).InnerText()   // Name column (0-indexed, after two hidden tds)
recType, _ := tds.Nth(3).InnerText() // Type column
ttlStr, _ := tds.Nth(4).InnerText() // TTL column
priority, _ := tds.Nth(5).InnerText() // Priority column (empty for non-MX/SRV)
data, _ := tds.Nth(6).InnerText()    // Data/Content column
ddns, _ := tds.Nth(7).IsChecked()    // DDNS checkbox (or InnerText)
```

Option B — Edit form readback: Click the row to open the edit form, then read `input#_name`, `input#_content`, etc. More reliable but triggers a UI state change and takes longer.

**Recommendation: Option A (column parsing)** for list/get endpoints; Option B (form readback) only if column parsing proves unreliable during integration tests. The column structure is consistent and verified.

**IMPORTANT caveat:** The exact column indices need verification via Playwright MCP or integration test against the live site. The two hidden `td` elements may or may not be visible in the DOM column count depending on their `display:none` vs `type=hidden` implementation. This should be verified before coding.

### Anti-Patterns to Avoid

- **Caching zone/record data in SQLite:** REC-01 explicitly says "scraped live, no local cache." Adding a cache table adds complexity and staleness bugs without solving a performance problem the requirements impose. PERF-01 allows 10s for reads — live scraping is well within that.
- **Reading form fields via page.Evaluate() with arbitrary JS:** Use Playwright locators for form field reading. The existing codebase consistently uses locators — do not introduce `page.Evaluate()` for field access except where the page object already uses it for JS functions (editFormHandler, deleteRecord).
- **Putting selectors in handler code:** BROWSER-07 requires all selectors to stay in `internal/browser/pages/`. Handlers must not reference CSS selector strings directly.
- **Returning 404 on already-deleted record:** REC-08 mandates 204. Map "not found after navigate" to 204, not 404.
- **Zone ID vs Zone Name confusion in deleteRecord:** The JS `deleteRecord(id, zone, rtype)` takes zone NAME as second argument (verified in CONTEXT.md). Passing zone ID here will fail silently or cause a JS error.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| IPv4 address validation | Custom regex | `net.ParseIP(s).To4() != nil` or `netip.ParseAddr(s).Is4()` | stdlib handles all edge cases including leading zeros, short forms |
| IPv6 address validation | Custom regex | `net.ParseIP(s).To16() != nil` or `netip.ParseAddr(s).Is6()` | stdlib handles compressed forms, mixed notation |
| Domain name validation | Complex RFC regex | Simple length + character check sufficient for SEC-04 scope | Full IDNA validation is overkill; HE.net validates server-side |
| Browser error to HTTP mapping | switch in every handler | Shared `mapBrowserError(w, err)` helper once | DRY; consistent status codes across all endpoints |
| Record type allowlist | String comparison everywhere | `model.ValidV1Types` map/set constant | Single source of truth for COMPAT-01 type subset |

**Key insight:** DNS field validation only needs to be "good enough to prevent injection" (SEC-04) and "catch obvious mistakes" (REC-09). The browser form's own `validateEdit()` JS function is the final gate — the API layer provides a friendlier early error message.

---

## Common Pitfalls

### Pitfall 1: Record Row Column Index Assumptions

**What goes wrong:** Code assumes td[2]=Name, td[3]=Type but the two "hidden" tds may have different display properties than expected. If they are `display:none` elements, Playwright still counts them in `.Nth(N)` — td[0] and td[1] are the hidden zone/record ID cells, so td[2] would be Name. But if the HTML structure changes or hidden tds are implemented as `input[type=hidden]` inside tds, the count is different.

**Why it happens:** The CONTEXT.md says "td[0] hidden = zone ID, td[1] hidden = record ID" but does not clarify whether these are literal `<td>` elements or `<input type=hidden>`. The two hidden inputs (`input#_zoneid`, `input#_recordid`) are in the FORM, not in the table rows.

**How to avoid:** Write a targeted Playwright MCP query against the live site before implementing `ParseRecordRow`. Check: `document.querySelectorAll('tr.dns_tr')[0].querySelectorAll('td').length` and the text content of each td. Then code the indices based on actual count.

**Warning signs:** Integration test returns empty/wrong Name fields; Priority always 0 even for MX records.

### Pitfall 2: Zone Name Required for deleteRecord JS Call

**What goes wrong:** `DeleteRecord(recordID, zoneName, recordType)` requires the zone name, not zone ID. Handlers receive zone ID from the URL path. If the handler passes zone ID as zone name, the JS call silently fails or causes an error, and no deletion occurs — but the page reloads anyway (network idle fires), so the handler thinks it succeeded.

**Why it happens:** The JS function signature and its HTML source are easy to miss. DeleteRecord in recordform.go correctly passes zoneName, but handlers must supply it.

**How to avoid:** Add a `GetZoneName(zoneID string) (string, error)` method to ZoneListPage. Call it inside the WithAccount func before calling DeleteRecord. Alternatively, validate after deletion by re-scanning rows.

**Warning signs:** Records not actually deleted; DELETE endpoint returns 204 but record still appears on subsequent GET.

### Pitfall 3: Idempotency Check on Content Match for SRV Records

**What goes wrong:** For SRV records, `model.Record.Content` is empty — SRV uses Priority+Weight+Port+Target instead. The idempotency check `contentMatches(parsed, req)` must handle SRV specially.

**Why it happens:** SRV is a special case in FillRecord (no Content field, uses 4 separate fields). The same special-casing applies to the equality check.

**How to avoid:** Define a `recordsMatch(a, b model.Record) bool` function that checks Type+Name and then dispatches to type-specific field comparison (content for most, priority+weight+port+target for SRV, priority+content for MX).

**Warning signs:** SRV records always appear as "already exists" or never match even when duplicate.

### Pitfall 4: Re-scan After Create Returns Wrong Record

**What goes wrong:** After `FillAndSubmit()`, the before/after diff of `GetRecordRows()` may find multiple new rows if two concurrent requests (different accounts, same zone) happen to add records between the two scans. Or the diff finds zero new rows if the form submission silently failed.

**Why it happens:** There is no atomic "here is your new record ID" response from HE.net. The diff approach is inherently racy across concurrent operations on the same zone from different accounts.

**How to avoid:** The `WithAccount` per-account mutex serializes all operations for a single account. If two different accounts happen to share the same zone (which HE.net does allow via delegation), the race can occur. For v1, this is acceptable — document it. The safer fallback: after create, search by type+name+content match (same as idempotency check). If exactly one match exists, return it as the created record.

**Warning signs:** POST /records returns wrong record ID; subsequent GET returns 404 for that ID.

### Pitfall 5: TTL Value Validation

**What goes wrong:** Code accepts any integer TTL, but HE.net only accepts 10 specific values. Sending an invalid TTL causes the select option to not be found, which throws a Playwright error when `SelectOption` finds no matching value.

**Why it happens:** TTL is a `<select>` not a free-text input. Valid values come from the verified CONTEXT.md: 300, 900, 1800, 3600, 7200, 14400, 28800, 43200, 86400, 172800.

**How to avoid:** Validate TTL in the API layer before any browser call. Return HTTP 400 with an actionable error message listing the valid values.

**Warning signs:** Playwright error "no option with value '1234' in select" in browser operation logs.

### Pitfall 6: COMPAT-03 Cross-compilation

**What goes wrong:** `playwright-go` requires downloading Chromium binaries at build time (via `playwright install`). The binary itself is CGO-free, but the Playwright driver (Node.js subprocess) may complicate `GOARCH=arm64` builds.

**Why it happens:** `modernc.org/sqlite` is CGO-free (already chosen for this). `playwright-go` Go package is also CGO-free. The Chromium download happens at deploy time, not build time — `playwright install` is a separate step in the Dockerfile. The Go binary cross-compiles fine; only the Docker image is restricted to linux/amd64 per COMPAT-03.

**How to avoid:** For `go build`, `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` works cleanly. For Docker, the multi-stage Dockerfile only needs `linux/amd64` target — arm64 Docker image is not required. Verify with a CI cross-compile step.

**Warning signs:** Build failure mentioning CGO when targeting arm64.

---

## Code Examples

Verified patterns from codebase inspection:

### Zone Add (New Method for ZoneListPage)

```go
// Pattern: div#add_zone panel → fill add_domain input → submit
// Source: selectors.go SelectorAddZonePanel/Input/Submit + CONTEXT.md "Add Zone Form"
func (zp *ZoneListPage) AddZone(domainName string) error {
    // The add_zone panel is triggered by a sidebar link click OR is already visible.
    // Navigate to zone list first to ensure we are on the right page.
    if err := zp.page.Locator(SelectorAddZonePanel).WaitFor(); err != nil {
        return fmt.Errorf("wait for add_zone panel: %w", err)
    }

    if err := zp.page.Locator(SelectorAddZoneInput).Fill(domainName); err != nil {
        return fmt.Errorf("fill add_domain: %w", err)
    }

    if err := zp.page.Locator(SelectorAddZoneSubmit).Click(); err != nil {
        return fmt.Errorf("click Add Domain!: %w", err)
    }

    if err := zp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
        State: playwright.LoadStateNetworkidle,
    }); err != nil {
        return fmt.Errorf("wait after add zone: %w", err)
    }

    return nil
}
```

**Note:** The `div#add_zone` panel may not be visible by default — it may require clicking a "Add a new domain" sidebar link first. The selectors already exist (SelectorAddZonePanel, SelectorAddZoneInput, SelectorAddZoneSubmit). The page flow needs empirical verification: check if the panel auto-opens or needs a click trigger. See CONTEXT.md: "Trigger: click link with text 'Add a new domain' in sidebar."

A sidebar link selector needs to be added: `a:has-text("Add a new domain")` or inspect the exact link `href`/`id` via Playwright MCP.

### Zone Delete (New Method for ZoneListPage)

```go
// Pattern: click img[alt="delete"][value=zoneID] → triggers delete_dom(this) JS → form submits
// Source: CONTEXT.md "Delete Zone" section
func (zp *ZoneListPage) DeleteZone(zoneID string) error {
    selector := fmt.Sprintf(`img[alt="delete"][value="%s"]`, zoneID)
    if err := zp.page.Locator(selector).Click(); err != nil {
        return fmt.Errorf("click delete zone %q: %w", zoneID, err)
    }

    if err := zp.page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
        State: playwright.LoadStateNetworkidle,
    }); err != nil {
        return fmt.Errorf("wait after delete zone %q: %w", zoneID, err)
    }

    return nil
}
```

**Note:** The `delete_dom(this)` JS function fills `form[name="remove_domain"]` and submits it. Playwright clicking the img triggers the onclick. A browser confirmation dialog may appear — needs empirical verification. If a confirm dialog appears, use `page.OnDialog(func(dialog playwright.Dialog) { dialog.Accept() })` before the click.

### Zone Name Resolution (New Method for ZoneListPage)

```go
// Source: derived from existing GetZoneID() pattern in zonelist.go
func (zp *ZoneListPage) GetZoneName(zoneID string) (string, error) {
    selector := fmt.Sprintf(`img[alt="delete"][value="%s"]`, zoneID)
    img := zp.page.Locator(selector)

    name, err := img.GetAttribute("name")
    if err != nil {
        return "", fmt.Errorf("get zone name for ID %q: %w", zoneID, err)
    }
    if name == "" {
        return "", fmt.Errorf("zone ID %q not found", zoneID)
    }

    return name, nil
}
```

This is the inverse of the existing `GetZoneID()` method. Both read from the same `img[alt="delete"]` elements — `name` attribute = zone name, `value` attribute = zone ID.

### Per-Type Field Validation

```go
// Source: derived from model/types.go field definitions and REC-09 + CONTEXT.md TTL values
// Location: internal/validation/record.go (new package)

// ValidTTLs is the exhaustive set of TTL values accepted by dns.he.net.
// Source: CONTEXT.md "TTL select options" (verified 2026-02-27 against live site).
var ValidTTLs = map[int]bool{
    300: true, 900: true, 1800: true, 3600: true, 7200: true,
    14400: true, 28800: true, 43200: true, 86400: true, 172800: true,
}

// V1RecordTypes is the set of DNS record types supported in v1 (COMPAT-01).
var V1RecordTypes = map[model.RecordType]bool{
    model.RecordTypeA:     true,
    model.RecordTypeAAAA:  true,
    model.RecordTypeCNAME: true,
    model.RecordTypeMX:    true,
    model.RecordTypeTXT:   true,
    model.RecordTypeSRV:   true,
    model.RecordTypeCAA:   true,
    model.RecordTypeNS:    true,
}

func ValidateRecord(rec model.Record) error {
    if !V1RecordTypes[rec.Type] {
        return fmt.Errorf("unsupported record type %q in v1; supported: A, AAAA, CNAME, MX, TXT, SRV, CAA, NS", rec.Type)
    }

    if !ValidTTLs[rec.TTL] {
        return fmt.Errorf("invalid TTL %d; valid values: 300, 900, 1800, 3600, 7200, 14400, 28800, 43200, 86400, 172800", rec.TTL)
    }

    switch rec.Type {
    case model.RecordTypeA:
        ip := net.ParseIP(rec.Content)
        if ip == nil || ip.To4() == nil {
            return fmt.Errorf("A record content must be a valid IPv4 address, got %q", rec.Content)
        }

    case model.RecordTypeAAAA:
        ip := net.ParseIP(rec.Content)
        if ip == nil || ip.To4() != nil {
            return fmt.Errorf("AAAA record content must be a valid IPv6 address, got %q", rec.Content)
        }

    case model.RecordTypeMX:
        if rec.Priority < 1 || rec.Priority > 65535 {
            return fmt.Errorf("MX priority must be 1-65535, got %d", rec.Priority)
        }
        if strings.TrimSpace(rec.Content) == "" {
            return fmt.Errorf("MX record content (mail server) must not be empty")
        }

    case model.RecordTypeSRV:
        if rec.Priority < 0 || rec.Priority > 65535 {
            return fmt.Errorf("SRV priority must be 0-65535, got %d", rec.Priority)
        }
        if rec.Weight < 0 || rec.Weight > 65535 {
            return fmt.Errorf("SRV weight must be 0-65535, got %d", rec.Weight)
        }
        if rec.Port < 1 || rec.Port > 65535 {
            return fmt.Errorf("SRV port must be 1-65535, got %d", rec.Port)
        }
        if strings.TrimSpace(rec.Target) == "" {
            return fmt.Errorf("SRV target must not be empty")
        }

    case model.RecordTypeCNAME, model.RecordTypeNS, model.RecordTypeCAA:
        if strings.TrimSpace(rec.Content) == "" {
            return fmt.Errorf("%s record content must not be empty", rec.Type)
        }

    case model.RecordTypeTXT:
        if len(rec.Content) > 255 {
            // Single TXT string limit per RFC 1035 — HE.net may handle longer via chunking
            // but API validates the single-string input. Flag as LOW confidence.
            return fmt.Errorf("TXT content exceeds 255 characters; use multiple strings for longer values")
        }
    }

    if strings.TrimSpace(rec.Name) == "" {
        return fmt.Errorf("record name must not be empty")
    }

    return nil
}
```

### Router Extension

```go
// Source: router.go pattern — extend existing /api/v1 route group
// Location: internal/api/router.go

r.Route("/api/v1", func(r chi.Router) {
    r.Use(middleware.BearerAuth(db, secret))

    // ... existing account/token routes unchanged ...

    // Zone routes — account ID implicit from token claims (per Phase 1 CONTEXT.md decision)
    r.Route("/zones", func(r chi.Router) {
        r.Get("/", handlers.ListZones(sm))
        r.With(middleware.RequireAdmin).Post("/", handlers.AddZone(sm))

        r.Route("/{zoneID}", func(r chi.Router) {
            r.With(middleware.RequireAdmin).Delete("/", handlers.DeleteZone(sm))

            r.Route("/records", func(r chi.Router) {
                r.Get("/", handlers.ListRecords(sm))
                r.With(middleware.RequireAdmin).Post("/", handlers.CreateRecord(sm))

                r.Route("/{recordID}", func(r chi.Router) {
                    r.Get("/", handlers.GetRecord(sm))
                    r.With(middleware.RequireAdmin).Put("/", handlers.UpdateRecord(sm))
                    r.With(middleware.RequireAdmin).Delete("/", handlers.DeleteRecord(sm))
                })
            })
        })
    })
})
```

**Important:** The `{zoneID}` in the URL is the HE.net internal zone ID (e.g., "1294061"), not a database primary key. No SQLite lookup needed — it goes directly to the browser navigation URL `?hosted_dns_zoneid={zoneID}`.

### Query Parameter Filtering (API-06)

```go
// Source: net/http stdlib, no new libraries needed
// In ListRecords handler:
typeFilter := r.URL.Query().Get("type")    // e.g., "A", "MX"
nameFilter := r.URL.Query().Get("name")    // e.g., "mail.example.com"

// After parsing all rows into []model.Record:
filtered := make([]model.Record, 0, len(records))
for _, rec := range records {
    if typeFilter != "" && string(rec.Type) != strings.ToUpper(typeFilter) {
        continue
    }
    if nameFilter != "" && rec.Name != nameFilter {
        continue
    }
    filtered = append(filtered, rec)
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Rod (go-rod) browser automation | playwright-go v0.5700.1 | Phase 1 decision | Auto-waits, Inspector, WaitForLoadState; already implemented |
| mattn/go-sqlite3 (CGO) | modernc.org/sqlite (CGO-free) | Phase 1 decision | CGO-free enables COMPAT-03 cross-compile without GCC |
| Per-request browser context | Per-account session with mutex | Phase 1 design | Serialized operations; handles HE.net single-session model |

**No deprecated approaches in the current stack.** All Phase 1 and 2 patterns carry forward unchanged into Phase 3.

---

## Open Questions

1. **Add Zone Panel Trigger**
   - What we know: `div#add_zone` is the panel; `input[name="add_domain"]` is the field; selectors are in selectors.go
   - What's unclear: Does the panel auto-show on the zone list page, or does it require a click on a "Add a new domain" sidebar link?
   - CONTEXT.md says: "Trigger: click link with text 'Add a new domain' in sidebar"
   - Recommendation: Add a selector for the trigger link (e.g., `SelectorAddZoneTrigger = a:has-text("Add a new domain")`) and call it before filling the form. Verify via Playwright MCP before implementing.

2. **Delete Zone Confirmation Dialog**
   - What we know: `delete_dom(this)` fills a form and submits
   - What's unclear: Does a browser `confirm()` dialog appear before the form submits?
   - Recommendation: Register `page.OnDialog(func(d playwright.Dialog) { d.Accept() })` before clicking the delete image. If no dialog appears, the handler is a no-op. Verify empirically.

3. **Record Row Column Indices**
   - What we know: Columns are Name | Type | TTL | Priority | Data | DDNS | Delete per CONTEXT.md
   - What's unclear: Exact `<td>` indices when counted via Playwright `.Nth(N)`. The two "hidden" columns (zone ID, record ID) in CONTEXT.md may refer to `input[type=hidden]` inside a td, or fully hidden tds.
   - Recommendation: Use Playwright MCP to query `document.querySelectorAll('tr.dns_tr')[0].children` before coding ParseRecordRow. This is the highest-risk implementation detail.

4. **TXT Record Length Limit**
   - What we know: RFC 1035 limits a single TXT string to 255 bytes; multiple strings can be concatenated
   - What's unclear: HE.net's actual enforced limit; whether the single input field handles multi-string TXT
   - Recommendation: Accept strings up to 2048 chars (HE.net's likely limit) and let the server validate. Keep the validation permissive (empty check only) and remove the 255-char limit noted above. Confidence: LOW — needs empirical testing.

5. **ReadRecordFields After Edit Form Open**
   - What we know: `EditExistingRecord(recordID)` clicks the row and opens the form
   - What's unclear: Do the form fields (`input#_name`, `input#_content`, etc.) reliably populate with the existing record's values for all 17 types?
   - Recommendation: Implement `ReadRecordFields() (model.Record, error)` that reads all visible inputs after `EditExistingRecord()`. This is the Option B fallback for parsing records when column parsing is unreliable. Use for GET single record if column parsing proves fragile.

---

## Implementation Guide: What to Build, in Order

This section summarizes the build sequence for the planner. Each item maps to one or more plan tasks.

### 03-01: Zone Page Objects + Zone API Handlers

**New page object methods (zonelist.go additions):**

1. `ZoneListPage.AddZone(domainName string) error`
   - Click "Add a new domain" sidebar trigger
   - Fill `input[name="add_domain"]`
   - Click `input[name="submit"][value="Add Domain!"]`
   - WaitForLoadState NetworkIdle
   - Return nil or error

2. `ZoneListPage.DeleteZone(zoneID string) error`
   - Register dialog handler (accept confirm if present)
   - Click `img[alt="delete"][value="{zoneID}"]`
   - WaitForLoadState NetworkIdle
   - Return nil or error

3. `ZoneListPage.GetZoneName(zoneID string) (string, error)`
   - Locate `img[alt="delete"][value="{zoneID}"]`
   - Return `name` attribute

**New selectors (selectors.go addition):**

```go
SelectorAddZoneTrigger = `a:has-text("Add a new domain")`
// Or: need empirical verification of the exact link selector
```

**New handlers (internal/api/handlers/zones.go):**

- `ListZones(sm) http.HandlerFunc` — GET /api/v1/zones
- `AddZone(sm) http.HandlerFunc` — POST /api/v1/zones
- `DeleteZone(sm) http.HandlerFunc` — DELETE /api/v1/zones/{zoneID}
- `mapBrowserError(w, err)` — shared error mapper (also used by records.go)

**Router extension (router.go):** Add `/zones` route group.

### 03-02: Record Page Objects + Record API Handlers

**New page object code:**

4. `ParseRecordRow(page playwright.Page, rowID string) (model.Record, error)` — or a method on ZoneListPage
   - Locate `tr#{rowID}`
   - Read td columns for Name, Type, TTL, Priority, Data, DDNS
   - Return populated model.Record

5. `ZoneListPage.GetParsedRecords() ([]model.Record, error)`
   - Calls GetRecordRows() then ParseRecordRow for each editable row
   - Returns slice of model.Record (excludes locked SOA rows)

6. `RecordFormPage.ReadRecordFields() (model.Record, error)` (optional fallback)
   - Reads open edit form fields
   - Returns model.Record with all visible fields populated

**New handlers (internal/api/handlers/records.go):**

- `ListRecords(sm) http.HandlerFunc` — GET /api/v1/zones/{zoneID}/records (supports ?type=&name= filters)
- `GetRecord(sm) http.HandlerFunc` — GET /api/v1/zones/{zoneID}/records/{recordID}
- `CreateRecord(sm) http.HandlerFunc` — POST with idempotency pre-check
- `UpdateRecord(sm) http.HandlerFunc` — PUT, uses EditExistingRecord + FillAndSubmit
- `DeleteRecord(sm) http.HandlerFunc` — DELETE with idempotent 204-on-missing

**New validation package (internal/validation/record.go):**

- `ValidateRecord(rec model.Record) error` — per-type rules
- `ValidTTLs` map
- `V1RecordTypes` map
- `RecordsMatch(a, b model.Record) bool` — for idempotency comparison

### 03-03: Validation, Filtering, Cross-compilation Verification

- Add TTL validation and per-type field validation to all write handlers (POST, PUT)
- Add query parameter filtering to ListRecords (type, name)
- Add `CloseAccount(accountID string)` to SessionManager (referenced in DeleteAccount TODO comment in accounts.go)
- Verify `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` succeeds
- Integration test against live dns.he.net test zone (royalheadshots.online, ID: 1294061)

---

## API Response Schemas

These schemas define the JSON contract for COMPAT-01 and API-05.

### Zone Response

```json
{
  "id": "1294061",
  "name": "royalheadshots.online",
  "account_id": "prod"
}
```

List response: `{"zones": [...]}`

### Record Response

```json
{
  "id": "8299837822",
  "zone_id": "1294061",
  "type": "A",
  "name": "mail.royalheadshots.online",
  "content": "1.2.3.4",
  "ttl": 300,
  "priority": 0,
  "weight": 0,
  "port": 0,
  "target": "",
  "dynamic": false
}
```

For MX: `priority` is populated, `content` is the mail server hostname.
For SRV: `priority`, `weight`, `port`, `target` are populated; `content` is empty.
For other types: `priority`, `weight`, `port`, `target` are 0/""; `content` is the value.

List response: `{"records": [...]}`

**Note on `account_id` in Zone:** The model.Zone struct already has `AccountID string`. Handlers populate this from `claims.AccountID` (not from the browser — HE.net does not expose account ID in the HTML). This satisfies API-05 (stable IDs) and COMPAT-01 (consistent schema).

---

## Timeout Configuration Verification (PERF-01, PERF-02, PERF-03)

Current Config defaults (from config.go):

| Parameter | Default | Env Var | Relevant Req |
|-----------|---------|---------|--------------|
| `OperationTimeoutSec` | 30 | `OPERATION_TIMEOUT_SEC` | PERF-01 (read <10s), PERF-02 (write <15s) |
| `OperationQueueTimeoutSec` | 60 | `OPERATION_QUEUE_TIMEOUT_SEC` | PERF-03 (<60s queue) |
| `MinOperationDelaySec` | 1.5 | `MIN_OPERATION_DELAY_SEC` | BROWSER-08 rate limit |
| `SessionMaxAgeSec` | 1800 | `SESSION_MAX_AGE_SEC` | BROWSER-05 stale session |

**Analysis:**
- PERF-01 (<10s reads): A zone list + navigate typically takes 3-6s. The 30s opTimeout is a safe ceiling. No changes needed.
- PERF-02 (<15s writes): Navigate + form fill + submit typically takes 5-10s. The 30s opTimeout is the absolute ceiling; the PERF-02 requirement is about typical performance, not timeout config.
- PERF-03 (<60s queued): `queueTimeout=60s` exactly matches the requirement. No changes needed.

**Recommendation:** Config defaults are correct. No new config fields needed for Phase 3. Document the performance expectations in handler-level comments.

---

## Sources

### Primary (HIGH confidence)

- `internal/browser/pages/zonelist.go` — ZoneListPage methods: NavigateToZoneList, ListZones, GetZoneID, NavigateToZone, GetRecordRows, RecordRow struct
- `internal/browser/pages/recordform.go` — RecordFormPage methods: OpenNewRecordForm, FillRecord, SubmitRecord, FillAndSubmit, EditExistingRecord, DeleteRecord
- `internal/browser/pages/selectors.go` — All verified selectors including SelectorAddZonePanel/Input/Submit
- `internal/browser/session.go` — WithAccount signature and contract
- `internal/api/handlers/accounts.go` — Handler pattern (closure, claims extraction, error handling)
- `internal/api/router.go` — Chi route structure to extend
- `internal/model/types.go` — Zone and Record structs
- `internal/config/config.go` — Timeout configuration
- `.planning/phases/01-foundation-browser-core/01-CONTEXT.md` — Verified HE.net HTML structure, selectors, TTL values, JS function signatures
- `internal/browser/errors.go` — ErrQueueTimeout, ErrSessionUnhealthy

### Secondary (MEDIUM confidence)

- `go.mod` — Confirmed CGO-free dependency stack (modernc.org/sqlite, playwright-go) enabling COMPAT-03 cross-compile
- Go stdlib `net` package — `net.ParseIP()` for IP validation (standard, well-documented behavior)

### Tertiary (LOW confidence — needs empirical verification)

- Add Zone trigger link selector: "Add a new domain" sidebar link exact selector not verified via live site
- Delete Zone confirmation dialog: whether `confirm()` dialog appears during zone deletion
- Record row column indices for ParseRecordRow: exact `<td>` count and indices not confirmed against live DOM

---

## Metadata

**Confidence breakdown:**

- Standard stack: HIGH — all libraries already in go.mod from Phases 1-2
- Architecture (handlers, WithAccount pattern): HIGH — direct pattern from existing accounts.go/tokens.go
- Existing page objects (what's there): HIGH — read directly from source files
- Gaps (what needs building): HIGH — derived from direct codebase + requirements analysis
- Field validation rules: HIGH for types/TTL values (from CONTEXT.md); MEDIUM for domain validation edge cases
- Browser behavior (add zone panel, delete zone dialog): LOW — CONTEXT.md describes structure but JS behavior needs empirical confirmation
- Record row column parsing: MEDIUM — CONTEXT.md describes columns but exact td indices need live verification
- Cross-compile: HIGH — CGO-free stack confirmed from go.mod

**Research date:** 2026-02-28
**Valid until:** 2026-04-01 (HE.net HTML structure could change; re-verify selectors if more than 30 days pass)

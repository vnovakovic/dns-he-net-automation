# Phase 1: Foundation + Browser Core - Context

**Gathered:** 2026-02-27
**Status:** Ready for planning

<domain>
## Phase Boundary

Build the Go service foundation and browser automation engine:
- Go module, project structure, config loading, SQLite schema + goose migrations
- playwright-go (Chromium) session manager with per-account mutex
- HE page objects for login, zone list, and record CRUD (A, AAAA, CNAME, MX, TXT, NS)
- Credential interface backed by env vars (Vault integration is Phase 4)
- Verified working against live dns.he.net with a test account

DNS API surface, JWT tokens, and admin UI are NOT in this phase.

</domain>

<decisions>
## Implementation Decisions

### Browser Automation Library
- **playwright-go** (`github.com/playwright-community/playwright-go`) — NOT go-rod/rod
- Reason: best-in-class automation tooling (Inspector, trace viewer, auto-waits, larger community)
- Node.js runtime in Docker image is acceptable
- Playwright Inspector used during development (`PLAYWRIGHT_HEADLESS=false`)
- `PLAYWRIGHT_SLOW_MO` env var for slow-motion debugging

### Browser Configuration
- `PLAYWRIGHT_HEADLESS=false` env var for dev (headless=false shows browser window)
- `PLAYWRIGHT_SLOW_MO` env var for slow-motion mode during debugging
- Chromium: downloaded via `playwright install` in Dockerfile build step (NOT vendored in git repo — too large)
- Docker layer caches the downloaded Chromium

### Credential Configuration (Phase 1 — env vars, Vault comes in Phase 4)
- Multiple HE accounts supported from day one
- Format: **JSON array** in single env var:
  ```
  HE_ACCOUNTS=[{"id":"prod","username":"vnovakov","password":"..."}]
  ```
- Account ID is used to scope sessions and (later) tokens
- `PORT` env var for HTTP listen port, default 8080
- All config from env vars only (12-factor style, no config file)

### Token / Account Scope
- API token is scoped to one HE account + one role (admin or viewer)
- Token IS the account selector — no need to send account ID separately in API calls
- URL structure: `/api/v1/zones`, `/api/v1/records` (no account ID in path)
- Admin endpoints for service management at `/admin/...`

### Session Concurrency Model
- One Playwright browser context per HE account
- Per-account `sync.Mutex` — all operations for an account are serialized
- This is a **correctness requirement**, not a performance choice
- Concurrent requests queue behind the mutex (second request waits for first)
- Queue with timeout: if request waits longer than `OPERATION_QUEUE_TIMEOUT_SEC` (default 60s) → return 429 Too Many Requests

### Session Failure / Recovery
- Transparent retry — API client never sees session failures
- Service automatically detects stale/crashed sessions and re-logins before next operation
- Overall operation timeout: configurable via `OPERATION_TIMEOUT_SEC` env var, default 30s
- If recovery fails within timeout → return 503 with Retry-After header

### HE.net UI Discovery Method
- **Playwright MCP** used during development for live site inspection and selector verification
- **Playwright Inspector** (`PLAYWRIGHT_HEADLESS=false`) used during Page Object implementation for fine-tuning
- Both approaches used: MCP gives first map, Inspector fine-tunes during coding

### Test Account Strategy
- Production account with dedicated test zone `royalheadshots.online` (zone ID: 1294061)
- Test records created/deleted during integration tests on this zone
- Do NOT use zone deletion in tests — only record-level operations

</decisions>

<specifics>
## dns.he.net HTML Structure (Empirically Verified 2026-02-27)

This is the actual current HTML structure discovered via Playwright MCP on the live site.
**Use these exact selectors in Page Objects — verified against production.**

### Login Page (`https://dns.he.net/`)

```
Form fields:
  input[name="email"]     — username field
  input[name="pass"]      — password field
  button[text="Login!"]   — submit button

Session: standard browser cookie (no CSRF token on login form)
After login: redirects back to https://dns.he.net/ with Account Menu visible
Success check: presence of link[href="/?action=logout"] or "Welcome" text in Account Menu
```

### Zone List Page (`https://dns.he.net/`)

```
Zone table: #domains_table (DataTables-powered, all zones loaded at once)

Per-zone row structure:
  tr.odd / tr.even
    td[0] "go"     — opens zone website in new window (NOT DNS management)
    td[1] "edit"   — img[alt="edit"][name="ZONE_NAME"] onclick navigates to zone records
                     onclick pattern: document.location.href='?hosted_dns_zoneid=ZONE_ID&menu=edit_zone&hosted_dns_editzone'
    td[2] name     — zone name text
    td[3] "delete" — img[alt="delete"][name="ZONE_NAME"][value="ZONE_ID"] onclick="delete_dom(this)"

Extract Zone ID from edit img:
  selector: img[alt="edit"][name="${zoneName}"]
  zone ID:  parse onclick attribute → extract hosted_dns_zoneid value

  OR faster: img[alt="delete"][name="${zoneName}"] → .value attribute = zone ID directly

Navigate to zone records:
  URL: https://dns.he.net/?hosted_dns_zoneid=${zoneID}&menu=edit_zone&hosted_dns_editzone
```

### Add Zone Form

```
Trigger: click link with text "Add a new domain" in sidebar
Panel: div#add_zone becomes visible (modal overlay)

Form: form[name="add_zone"] → POST /index.cgi
  Hidden: action=add_zone, retmain=0
  Field:  input[name="add_domain"]  — domain name
  Submit: input[name="submit"][value="Add Domain!"]
  Cancel: input#btn_cancel
```

### Delete Zone

```
Trigger: img[alt="delete"][value="ZONE_ID"][onclick="delete_dom(this)"] on zone row
delete_dom() fills form[name="remove_domain"] hidden fields and submits:

Form: form[name="remove_domain"] → POST /index.cgi
  Hidden: account (session hash), delete_id=ZONE_ID, remove_domain=1
```

### Zone Records Page (`?hosted_dns_zoneid=ZONE_ID&menu=edit_zone&hosted_dns_editzone`)

```
Record table: plain HTML table (not DataTables)
Columns: Name | Type | TTL | Priority | Data | DDNS | Delete

Record row structure:
  tr[id="RECORD_ID"][class="dns_tr"][onclick="editRow(this)"]
    td[0] hidden  = zone ID
    td[1] hidden  = record ID
    td[2]         = formatted record display text
    td.dns_delete = img[alt="delete"] — delete button

Locked rows (SOA):
  tr.dns_tr_locked[onclick="lockedElement('...')"] — not editable

Record ID extraction:
  tr.id = record ID (e.g., "8299837822")
  delete td onclick: deleteRecord('RECORD_ID', 'ZONE_NAME', 'RECORD_TYPE')
```

### Add/Edit Record Form (SINGLE FORM for ALL types)

```
Trigger (new record): editFormHandler('TYPE') — called by sidebar links
  Standard: 'A', 'AAAA', 'CNAME', 'ALIAS', 'MX', 'NS', 'TXT'
  Additional: 'CAA', 'AFSDB', 'HINFO', 'RP', 'LOC', 'NAPTR', 'PTR', 'SSHFP', 'SPF', 'SRV'
  ALL 17 types use the same form#edit_record, fields show/hide via JS

Trigger (edit record): editRow(rowElement) — called by clicking a record row

Form: form#edit_record → POST to current zone URL (method POST)
  Validates via: onsubmit="return validateEdit(this)"

Hidden fields (always present):
  input[name="account"]                — session hash (set by server, do not touch)
  input[name="menu"]                   — value: "edit_zone"
  input#_type [name="Type"]            — record type: "A", "MX", "SRV", etc.
  input#_zoneid [name="hosted_dns_zoneid"]   — zone ID
  input#_recordid [name="hosted_dns_recordid"] — "" for new, RECORD_ID for edit
  input[name="hosted_dns_editzone"]    — value: "1"

Visible fields by record type (ALL content fields are INPUT type=text, never TEXTAREA):

  STANDARD (Name + Content + TTL):
    A, AAAA, CNAME, ALIAS, NS, CAA, HINFO, RP, LOC, NAPTR, PTR, SSHFP, SPF
    input#_name [name="Name"], input#_content [name="Content"], select#_ttl [name="TTL"]

  WITH DDNS CHECKBOX (Name + Content + TTL + dynamic):
    A, AAAA, TXT, AFSDB
    + input#_dynamic [name="dynamic"] (checkbox, value="1")

  MX (Name + Priority + Content + TTL):
    input#_name, input#_prio [name="Priority"] (visible text), input#_content, select#_ttl

  SRV — SPECIAL (5 fields, no Content):
    input#_name [name="Name"]
    input#_prio [name="Priority"]
    input#_weight [name="Weight"]
    input#_port [name="Port"]
    input#_target [name="Target"]
    select#_ttl [name="TTL"]

TTL select options: 172800 (48h default), 86400 (24h), 43200 (12h), 28800 (8h), 14400 (4h),
                    7200 (2h), 3600 (1h), 1800 (30m), 900 (15m), 300 (5m)

Submit: input[name="hosted_dns_editrecord"][value="Submit"] (type="submit")
Cancel: input#btn_cancel [name="hosted_dns_editrecord_cancel"] (type="button")
```

### Delete Record

```
Trigger: td.dns_delete[onclick="deleteRecord('RECORD_ID','ZONE_NAME','TYPE')"]

deleteRecord() fills form#record_delete hidden fields and submits:
Form: form#record_delete → POST /index.cgi
  Hidden: hosted_dns_zoneid, hosted_dns_recordid=RECORD_ID,
          menu=edit_zone, hosted_dns_delconfirm, hosted_dns_editzone=1
```

### BIND Import

```
Panel: div#add_bind_zone (via "Advanced" tab then a link, or direct trigger)
Form: form[name="add_bind_zone"] → POST /index.cgi
  Hidden: menu=add_bind_zone
  Fields: input[name="domain_name"], textarea[name="raw_zone"]
  Submit: input[name="submit"][value="Add Zone!"]
```

### Session / Auth Notes

- Session is a standard browser cookie (`he-auth` or similar) — playwright-go handles this automatically
- The `account` hidden field in forms = SHA-MD5 hash of account — set server-side in HTML, do NOT hardcode
- No CSRF token separate from the account hash — session cookie + account hash = auth
- Session expiry: **unknown, needs empirical testing** — start with 30-minute proactive re-login threshold
- Rate limiting: **unknown thresholds** — start with 1.5s minimum delay between browser operations

</specifics>

<deferred>
## Deferred Ideas

- Playwright Inspector integration into CI — Phase 4 (Production Hardening)
- BIND export curl command discovery — Phase 6 (Raw Zone panel uses undocumented endpoint)
- BIND export via "Raw Zone" expand panel — Phase 6

</deferred>

---

*Phase: 01-foundation-browser-core*
*Context gathered: 2026-02-27*
*HE.net selectors verified live via Playwright MCP on account: vnovakov*

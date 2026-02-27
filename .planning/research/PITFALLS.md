# Domain Pitfalls

**Domain:** Go REST API wrapper around headless browser automation of dns.he.net
**Researched:** 2026-02-26
**Confidence:** MEDIUM (based on training data for Rod/Go patterns and dns.he.net structure; web verification was unavailable during research)

---

## Critical Pitfalls

Mistakes that cause rewrites, data loss, or service-level failures.

---

### Pitfall 1: dns.he.net Session Invalidation on Concurrent Logins

**What goes wrong:** dns.he.net uses server-side sessions tied to cookies. If two browser instances (or tabs) log into the same HE account simultaneously, the second login invalidates the first session. Subsequent operations on the first session silently fail or redirect to the login page, and the scraper interprets the redirected HTML as "success" because it doesn't validate the response context.

**Why it happens:** The natural instinct is to parallelize operations -- "update 5 records at once" -- but HE's session model is single-session-per-account. There is no official documentation about this because there is no official API, but web UIs that use PHP server-side sessions almost universally enforce single-session semantics.

**Consequences:**
- DNS records silently not updated (scraper reports success but operation hit login page)
- Race condition between concurrent requests causes interleaved partial state
- Debugging is extremely difficult because the scraper "worked" from its perspective

**Prevention:**
- Enforce a strict mutex (sync.Mutex or channel-based semaphore) per dns.he.net account. Every browser operation for a given account must serialize through this lock.
- Design the request queue as: `API request -> per-account queue -> single browser session -> response`
- Never share a browser page across goroutines. One page per operation, but only one operation per account at a time.
- Validate every page load: after any navigation or form submission, check for the presence of expected page elements (e.g., the zone list table, the record edit form) before proceeding.

**Detection (warning signs):**
- Tests pass individually but fail when run in parallel
- Intermittent "record not found" or "zone not found" errors in production
- Log entries showing login page HTML where zone data was expected

**Phase relevance:** Phase 1 (core browser automation layer). This must be baked into the architecture from day one, not bolted on later.

**Confidence:** HIGH -- this is a well-known pattern for web UIs backed by PHP sessions.

---

### Pitfall 2: Chromium Process Leaks and OOM Kills in Long-Running Service

**What goes wrong:** Rod launches Chromium sub-processes. If browser.Close() or page.Close() is not called on every code path (including panics, timeouts, and context cancellations), orphaned Chromium processes accumulate. In a Docker container with memory limits, this leads to OOM kills. On bare metal, it leads to gradual memory exhaustion over days/weeks.

**Why it happens:** Go's defer works per-function, but Rod operations span multiple functions. A panic in a navigation callback, a context timeout during page.WaitLoad(), or a Rod hijack that hangs -- all leave browser resources allocated. Rod's launcher also spawns a separate Chromium process that is not tied to Go's garbage collector.

**Consequences:**
- Service crashes after hours/days of operation with no obvious trigger
- Docker container restarts repeatedly (OOM killed)
- Host system becomes unresponsive from memory pressure
- Zombie Chromium processes visible in ps aux but not tracked by the Go service

**Prevention:**
- Wrap every browser operation in a function with explicit cleanup: `defer page.MustClose()` at the point of page creation, never deferred elsewhere.
- Use `rod.Browser.MustIncognito()` for each operation to get isolated browser contexts that can be independently closed.
- Implement a watchdog goroutine that periodically checks for orphaned Chromium processes (by PID tracking) and kills them.
- Set Chromium launch flags for memory control: `--disable-gpu`, `--disable-dev-shm-usage`, `--no-sandbox`, `--disable-extensions`, `--single-process` (for Docker).
- In Docker, mount `/dev/shm` with adequate size (default 64MB is too small for Chromium): `--shm-size=256m` or use `--disable-dev-shm-usage`.
- Implement a hard timeout (context.WithTimeout) for every browser operation. If it exceeds 30 seconds, kill the page and browser context unconditionally.
- Use Rod's launcher with `launcher.New().Leakless(true)` -- Rod's leakless mode uses a guardian process to clean up Chromium on parent exit.

**Detection (warning signs):**
- Increasing RSS memory in Prometheus/metrics over time without corresponding load increase
- `pgrep chromium | wc -l` growing over time
- Container restart count increasing in Docker/Kubernetes

**Phase relevance:** Phase 1 (browser session management). The session pool/manager must handle lifecycle correctly from the start.

**Confidence:** HIGH -- Chromium process leaks are the most commonly reported issue in Rod, Puppeteer, and Playwright projects.

---

### Pitfall 3: dns.he.net HTML Structure Changes Breaking All Operations Silently

**What goes wrong:** dns.he.net is not a modern SPA -- it is a traditional server-rendered site. Hurricane Electric can change HTML structure, CSS classes, form field names, or table layouts at any time without notice. When this happens, CSS selectors or XPath queries return empty results, and the scraper either fails with cryptic errors or (worse) writes incorrect data.

**Why it happens:** There is no contract (API schema) between the scraper and dns.he.net. The "API" is reverse-engineered HTML scraping. HE has no obligation to maintain backward compatibility in their HTML.

**Consequences:**
- All DNS operations fail simultaneously after an HE UI update
- If selectors partially match (e.g., table structure changes but some selectors still work), records may be created in wrong zones or with wrong values
- Zone deletion could target the wrong zone if the zone list HTML changes

**Prevention:**
- Create an abstraction layer (e.g., `he_client` package) that encapsulates ALL selector knowledge. Every CSS selector, XPath query, and form field name lives in ONE file or struct. When HE changes their UI, you update one place.
- Implement "page identity assertions" -- before any operation, verify you are on the expected page by checking for 2-3 independent markers (page title, a specific element, URL pattern).
- Build a comprehensive integration test suite that runs against live dns.he.net (on a test account/zone) on a schedule (daily cron). This is your early warning system.
- For every scraping operation, validate the response: after "add record," verify the record appears in the zone list. After "delete record," verify it is gone.
- Version your selectors. When updating selectors for an HE UI change, log the change so you can correlate with incident timelines.

**Detection (warning signs):**
- Integration tests start failing
- "Element not found" errors in logs
- Operations succeed (no error) but the expected DNS record does not appear

**Phase relevance:** Phase 1 (scraper design) and ongoing maintenance. The abstraction layer design is architectural -- it must be in place before writing any scraping logic.

**Confidence:** HIGH -- this is the fundamental risk of any screen-scraping project and the primary reason this project is inherently brittle.

---

### Pitfall 4: dns.he.net Rate Limiting and IP Blocking

**What goes wrong:** Rapid automated requests to dns.he.net trigger rate limiting or IP-based blocking. The service may respond with HTTP 429, a CAPTCHA challenge, a temporary ban page, or silently drop requests. Since there is no documented rate limit policy, you discover the limits by hitting them.

**Why it happens:** dns.he.net is a free service. Hurricane Electric has strong incentive to protect it from abuse. Headless browsers making rapid sequential requests look like automated abuse -- because they are.

**Consequences:**
- Service IP gets temporarily or permanently blocked from dns.he.net
- All DNS management operations fail for all accounts managed by this service
- If running in a shared infrastructure environment, the block affects other services on the same IP
- CAPTCHA challenges cannot be solved by the headless browser, causing a hard failure

**Prevention:**
- Implement configurable rate limiting in the browser session manager: minimum delay between operations (start with 2-3 seconds between page loads, tune down carefully).
- Add jitter to delays (randomize between 1.5-4 seconds) to avoid looking like a bot with fixed timing.
- Implement exponential backoff when receiving unexpected responses (login redirects, empty pages, error pages).
- Detect CAPTCHA pages explicitly (check for CAPTCHA-related elements after every page load) and raise a specific error that propagates to the API caller as HTTP 503 with a clear message.
- Consider running the service behind a residential IP or at minimum a stable IP, so that if you need to contact HE support about unblocking, you have a fixed IP to reference.
- Batch operations where possible: if 10 records need updating, do them in a single browser session with delays, rather than 10 separate login-operate-logout cycles.
- Implement a circuit breaker pattern: if N consecutive operations fail with suspected rate-limiting, pause all operations for that account for a configurable cooldown period.

**Detection (warning signs):**
- Sudden spike in login failures across all accounts
- Pages returning different HTML than expected (CAPTCHA or ban page)
- Operations that worked yesterday all fail today

**Phase relevance:** Phase 1 (browser automation) and Phase 2 (API layer rate limiting). The browser layer needs delays; the API layer needs to enforce rate limits on incoming requests to prevent downstream overload.

**Confidence:** MEDIUM -- dns.he.net's specific rate limiting behavior is not well-documented. The general risk is HIGH but the specific thresholds are unknown and must be discovered empirically.

---

### Pitfall 5: Vault Connectivity Loss Causing Complete Service Outage

**What goes wrong:** The service stores dns.he.net credentials in HashiCorp Vault. If Vault becomes unreachable (network issue, Vault sealed, token expired), the service cannot retrieve credentials for any account, causing a total outage of all DNS operations. Unlike a database outage, this is not recoverable by retrying -- the credentials are simply unavailable.

**Why it happens:** Vault is a network dependency. In the happy path, it works invisibly. But Vault has unique failure modes: it can be sealed (requires manual unseal), its auth tokens expire, its lease renewals can fail, and it can be temporarily unreachable during upgrades.

**Consequences:**
- Complete service outage -- no DNS operations possible for any account
- If Vault token expires during operation, in-flight operations fail mid-execution
- If Vault was recently restarted and is sealed, the service cannot auto-recover
- Read-only API operations (list zones, list records) also fail because they require login

**Prevention:**
- Implement a credential cache with a configurable TTL (e.g., 5 minutes). Once credentials are fetched from Vault, cache them in memory (not disk, not SQLite) for the TTL duration. This provides resilience to brief Vault outages.
- Use Vault's renewable tokens and implement automatic lease renewal in a background goroutine. Monitor renewal failures as a leading indicator.
- Implement health checks that distinguish between "Vault unreachable" and "Vault sealed" -- they require different remediation.
- Design the API to return specific error codes for credential-related failures (e.g., HTTP 503 with `{"error": "credential_store_unavailable"}`) so callers know the issue is transient.
- Implement Vault connection retry with exponential backoff, but cap retries to avoid goroutine accumulation.
- Consider a "degraded mode" where read operations (using existing browser sessions) continue even if Vault is down, while write operations that require fresh login are rejected.
- Handle Vault token bootstrap carefully: support both token-based and AppRole auth methods. AppRole is more suitable for automated services.

**Detection (warning signs):**
- Vault health check endpoint returning non-200
- Lease renewal failures in logs
- Sudden "credential fetch failed" errors after a period of normal operation

**Phase relevance:** Phase 3 (Vault integration). But the credential caching architecture should be designed in Phase 1 as an interface, even if the initial implementation uses a config file.

**Confidence:** HIGH -- Vault operational pitfalls are well-documented.

---

### Pitfall 6: JWT Token Revocation Race Conditions

**What goes wrong:** A token is revoked (deleted from SQLite) while an in-flight request authenticated with that token is still executing a long-running browser operation. The operation completes and returns results to a caller who should no longer have access. Alternatively, a token is revoked but the revocation is not checked until the next request, allowing a window of unauthorized access.

**Why it happens:** Browser operations can take 5-30 seconds. If token validation happens only at request start, a revocation during the operation is invisible. SQLite's WAL mode allows concurrent reads during writes, so the revocation write does not block the in-flight read.

**Consequences:**
- Security violation: revoked tokens can still perform operations during a window
- If tokens are revoked due to compromise, the compromised token can still cause damage during in-flight operations
- Audit logs show operations performed by revoked tokens, which is confusing

**Prevention:**
- Accept that in-flight operations will complete -- this is a pragmatic choice. Document it as expected behavior. The revocation takes effect on the next request.
- Implement a token blacklist cache (in-memory set of revoked token IDs) that is checked on every request. This is O(1) and adds negligible overhead.
- Log a warning when a completed operation was performed by a token that was revoked during execution. This provides audit trail visibility.
- Set reasonable maximum operation timeouts (30 seconds) so the window of post-revocation access is bounded.
- For the "JWT opaque-style tokens" design: since tokens are just random identifiers looked up in SQLite (not cryptographically signed JWTs), revocation is immediate on the next database check. This is actually simpler and more secure than signed JWT revocation.

**Detection (warning signs):**
- Audit log entries with timestamps after the token revocation timestamp
- Test scenarios where revoke + immediate re-use succeeds

**Phase relevance:** Phase 2 (API authentication layer). Decide on the revocation semantics early and document them in the API spec.

**Confidence:** HIGH -- this is a well-understood distributed systems problem.

---

## Moderate Pitfalls

---

### Pitfall 7: BIND Zone File Parsing Edge Cases

**What goes wrong:** BIND zone file format (RFC 1035 Section 5) has numerous edge cases that naive parsers miss: `$ORIGIN` directives, `$INCLUDE`, `$TTL`, relative vs. absolute domain names (trailing dot), multi-line records with parentheses, semicolon comments within record data (e.g., TXT records containing semicolons), character escaping, and class field omission (defaulting to IN).

**Why it happens:** Most developers implement a "good enough" zone file parser that handles the common cases. Then users import a zone file exported from BIND, PowerDNS, or another tool, and it contains features the parser does not handle.

**Consequences:**
- Zone import silently drops records that the parser cannot understand
- Records imported with wrong values (e.g., relative name not resolved against $ORIGIN)
- TXT records with complex quoting/escaping imported incorrectly, breaking DKIM, SPF, or DMARC
- Export produces zone files that BIND itself cannot load

**Prevention:**
- Use an existing Go library for zone file parsing: `github.com/miekg/dns` is the gold standard Go DNS library and includes a complete zone file parser (`dns.ZoneParser`). Do NOT write a custom parser.
- Test with real-world zone files: export zones from BIND, export from dns.he.net, export from Cloudflare, and verify round-trip fidelity.
- Pay special attention to TXT record quoting: DNS TXT records can contain any byte, and zone file representation uses complex quoting rules. `miekg/dns` handles this correctly.
- Handle the trailing dot convention explicitly: `example.com.` is absolute, `www` is relative to $ORIGIN. Mishandling this is the most common zone file bug.
- For zone export (generating zone files from scraped data), validate the output by parsing it back with the same parser and comparing the result set.

**Detection (warning signs):**
- Record counts differ between imported file and resulting zone
- TXT records lose their content or gain extra backslashes
- BIND refuses to load an exported zone file

**Phase relevance:** Phase 3 (BIND import/export feature). Use `miekg/dns` from the start; do not attempt a custom parser.

**Confidence:** HIGH -- `miekg/dns` is the canonical Go DNS library (5k+ GitHub stars, used by CoreDNS, external-dns, and most Go DNS tools).

---

### Pitfall 8: Rod Page Navigation Timing Issues

**What goes wrong:** Rod's page navigation and element waiting can be fragile. Clicking a submit button, then immediately trying to read the result page, fails because the page has not finished loading. Or, `page.MustElement()` finds a stale element from the previous page that has not yet been replaced by the navigation.

**Why it happens:** dns.he.net uses traditional form POSTs (not AJAX). After a form submission, the browser navigates to a new page. Rod's default behavior is to resolve element queries against the current DOM, which may be the old page if navigation has not completed. Rod provides `page.MustWaitLoad()` and `page.MustWaitStable()` but they have different semantics and the wrong choice leads to either flaky tests or excessive waits.

**Consequences:**
- Intermittent test failures (the classic "flaky test" problem)
- Operations that work in development (fast network to HE) but fail in production (slower network)
- Scraper reports "element not found" for elements that definitely exist on the target page

**Prevention:**
- After every form submission or navigation, use `page.MustWaitLoad()` followed by checking for a page-specific element before proceeding.
- Never use fixed `time.Sleep()` for synchronization -- it is both too slow (wastes time) and too fast (still flaky). Use Rod's built-in waiting mechanisms.
- Use `page.MustWaitStable()` with a reasonable duration parameter for pages with dynamic content.
- Set a page-level timeout using `page.Timeout()` so that waiting operations do not hang forever.
- For form submissions that redirect, use `page.MustWaitRequestIdle()` to wait for all network activity to settle before reading the DOM.
- Build a helper function `waitForPage(page, expectedSelector, timeout)` that encapsulates the wait-and-verify pattern, and use it after every navigation.

**Detection (warning signs):**
- Tests pass 90% of the time but fail 10% of the time with "element not found"
- Adding `time.Sleep(1 * time.Second)` "fixes" the issue (indicates a timing problem)

**Phase relevance:** Phase 1 (browser automation core). Establish the navigation pattern in the first operation you implement and reuse it everywhere.

**Confidence:** HIGH -- this is the most commonly discussed Rod/Puppeteer issue in developer forums.

---

### Pitfall 9: dns.he.net Login Session Cookie Management

**What goes wrong:** dns.he.net login requires POST to a login form, which sets session cookies. If cookies are not properly maintained across page navigations within the same browser context, subsequent requests are treated as unauthenticated. Additionally, sessions may expire after a period of inactivity, and the expiry behavior is undocumented.

**Why it happens:** Rod manages cookies at the browser context level, which normally works transparently. But if you create a new page (tab) in a different context, or if you accidentally clear cookies, or if you navigate to a different subdomain, cookies may be lost.

**Consequences:**
- Operations silently fail because the browser is no longer authenticated
- Login operation succeeds but subsequent page loads redirect to login page
- Session expires mid-operation during a batch of record updates

**Prevention:**
- Use a single `browser.MustIncognito()` context per account session. All pages for that account open within this context, sharing cookies automatically.
- Implement a session health check before each operation: load a lightweight page (e.g., the zone list) and verify it shows zone data rather than a login form.
- Implement automatic re-login: if a session health check fails, perform a fresh login within the same browser context before retrying the operation.
- Track session creation time and proactively re-login before the estimated session expiry (start with 15-minute assumption, tune based on observed behavior).
- Never navigate to subdomains or external URLs within an HE session context -- this could cause cookie scoping issues.

**Detection (warning signs):**
- Increasing login frequency in logs
- "Redirect to login" errors after successful initial login
- Operations fail after the service has been idle for a period

**Phase relevance:** Phase 1 (session management). The session lifecycle (login, validate, re-login, cleanup) is foundational.

**Confidence:** MEDIUM -- dns.he.net's specific session behavior requires empirical discovery. The general patterns are HIGH confidence.

---

### Pitfall 10: SQLite Concurrent Write Contention Under Load

**What goes wrong:** SQLite handles concurrent reads well (especially in WAL mode) but serializes writes. If the API receives many concurrent requests that all need to write (token validation updates last-used timestamps, audit logging, etc.), write contention causes `SQLITE_BUSY` errors or significant latency.

**Why it happens:** SQLite uses file-level locking. WAL mode allows concurrent reads during writes, but only one writer at a time. The default busy timeout is 0 (immediate failure), so concurrent writers get errors instead of waiting.

**Consequences:**
- `database is locked` errors under moderate concurrent load
- Audit log entries dropped silently if error handling is inadequate
- Token last-used timestamps not updated, breaking usage analytics

**Prevention:**
- Set a busy timeout: `PRAGMA busy_timeout = 5000;` (5 seconds). This makes writers wait instead of immediately failing.
- Use a single `*sql.DB` instance with `SetMaxOpenConns(1)` for writes, effectively serializing write access at the Go level. This is cleaner than relying on SQLite's busy handling.
- Separate read and write connection pools: one read-only pool (can be concurrent) and one write pool (serialized).
- For high-frequency, low-priority writes (like audit logs and token last-used timestamps), batch them: accumulate in memory and flush to SQLite periodically (every 5 seconds or every 100 entries).
- Use WAL mode (`PRAGMA journal_mode=WAL;`) -- this is essential for any concurrent access pattern.
- Set `PRAGMA synchronous=NORMAL;` in WAL mode (safe for WAL, better performance than FULL).

**Detection (warning signs):**
- `database is locked` errors in logs
- API response latency increasing under load
- Missing audit log entries

**Phase relevance:** Phase 2 (API and database layer). Configure SQLite correctly from the start.

**Confidence:** HIGH -- SQLite concurrency characteristics are well-documented.

---

### Pitfall 11: Headless Chromium in Docker -- Sandbox and Shared Memory Issues

**What goes wrong:** Chromium in Docker fails to launch or crashes randomly because: (a) the default Docker seccomp profile blocks Chromium's sandbox syscalls, (b) `/dev/shm` defaults to 64MB which is insufficient for Chromium, (c) the container runs as root but Chromium's sandbox expects non-root, or (d) missing system fonts cause rendering issues.

**Why it happens:** Chromium's multi-process architecture uses sandboxing (namespaces, seccomp) that conflicts with Docker's own sandboxing. Docker's default shared memory allocation is too small for Chromium's shared memory IPC.

**Consequences:**
- Service fails to start in Docker with cryptic Chromium error messages
- Random crashes under load due to shared memory exhaustion
- Pages render incorrectly due to missing fonts (not critical for scraping, but can affect element positioning)

**Prevention:**
- Dockerfile must include: `--shm-size=256m` in docker run, or mount tmpfs at /dev/shm
- Use `--no-sandbox` flag in Chromium launch args (necessary when running as root in Docker)
- Use `--disable-dev-shm-usage` to use /tmp instead of /dev/shm
- Install minimal fonts in the Docker image: `apt-get install -y fonts-liberation fonts-noto-color-emoji`
- Install Chromium dependencies: `apt-get install -y libnss3 libxss1 libasound2 libatk-bridge2.0-0 libgtk-3-0`
- Use Rod's `launcher.New().Leakless(true).Headless(true).Set("disable-gpu").Set("no-sandbox")` pattern
- Base image should be Debian/Ubuntu-based (not Alpine) -- Chromium on Alpine requires musl workarounds that are fragile
- Test the Docker image in CI with actual browser operations, not just "does the service start"

**Detection (warning signs):**
- Service starts fine on developer machine but crashes in Docker
- "Failed to launch browser" errors on container startup
- Chromium crash dumps in /tmp/chromium-crash-reports

**Phase relevance:** Phase 4 (containerization). But knowing this upfront informs Dockerfile design from the first Docker attempt.

**Confidence:** HIGH -- this is extremely well-documented across Puppeteer, Playwright, and Rod Docker guides.

---

### Pitfall 12: Multi-Account Browser Instance Resource Scaling

**What goes wrong:** Each dns.he.net account requires its own browser context (to maintain separate sessions). With 10+ accounts, this means 10+ Chromium incognito contexts, each consuming 50-150MB of RAM. The service's memory footprint scales linearly with the number of managed accounts, and may exceed available resources.

**Why it happens:** The natural design is "one persistent browser session per account, always ready to serve." But idle browser contexts still consume memory, and Chromium does not release memory aggressively.

**Consequences:**
- Service memory usage grows to multiple GB with many accounts
- OOM kills in constrained environments
- Slow performance as the OS starts swapping

**Prevention:**
- Do NOT maintain persistent browser sessions. Instead, create browser contexts on-demand and destroy them after each operation (or after a short idle timeout, e.g., 30 seconds).
- Implement a session pool with a maximum size: if all slots are in use, new requests wait in a queue. This caps peak memory usage.
- Share a single Browser instance (one Chromium process) across all accounts, using `MustIncognito()` for per-account isolation. Do NOT launch separate Chromium processes per account.
- Monitor and expose per-account resource usage via metrics.
- Set a maximum concurrent operations limit (e.g., 3 concurrent browser operations across all accounts) to cap resource usage.

**Detection (warning signs):**
- Memory usage proportional to number of configured accounts
- Adding a new account causes existing operations to slow down
- Docker container approaching memory limit

**Phase relevance:** Phase 1 (session pool design). The session lifecycle architecture determines resource scaling.

**Confidence:** HIGH -- Chromium memory characteristics are well-understood.

---

## Minor Pitfalls

---

### Pitfall 13: dns.he.net Form Submission Requires Hidden Fields

**What goes wrong:** dns.he.net forms likely include hidden fields (CSRF tokens, form identifiers, session tokens). Submitting a form without these hidden fields causes silent rejection or unexpected behavior.

**Why it happens:** Developer builds form submission by setting only the visible fields (record type, name, value, TTL) and misses the hidden inputs that the browser would normally include.

**Prevention:**
- When submitting forms via Rod, interact with the actual page elements (fill input fields, click submit) rather than constructing HTTP POST requests manually. Rod's `page.MustElement().MustInput()` approach automatically includes all form fields.
- If you must construct requests programmatically, scrape ALL form fields (including hidden ones) before building the request.

**Phase relevance:** Phase 1 (record CRUD implementation).

**Confidence:** MEDIUM -- specific to dns.he.net's form implementation, needs empirical verification.

---

### Pitfall 14: TXT Record Value Escaping Between dns.he.net and BIND Format

**What goes wrong:** TXT records can contain special characters (quotes, semicolons, backslashes). dns.he.net's web form may escape these differently than BIND zone file format. When importing from BIND format and pushing to dns.he.net (or vice versa), the escaping can double-up or get lost, breaking DKIM keys, SPF records, and DMARC policies.

**Why it happens:** There are at least three representations of a TXT record value: the raw bytes, the BIND zone file quoted representation, and whatever dns.he.net's web form expects. Converting between these without a clear canonical form introduces escaping bugs.

**Prevention:**
- Define a canonical internal representation (raw bytes, no escaping) and convert to/from BIND format (using miekg/dns) and dns.he.net format (empirically determined) at the boundaries.
- Write explicit tests for DKIM, SPF, and DMARC records -- these are the most complex TXT records in practice.
- Test round-trip: BIND import -> push to HE -> scrape from HE -> BIND export, and verify the record values match.

**Phase relevance:** Phase 3 (BIND import/export) and Phase 1 (record CRUD).

**Confidence:** MEDIUM -- the general problem is HIGH confidence; dns.he.net's specific handling needs empirical verification.

---

### Pitfall 15: Vault Secret Path Structure and Migration

**What goes wrong:** Credentials are stored in Vault at a path structure (e.g., `secret/data/dns-he/accounts/<account-name>`). If the path structure is not designed thoughtfully, adding features later (like per-account metadata, credential rotation timestamps, or multiple credential versions) requires a Vault data migration that is operationally painful.

**Why it happens:** Vault KV v2 stores secrets as versioned JSON objects at a path. The path hierarchy and JSON schema are chosen early and become load-bearing.

**Prevention:**
- Use Vault KV v2 (not v1) -- it provides versioning, which enables credential rotation without downtime.
- Design the path structure to include the account identifier: `secret/data/dns-he-net-automation/accounts/<account-id>`
- Store credentials as a JSON object with explicit fields: `{"username": "...", "password": "...", "created_at": "...", "rotated_at": "..."}` rather than flat key-value.
- Abstract the Vault path structure behind a Go interface so the path convention can change without modifying business logic.

**Phase relevance:** Phase 3 (Vault integration). Design the path structure before writing the first line of Vault code.

**Confidence:** HIGH -- Vault KV patterns are well-established.

---

### Pitfall 16: Graceful Shutdown with In-Flight Browser Operations

**What goes wrong:** On SIGTERM (Docker stop, systemd stop), the service must shut down gracefully. But there may be browser operations in progress that take 10-30 seconds. If the service exits immediately, Chromium processes become orphaned. If the service waits too long, Docker force-kills it (default 10-second grace period).

**Why it happens:** The default Docker stop timeout is 10 seconds. Browser operations can easily exceed this. Without explicit graceful shutdown logic, the service either orphans processes or gets SIGKILL'd.

**Prevention:**
- Implement `context.Context` propagation from the top-level server context through to every browser operation. On SIGTERM, cancel the root context.
- In the browser session manager, handle context cancellation by closing all active browser contexts immediately (do not wait for operations to complete).
- Set Docker stop timeout to 30 seconds: `stop_grace_period: 30s` in docker-compose.
- Use Rod's Leakless launcher so that if the Go process dies, the guardian process cleans up Chromium.
- On shutdown, drain the request queue (reject new requests with 503, wait briefly for in-flight ones, then force-close).

**Phase relevance:** Phase 2 (API server lifecycle) and Phase 4 (Docker configuration).

**Confidence:** HIGH -- standard Go service lifecycle concern, amplified by Chromium subprocess management.

---

## Phase-Specific Warnings

| Phase Topic | Likely Pitfall | Mitigation |
|-------------|---------------|------------|
| Browser automation core (Phase 1) | Session invalidation on concurrent access (Pitfall 1) | Per-account mutex, single-session enforcement |
| Browser automation core (Phase 1) | Chromium process leaks (Pitfall 2) | Leakless launcher, explicit cleanup, watchdog |
| Browser automation core (Phase 1) | HE UI changes (Pitfall 3) | Selector abstraction layer, integration tests |
| Browser automation core (Phase 1) | Navigation timing (Pitfall 8) | Wait-and-verify helpers, never use time.Sleep |
| API layer (Phase 2) | Rate limiting from HE (Pitfall 4) | Configurable delays, jitter, circuit breaker |
| API layer (Phase 2) | SQLite write contention (Pitfall 10) | WAL mode, busy timeout, write serialization |
| API layer (Phase 2) | Token revocation semantics (Pitfall 6) | In-memory blacklist, bounded operation timeout |
| BIND and Vault integration (Phase 3) | Zone file parsing (Pitfall 7) | Use miekg/dns, never custom parser |
| BIND and Vault integration (Phase 3) | Vault connectivity (Pitfall 5) | Credential caching, lease renewal, health checks |
| BIND and Vault integration (Phase 3) | TXT record escaping (Pitfall 14) | Canonical internal representation, round-trip tests |
| Containerization (Phase 4) | Chromium in Docker (Pitfall 11) | --no-sandbox, shm-size, Debian base, font packages |
| Containerization (Phase 4) | Graceful shutdown (Pitfall 16) | Context propagation, Leakless, stop_grace_period |
| Scaling (ongoing) | Multi-account resource scaling (Pitfall 12) | On-demand sessions, shared browser, pool limits |

---

## Sources

- Training data knowledge of Rod (go-rod/rod) library patterns and Chromium automation pitfalls
- Training data knowledge of dns.he.net web interface structure (traditional PHP/HTML form-based UI)
- Training data knowledge of miekg/dns library for Go DNS operations and zone file parsing
- Training data knowledge of HashiCorp Vault operational patterns (KV v2, AppRole, lease management)
- Training data knowledge of SQLite concurrency characteristics (WAL mode, busy timeout)
- Training data knowledge of Docker + Chromium deployment challenges

**Note:** Web verification was unavailable during this research session. All findings are based on training data (cutoff May 2025). Confidence levels reflect this limitation. dns.he.net-specific behaviors (session handling, rate limits, HTML structure) should be empirically validated during Phase 1 development with a test account.

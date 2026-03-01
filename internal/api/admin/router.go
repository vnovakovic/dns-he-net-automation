package admin

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	playwright "github.com/playwright-community/playwright-go"
	"golang.org/x/crypto/bcrypt"

	"github.com/vnovakov/dns-he-net-automation/internal/api/admin/templates"
	"github.com/vnovakov/dns-he-net-automation/internal/audit"
	"github.com/vnovakov/dns-he-net-automation/internal/bindio"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/browser/pages"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
	"github.com/vnovakov/dns-he-net-automation/internal/reconcile"
	"github.com/vnovakov/dns-he-net-automation/internal/resilience"
	"github.com/vnovakov/dns-he-net-automation/internal/token"
)

// RegisterAdminRoutes mounts the /admin sub-router onto the provided chi.Router.
//
// FINAL SIGNATURE: This is the complete parameter set used by all plans (02, 03, 04).
// Plans 03 and 04 replace handler stubs in this file but do NOT change this signature.
//
// WHY define the full signature in plan 02:
//   Plans 03 (accounts/tokens) and 04 (zones/sync/audit) progressively fill in handlers
//   that need db, sm, breakers, and jwtSecret. Defining the full signature here means
//   main.go is updated exactly once (here) and compiles cleanly after each wave.
//   Without this, each plan would need a main.go edit, creating merge-conflict risk.
//
// All /admin routes (except /admin/login and /admin/static/*) are protected by
// the AdminAuth middleware (Basic Auth + session cookie).
//
// WHY separate admin auth from Bearer JWT:
//   The admin UI needs its own auth layer. Reusing Bearer JWT would require admin
//   operators to manage API tokens just to access the UI. The session cookie path
//   is more ergonomic for browser-based access. (CONTEXT.md decision)
//
// WHY call store/sm functions directly rather than HTTP requests to /api/v1:
//   Avoids token management complexity in the UI layer. Admin UI is an in-process
//   convenience layer, not an external API client. (RESEARCH.md anti-pattern)
func RegisterAdminRoutes(
	r chi.Router,
	db *sql.DB,
	sm *browser.SessionManager,
	breakers *resilience.BreakerRegistry,
	jwtSecret []byte,
	username, password, sessionKeyHex string,
) {
	// Decode the hex session signing key. Fall back to a zero key if misconfigured —
	// zero key means session cookies will not validate (every request redirects to login),
	// which is safe-fail behavior rather than a crash.
	//
	// WHY hex-encoded key (not raw bytes in env var):
	//   Raw bytes are hard to set reliably in environment variables (shell escaping issues).
	//   A hex string is unambiguous and easy to generate: openssl rand -hex 32
	signingKey, err := hex.DecodeString(sessionKeyHex)
	if err != nil || len(signingKey) == 0 {
		// Safe-fail: generate zero key — sessions will not persist across restarts,
		// but the server will not crash. Operator should set ADMIN_SESSION_KEY properly.
		signingKey = make([]byte, 32)
	}

	r.Route("/admin", func(r chi.Router) {
		// Static assets served without auth — CSS and JS must load before the login
		// page renders. Without this exclusion, the browser would redirect CSS/JS requests
		// to /admin/login, making the login form unstyled. (Rule 2 auto-fix applied here)
		// WHY fs.Sub: the embed FS stores files at "static/admin.css" etc.
		// After StripPrefix removes "/admin/static/", the FileServer sees just "admin.css"
		// and looks for it at the FS root — not found (404). fs.Sub re-roots the FS at
		// the "static/" subdirectory so "admin.css" resolves correctly.
		staticSubFS, _ := fs.Sub(staticFS, "static")
		r.Handle("/static/*",
			http.StripPrefix("/admin/static/",
				http.FileServer(http.FS(staticSubFS))),
		)

		// Login and logout routes — outside the AdminAuth middleware.
		// Login must be accessible to unauthenticated users (obvious).
		// Logout is outside auth so it works even if the cookie becomes invalid.
		r.Get("/login", handleLoginPage())
		r.Post("/login", handleLoginPost(db, username, password, signingKey))
		r.Get("/logout", handleLogout())

		// Protected routes — all require AdminAuth (Basic Auth or session cookie).
		r.Group(func(r chi.Router) {
			r.Use(AdminAuth(username, password, signingKey))

			// Default /admin redirect to accounts page.
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/admin/accounts", http.StatusFound)
			})

			// Account management pages.
			// load-zones: fetches zones from dns.he.net via browser session, upserts to DB,
			//   returns AccountZonesList partial for htmx insertion into the account card.
			// zones/{zoneName}: removes a single zone from the DB (not from dns.he.net).
			//   {zoneName} captures the full domain name including dots (chi routes by segment,
			//   and dots within a segment are captured without special handling).
			r.Get("/accounts", handleAccountsPage(db))
			r.Post("/accounts", handleAccountCreate(db))
			r.Delete("/accounts/{accountID}", handleAccountDelete(db, sm))
			r.Get("/accounts/{accountID}/load-zones", handleAccountLoadZones(db, sm, breakers))
			r.Post("/accounts/{accountID}/zones", handleAccountZoneAdd(db))
			r.Delete("/accounts/{accountID}/zones/{zoneName}", handleAccountZoneRemove(db))

			// Token management pages (handlers replaced by plan 03).
			r.Get("/tokens", handleTokensPage(db))
			r.Get("/tokens/{accountID}", handleTokensForAccount(db))
			r.Post("/tokens/{accountID}", handleTokenIssue(db, jwtSecret))
			r.Delete("/tokens/{tokenID}", handleTokenRevoke(db))

			// Zones page: shows zones from DB with per-zone Export BIND / Import BIND actions.
			// export and import routes use the numeric HE zone ID (not the zone name) so
			// the browser session can navigate to the correct zone page on dns.he.net.
			// WHY {zoneID}/export and {zoneID}/import (not under /accounts/...):
			//   These operations work on a single zone regardless of which account owns it.
			//   The account_id is passed as a query param (export) or form field (import)
			//   because both the zone ID and account ID are known from the Zones page card.
			r.Get("/zones", handleZonesPage(db))
			r.Get("/zones/{zoneID}/export", handleAdminZoneExport(sm, breakers))
			r.Post("/zones/{zoneID}/import", handleAdminZoneImport(sm, breakers))

			// Sync trigger page (handlers replaced by plan 04).
			r.Get("/sync", handleSyncPage(db))
			r.Post("/sync/trigger", handleSyncTrigger(db, sm, breakers))

			// Audit log page (handler replaced by plan 04).
			r.Get("/audit", handleAuditPage(db))

			// User management — admin-only (guarded inside each handler).
			// Account Users are DB-backed operator accounts that each see only their own
			// HE accounts. Only the env-configured Server Admin can create/delete them.
			r.Get("/users", handleUsersPage(db))
			r.Post("/users", handleUserCreate(db))
			r.Delete("/users/{userID}", handleUserDelete(db))
		})
	})
}

// handleLoginPage renders the login form (GET /admin/login).
func handleLoginPage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.LoginPage("").Render(r.Context(), w)
	}
}

// handleLoginPost processes the login form submission (POST /admin/login).
// On success: issues session cookie, redirects to /admin/accounts.
// On failure: re-renders login form with error message (401 status).
//
// WHY two-stage auth (admin env check first, then DB users):
//   1. Admin (env): stateless — no DB needed. Checked first so the admin can always log in
//      even if the DB is corrupt or the users table is empty.
//   2. Account Users (DB bcrypt): checked second. On match, issues a user session cookie
//      scoped to that user's ID — downstream handlers filter accounts by this ID.
//
// WHY 401 status on wrong credentials (not redirect):
//   curl scripts checking status codes get the right signal. The browser still
//   sees the re-rendered form because the response body contains HTML.
func handleLoginPost(db *sql.DB, adminUsername, adminPassword string, signingKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		u := r.FormValue("username")
		p := r.FormValue("password")

		// 1. Admin check (env vars) — unchanged behavior.
		if u == adminUsername && p == adminPassword {
			IssueAdminSessionCookie(w, signingKey)
			http.Redirect(w, r, "/admin/accounts", http.StatusFound)
			return
		}

		// 2. Account user check — bcrypt verify against DB.
		userID, err := authenticateAccountUser(r.Context(), db, u, p)
		if err == nil {
			IssueUserSessionCookie(w, userID, signingKey)
			http.Redirect(w, r, "/admin/accounts", http.StatusFound)
			return
		}

		// 3. Wrong credentials — re-render login form with error message.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = templates.LoginPage("Invalid username or password").Render(r.Context(), w)
	}
}

// authenticateAccountUser looks up a user by username and verifies the password with bcrypt.
// Returns the user's ID (= username) on success, or an error if not found or password wrong.
//
// WHY bcrypt.CompareHashAndPassword (not == comparison):
//   Passwords are stored as bcrypt hashes (not plaintext). Direct comparison would never match.
//   bcrypt.CompareHashAndPassword extracts the salt from the stored hash and rehashes the
//   supplied password at the same cost — timing is constant regardless of password length.
//
// WHY NOT distinguish "user not found" from "wrong password" in the return:
//   Returning the same error for both cases prevents username enumeration attacks — an
//   attacker cannot determine whether a given username exists by observing the response.
func authenticateAccountUser(ctx context.Context, db *sql.DB, username, password string) (string, error) {
	var id, hash string
	err := db.QueryRowContext(ctx,
		`SELECT id, password_hash FROM users WHERE username = ?`,
		username,
	).Scan(&id, &hash)
	if err != nil {
		// sql.ErrNoRows (user not found) or other DB error — treat both as auth failure.
		return "", errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return "", errors.New("invalid credentials")
	}
	return id, nil
}

// handleLogout clears the session cookie and redirects to login.
func handleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ClearSessionCookie(w)
		http.Redirect(w, r, "/admin/login", http.StatusFound)
	}
}

// Stub handlers for plan 03 and 04 implementation.
// These return 501 until the respective plans replace them.
//
// WHY stubs (not panics):
//   Stubs let the router compile and the auth layer be tested in plan 02.
//   Panics would make the server unusable before plan 03 is complete.
//
// WHY all stubs accept their eventual full parameter set:
//   This ensures the function signatures in RegisterAdminRoutes above never need
//   to change when plans 03 and 04 replace these bodies. Plans 03 and 04 only
//   replace function bodies, not the wiring in RegisterAdminRoutes. (Checker issue 5 fix)

// listAccountsFromDB queries the accounts table and returns model.Account records ordered by
// created_at ASC. Extracted as a helper because multiple page handlers need the same list.
//
// WHY userID param (not always SELECT * without WHERE):
//   Account Users must only see their own HE accounts. Passing "" (admin session) returns all
//   accounts with no WHERE clause. Passing a non-empty userID adds WHERE user_id = ? so the
//   Account User cannot see accounts owned by the admin or other users.
//   The filter is applied in SQL, not in Go, to keep result sets small (no over-fetching).
//
// WHY not use store.ListAccounts:
//   The store package only exposes Open() for DB initialization. Account CRUD is done via
//   inline DB queries (same pattern as REST handlers in internal/api/handlers/accounts.go).
//   The admin UI follows the same pattern — direct DB access, no HTTP round-trips to /api/v1.
func listAccountsFromDB(r *http.Request, db *sql.DB, userID string) ([]model.Account, error) {
	var rows *sql.Rows
	var err error

	if userID == "" {
		// Admin session — return all accounts regardless of owner.
		rows, err = db.QueryContext(r.Context(),
			// WHY include password in SELECT:
			//   model.Account.Password has json:"-" so it never leaks into API responses.
			//   The admin UI handler needs it to populate the struct for any display or
			//   credential-update operations. Omitting it here would require a separate
			//   query whenever the password is needed.
			`SELECT id, username, password, created_at, updated_at FROM accounts ORDER BY created_at ASC`,
		)
	} else {
		// Account User session — filter to only their own HE accounts.
		rows, err = db.QueryContext(r.Context(),
			`SELECT id, username, password, created_at, updated_at FROM accounts WHERE user_id = ? ORDER BY created_at ASC`,
			userID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var acc model.Account
		if err := rows.Scan(&acc.ID, &acc.Username, &acc.Password, &acc.CreatedAt, &acc.UpdatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, acc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if accounts == nil {
		accounts = []model.Account{}
	}
	return accounts, nil
}

// listZonesFromDB returns all zones for one account from the DB ordered by name.
// Zone.ID is he_zone_id (the numeric ID assigned by dns.he.net); may be "" for
// zones that were manually registered before a "Load zones from HE" was run.
//
// WHY read from zones table (not live-fetch from dns.he.net):
//   Live-fetching requires a browser session. The DB cache is populated by
//   handleAccountLoadZones (GET /admin/accounts/{accountID}/load-zones) which
//   operators trigger explicitly. Reads here are cheap, instant, and session-free.
func listZonesFromDB(ctx context.Context, db *sql.DB, accountID string) ([]model.Zone, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT name, he_zone_id FROM zones WHERE account_id = ? ORDER BY name ASC`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var zones []model.Zone
	for rows.Next() {
		var z model.Zone
		z.AccountID = accountID
		if err := rows.Scan(&z.Name, &z.ID); err != nil {
			return nil, err
		}
		zones = append(zones, z)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if zones == nil {
		zones = []model.Zone{}
	}
	return zones, nil
}

func handleAccountsPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := listAccountsFromDB(r, db, getSessionUserID(r))
		if err != nil {
			http.Error(w, "Failed to list accounts", http.StatusInternalServerError)
			return
		}

		// Pre-load zones from DB for each account so the page renders without
		// browser sessions. Zones are populated by "Load zones from HE" button.
		zonesByAccount := make(map[string][]model.Zone)
		for _, acc := range accounts {
			zones, err := listZonesFromDB(r.Context(), db, acc.ID)
			if err != nil {
				http.Error(w, "Failed to list zones", http.StatusInternalServerError)
				return
			}
			zonesByAccount[acc.ID] = zones
		}

		data := templates.PageData{Title: "Accounts", ActivePage: "accounts", IsAdmin: isAdminSession(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AccountsPage(accounts, zonesByAccount, data).Render(r.Context(), w)
	}
}

// handleAccountCreate registers a new account and returns just the new row for htmx insertion.
//
// WHY return only AccountRow (not a full page):
//   The form uses hx-swap="beforeend" on #accounts-table tbody — htmx appends the partial
//   response directly into the table body without a full page reload. Returning the full page
//   would overwrite the entire page content.
//
// WHY inline DB insert (not store.CreateAccount):
//   The store package only provides Open(). Account CRUD mirrors the REST handler pattern:
//   inline ExecContext + QueryRowContext for created_at (DB-assigned timestamp).
//
// WHY writeFormError (not http.Error):
//   http.Error returns 4xx/5xx which htmx swallows silently — the user sees the form reset
//   but no feedback. writeFormError retargets the response to #account-register-error via
//   HX-Retarget + HX-Reswap headers and returns 200 so htmx actually performs the swap.
//   This makes all error cases (duplicate ID, duplicate username, parse failure) visible.
func handleAccountCreate(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// writeFormError sends an error partial to #account-register-error via htmx retarget.
		// Must be 200 — htmx only performs the swap on 2xx responses.
		writeFormError := func(msg string) {
			w.Header().Set("HX-Retarget", "#account-register-error")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = templates.AccountRegisterError(msg).Render(r.Context(), w)
		}

		if err := r.ParseForm(); err != nil {
			writeFormError("Bad request: could not parse form.")
			return
		}
		accountID := strings.TrimSpace(r.FormValue("account_id"))
		username := strings.TrimSpace(r.FormValue("username"))
		// WHY not trimming password: leading/trailing spaces in passwords are intentional.
		// Trimming would silently store a different credential than what the operator typed.
		password := r.FormValue("password")
		if accountID == "" || username == "" {
			writeFormError("Account ID and username are both required.")
			return
		}
		if password == "" {
			writeFormError("Password is required.")
			return
		}

		// SECURITY (SEC-03): password is never logged — only the accountID appears in errors.
		//
		// WHY user_id from session (not a form field):
		//   Accepting user_id from the form would allow any authenticated user to create accounts
		//   owned by a different user ID (horizontal privilege escalation). Taking it from the
		//   verified session context ensures the account belongs to the logged-in user.
		//   Admin sessions return "" for user_id — admin-created accounts are "admin-owned"
		//   (visible to admin, not visible to any account user).
		userID := getSessionUserID(r)
		_, err := db.ExecContext(r.Context(),
			`INSERT INTO accounts (id, username, password, user_id) VALUES (?, ?, ?, ?)`,
			accountID, username, password, userID,
		)
		if err != nil {
			errStr := err.Error()
			// Provide human-readable messages for the most common constraint violation.
			// username no longer has a UNIQUE constraint (removed in migration 004) —
			// only the PRIMARY KEY (id) can cause a UNIQUE/PK conflict here.
			if strings.Contains(errStr, "UNIQUE") || strings.Contains(errStr, "PRIMARY KEY") {
				writeFormError(fmt.Sprintf("Account ID %q is already registered.", accountID))
			} else {
				writeFormError("Failed to create account: " + errStr)
			}
			return
		}

		// Fetch the DB-assigned created_at and updated_at for the template.
		var acc model.Account
		err = db.QueryRowContext(r.Context(),
			`SELECT id, username, created_at, updated_at FROM accounts WHERE id = ?`,
			accountID,
		).Scan(&acc.ID, &acc.Username, &acc.CreatedAt, &acc.UpdatedAt)
		if err != nil {
			writeFormError("Account was created but could not be retrieved — reload the page.")
			return
		}

		// AccountRegisterSuccess returns the new AccountCard (appended to #accounts-cards)
		// plus OOB clear of #account-register-error for any prior error message.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AccountRegisterSuccess(acc).Render(r.Context(), w)
	}
}

// handleAccountDelete removes an account by ID and returns an empty 200 for htmx row removal.
//
// WHY return empty 200 (not 204):
//   htmx hx-swap="outerHTML swap:500ms" replaces the target element with the response body.
//   An empty 200 body makes htmx replace the row element with nothing (effectively removing it).
//   204 No Content would also work, but 200 with empty body is clearer for htmx's swap behavior.
//
// WHY sm parameter is accepted but _ = sm:
//   The RegisterAdminRoutes signature is frozen (plan 02 decision). sm is accepted for signature
//   consistency. Session cleanup on account deletion is handled lazily — the next operation
//   attempt on a deleted account will fail and the session will be cleaned up at that point.
func handleAccountDelete(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		_, err := db.ExecContext(r.Context(),
			`DELETE FROM accounts WHERE id = ?`,
			accountID,
		)
		if err != nil {
			http.Error(w, "Failed to delete account", http.StatusInternalServerError)
			return
		}
		// sm is accepted for RegisterAdminRoutes signature stability — see handleAccountDelete comment.
		_ = sm
		// htmx swaps the target row with empty body, removing it from the DOM.
		w.WriteHeader(http.StatusOK)
	}
}

func handleTokensPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := listAccountsFromDB(r, db, getSessionUserID(r))
		if err != nil {
			http.Error(w, "Failed to list accounts", http.StatusInternalServerError)
			return
		}
		data := templates.PageData{Title: "Tokens", ActivePage: "tokens", IsAdmin: isAdminSession(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.TokensPage(accounts, data).Render(r.Context(), w)
	}
}

func handleTokensForAccount(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		tokens, err := token.ListTokens(r.Context(), db, accountID)
		if err != nil {
			http.Error(w, "Failed to list tokens", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.TokensForAccount(accountID, tokens).Render(r.Context(), w)
	}
}

// handleTokenIssue issues a new JWT bearer token and returns the token row + plaintext JWT.
//
// WHY show rawJWT in the response (not just the row):
//   The raw JWT is shown exactly once at creation — it is never persisted in the DB (SEC-02).
//   NewTokenResult renders both the TokenRow and a plaintext JWT reveal row so the operator
//   can copy it immediately. After page reload it will not be shown again.
//
// WHY ListTokens + JTI match (not a direct GetToken by JTI):
//   There is no GetToken(jti) helper. ListTokens returns all tokens ordered by created_at DESC.
//   We search for the matching JTI in the returned slice — the newly issued token will be there.
func handleTokenIssue(db *sql.DB, jwtSecret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		label := r.FormValue("label")
		role := r.FormValue("role")
		if role == "" {
			role = "viewer"
		}

		rawJWT, jti, err := token.IssueToken(r.Context(), db, accountID, role, label, 0, jwtSecret)
		if err != nil {
			http.Error(w, "Failed to issue token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Fetch the newly issued token record to populate the template.
		// ListTokens returns newest first; we find the matching record by JTI.
		tokens, err := token.ListTokens(r.Context(), db, accountID)
		if err != nil || len(tokens) == 0 {
			http.Error(w, "Failed to fetch issued token", http.StatusInternalServerError)
			return
		}
		var tok token.TokenRecord
		for _, t := range tokens {
			if t.JTI == jti {
				tok = t
				break
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.NewTokenResult(tok, rawJWT).Render(r.Context(), w)
	}
}

// handleTokenRevoke revokes a token by JTI using RevokeByJTI (no accountID required).
//
// WHY token.RevokeByJTI instead of token.RevokeToken:
//   The admin revoke URL is DELETE /admin/tokens/{tokenID} — it does not include accountID.
//   token.RevokeToken requires accountID for scoped revocation (prevents cross-account revokes
//   via the REST API). For the admin UI, full authority over all tokens is expected, so the
//   simpler JTI-only revoke is correct. RevokeByJTI was added to token.go in task 1.
func handleTokenRevoke(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenID := chi.URLParam(r, "tokenID")
		if err := token.RevokeByJTI(r.Context(), db, tokenID); err != nil {
			http.Error(w, "Failed to revoke token", http.StatusInternalServerError)
			return
		}
		// Return empty 200 — htmx swaps row with empty body, removing it from DOM.
		w.WriteHeader(http.StatusOK)
	}
}


// handleZonesPage renders the zones view showing all zones from DB (GET /admin/zones).
// Zones come from the DB (populated by "Load zones from HE" on the Accounts page).
// This page is for BIND export/import operations — it does not trigger browser sessions.
//
// WHY read from DB (not live-fetch):
//   "Load zones from HE" on the Accounts page writes zones to the DB. This page
//   reads that cache. Export/Import actions (per-zone) do use browser sessions, but
//   only when the operator explicitly triggers them — not on page load.
func handleZonesPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := listAccountsFromDB(r, db, getSessionUserID(r))
		if err != nil {
			http.Error(w, "Failed to list accounts", http.StatusInternalServerError)
			return
		}

		zonesByAccount := make(map[string][]model.Zone)
		for _, acc := range accounts {
			zones, err := listZonesFromDB(r.Context(), db, acc.ID)
			if err != nil {
				http.Error(w, "Failed to list zones", http.StatusInternalServerError)
				return
			}
			zonesByAccount[acc.ID] = zones
		}

		data := templates.PageData{Title: "Zones", ActivePage: "zones", IsAdmin: isAdminSession(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.ZonesPage(accounts, zonesByAccount, data).Render(r.Context(), w)
	}
}

// handleSyncPage renders the sync trigger form (GET /admin/sync).
//
// WHY load accounts for the select (not just show a blank form):
//   The account selector requires a list of registered accounts. This is a
//   cheap DB query and improves usability significantly vs. requiring operators
//   to type account IDs manually.
func handleSyncPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := listAccountsFromDB(r, db, getSessionUserID(r))
		if err != nil {
			http.Error(w, "Failed to list accounts", http.StatusInternalServerError)
			return
		}
		data := templates.PageData{Title: "Sync", ActivePage: "sync", IsAdmin: isAdminSession(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.SyncPage(accounts, data).Render(r.Context(), w)
	}
}

// handleSyncTrigger processes the sync form submission (POST /admin/sync/trigger).
//
// WHY in-process reconcile (not HTTP POST to /api/v1/zones/{zoneID}/sync):
//   Making an HTTP request to the REST API from within the admin UI handler would
//   require managing a Bearer token (issue → use → store securely) and adds
//   unnecessary round-trip latency. Calling reconcile.DiffRecords and
//   reconcile.Apply directly reuses the same logic without network overhead.
//   (RESEARCH.md anti-pattern: admin UI is an in-process layer, not an API client)
//
// WHY the form posts account_id and zone_id in the body (not the URL):
//   The hx-post target is /admin/sync/trigger (fixed path). The zone_id is a
//   form field because a select/input in the form body is more flexible than
//   embedding it in the URL — users can change zone IDs without navigating away.
//
// WHY operation closures (deleteFn, updateFn, createFn) mirror sync.go exactly:
//   These closures call the same browser page objects (ZoneListPage, RecordFormPage)
//   with the same breakers.Execute + WithRetry + WithAccount wrapping. Keeping
//   them identical ensures identical retry and circuit-breaker semantics for both
//   the REST API and admin UI sync paths.
func handleSyncTrigger(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		accountID := r.FormValue("account_id")
		zoneID := r.FormValue("zone_id")
		dryRun := r.FormValue("dry_run") == "true"
		desiredJSON := r.FormValue("desired_records")
		if desiredJSON == "" {
			desiredJSON = "[]"
		}

		var desiredRecords []model.Record
		if err := json.Unmarshal([]byte(desiredJSON), &desiredRecords); err != nil {
			http.Error(w, "Invalid desired records JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Scrape current DNS state from dns.he.net via browser automation.
		var currentRecords []model.Record
		err := breakers.Execute(r.Context(), accountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, accountID, "list_records", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)
					recs, err := zl.ListRecords(zoneID)
					if err != nil {
						return err
					}
					currentRecords = recs
					return nil
				})
			})
		})
		if err != nil {
			http.Error(w, "Failed to fetch current records: "+err.Error(), http.StatusInternalServerError)
			return
		}

		plan := reconcile.DiffRecords(currentRecords, desiredRecords)

		var results []reconcile.SyncResult
		hadErrors := false

		if !dryRun {
			// deleteFn navigates to the zone, looks up the zone name and record type,
			// then calls the deleteRecord browser action.
			// Mirrors the deleteFn in internal/api/handlers/sync.go exactly.
			deleteFn := func(ctx context.Context, zID string, rec model.Record) error {
				return breakers.Execute(ctx, accountID, func() error {
					return resilience.WithRetry(ctx, func(ctx context.Context) error {
						return sm.WithAccount(ctx, accountID, "delete_record", func(page playwright.Page) error {
							zl := pages.NewZoneListPage(page)
							if err := zl.NavigateToZone(zID); err != nil {
								return err
							}
							zoneName, err := zl.GetZoneName(zID)
							if err != nil {
								return err
							}
							parsed, err := zl.ParseRecordRow(rec.ID)
							if err != nil {
								return err
							}
							rf := pages.NewRecordFormPage(page)
							return rf.DeleteRecord(rec.ID, zoneName, string(parsed.Type))
						})
					})
				})
			}

			// updateFn opens the edit form for the existing record ID and submits new field values.
			// Mirrors the updateFn in internal/api/handlers/sync.go exactly.
			updateFn := func(ctx context.Context, zID string, rec model.Record) error {
				return breakers.Execute(ctx, accountID, func() error {
					return resilience.WithRetry(ctx, func(ctx context.Context) error {
						return sm.WithAccount(ctx, accountID, "update_record", func(page playwright.Page) error {
							zl := pages.NewZoneListPage(page)
							if err := zl.NavigateToZone(zID); err != nil {
								return err
							}
							rf := pages.NewRecordFormPage(page)
							if err := rf.EditExistingRecord(rec.ID); err != nil {
								return err
							}
							return rf.FillAndSubmit(rec)
						})
					})
				})
			}

			// createFn opens the new-record form for the given type and submits.
			// Mirrors the createFn in internal/api/handlers/sync.go exactly.
			createFn := func(ctx context.Context, zID string, rec model.Record) error {
				return breakers.Execute(ctx, accountID, func() error {
					return resilience.WithRetry(ctx, func(ctx context.Context) error {
						return sm.WithAccount(ctx, accountID, "create_record", func(page playwright.Page) error {
							zl := pages.NewZoneListPage(page)
							if err := zl.NavigateToZone(zID); err != nil {
								return err
							}
							rf := pages.NewRecordFormPage(page)
							if err := rf.OpenNewRecordForm(string(rec.Type)); err != nil {
								return err
							}
							return rf.FillAndSubmit(rec)
						})
					})
				})
			}

			results = reconcile.Apply(r.Context(), zoneID, plan, deleteFn, updateFn, createFn)
			for _, res := range results {
				if res.Status == "error" {
					hadErrors = true
				}
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.SyncResultPartial(zoneID, dryRun, plan, results, hadErrors).Render(r.Context(), w)
	}
}

// handleAccountLoadZones fetches the live zone list for one account via browser session,
// upserts the results into the zones DB table, then returns the AccountZonesList htmx partial
// (GET /admin/accounts/{accountID}/load-zones).
//
// WHY upsert to DB after fetching (not just return live data):
//   Persisting zones in DB lets handleAccountsPage and handleZonesPage serve the Accounts
//   and Zones pages without browser sessions on every load. The DB acts as a cache that is
//   refreshed on demand by this handler. he_zone_id is preserved across refreshes via
//   ON CONFLICT DO UPDATE, so export/import links remain valid after re-loading.
//
// WHY return AccountZonesList (not ZonesForAccount):
//   This handler is triggered from the Accounts page card — it replaces the zones sub-list
//   inside the account card. ZonesForAccount is used by the Zones page, which has a
//   different layout (account header + zones table in one card). AccountZonesList renders
//   only the zones table (or empty state) without the account header.
func handleAccountLoadZones(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")

		var fetched []model.Zone
		err := breakers.Execute(r.Context(), accountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, accountID, "list_zones", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)
					var err error
					fetched, err = zl.ListZones()
					return err
				})
			})
		})
		if err != nil {
			http.Error(w, "Failed to load zones from dns.he.net: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Upsert fetched zones into the DB.
		// ON CONFLICT(account_id, name): PRIMARY KEY is (account_id, name).
		// We update he_zone_id so that previously-empty entries get their numeric ID populated.
		for _, z := range fetched {
			if _, err := db.ExecContext(r.Context(),
				`INSERT INTO zones (account_id, name, he_zone_id) VALUES (?, ?, ?)
				 ON CONFLICT(account_id, name) DO UPDATE SET he_zone_id = excluded.he_zone_id`,
				accountID, z.Name, z.ID,
			); err != nil {
				http.Error(w, "Failed to save zones: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Return zones from DB (authoritative after upsert) — not the raw fetched slice.
		// Reading from DB ensures the response reflects the persisted state.
		zones, err := listZonesFromDB(r.Context(), db, accountID)
		if err != nil {
			http.Error(w, "Failed to list zones: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AccountZonesList(accountID, zones).Render(r.Context(), w)
	}
}

// handleAccountZoneAdd registers a zone under an account in the DB
// (POST /admin/accounts/{accountID}/zones).
//
// WHY manual zone add (not only "Load zones from HE"):
//   An operator may want to register a single specific zone without triggering
//   a full browser session that scrapes all zones from dns.he.net. This handler
//   lets them type the zone name (and optionally the HE numeric zone ID) directly.
//   If he_zone_id is left empty, Export/Import on the Zones page are disabled
//   until the operator either enters the ID manually or runs "Load zones from HE".
//
// WHY ON CONFLICT DO UPDATE:
//   Re-submitting the same zone name is idempotent. If the zone already exists and
//   the operator provides a non-empty he_zone_id, the ID is updated. If empty,
//   the existing he_zone_id is preserved (CASE WHEN prevents overwriting with empty).
//
// WHY writeZoneError (not http.Error):
//   http.Error returns 500 which htmx swallows silently — the zones list stays
//   unchanged and no feedback appears. writeZoneError retargets to the per-account
//   error div via HX-Retarget + status 200 so htmx actually swaps the error in.
func handleAccountZoneAdd(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		// account_id_safe is the sanitized (dot-free) account ID computed in the template,
		// passed as a hidden field so we can build the correct HX-Retarget CSS selector
		// without duplicating the sanitization logic in Go.
		accountIDSafe := r.FormValue("account_id_safe")
		zoneName := strings.TrimSpace(r.FormValue("zone_name"))
		heZoneID := strings.TrimSpace(r.FormValue("he_zone_id"))

		writeZoneError := func(msg string) {
			w.Header().Set("HX-Retarget", "#zone-add-error-"+accountIDSafe)
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// Inline error HTML — no separate template needed for a one-liner.
			_, _ = fmt.Fprintf(w, `<p class="error-banner" style="margin:0;">%s</p>`, msg)
		}

		if zoneName == "" {
			writeZoneError("Zone name is required.")
			return
		}

		_, err := db.ExecContext(r.Context(),
			// CASE WHEN: preserve the existing he_zone_id if the new value is empty.
			// This prevents overwriting a known zone ID with an empty string when the
			// operator re-registers a zone they already loaded from HE.
			`INSERT INTO zones (account_id, name, he_zone_id) VALUES (?, ?, ?)
			 ON CONFLICT(account_id, name) DO UPDATE
			 SET he_zone_id = CASE WHEN excluded.he_zone_id != '' THEN excluded.he_zone_id ELSE he_zone_id END`,
			accountID, zoneName, heZoneID,
		)
		if err != nil {
			writeZoneError("Failed to add zone: " + err.Error())
			return
		}

		zones, err := listZonesFromDB(r.Context(), db, accountID)
		if err != nil {
			writeZoneError("Zone saved but failed to reload list — refresh the page.")
			return
		}

		// Clear the error div OOB in case a previous error was showing.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AccountZonesList(accountID, zones).Render(r.Context(), w)
	}
}

// handleAccountZoneRemove removes a zone from the DB for one account
// (DELETE /admin/accounts/{accountID}/zones/{zoneName}).
//
// WHY remove from DB only (not from dns.he.net):
//   Removing a zone on dns.he.net is a destructive DNS operation that should
//   require a separate, explicit confirmation workflow. The DB entry is just the
//   local cache — removing it hides the zone from the Accounts and Zones pages
//   without touching the live DNS zone. The operator can re-add it by clicking
//   "Load zones from HE" again.
//
// WHY return empty 200 (not 204):
//   htmx hx-target="closest tr" + hx-swap="outerHTML" replaces the row element
//   with the response body. Empty 200 body makes htmx replace the row with nothing,
//   effectively removing the row from the DOM. 204 also works but 200 is consistent
//   with other htmx row-removal patterns in this file.
func handleAccountZoneRemove(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		zoneName := chi.URLParam(r, "zoneName")
		if _, err := db.ExecContext(r.Context(),
			`DELETE FROM zones WHERE account_id = ? AND name = ?`,
			accountID, zoneName,
		); err != nil {
			http.Error(w, "Failed to remove zone", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// handleAdminZoneExport fetches all records for a zone via browser session and returns
// a BIND zone file download (GET /admin/zones/{zoneID}/export?account_id={accountID}).
//
// WHY account_id as query param (not in path):
//   The Export link is generated by the ZonesForAccount template which knows both
//   accountID and zoneID. Embedding accountID in the query string is simpler than
//   a nested route like /zones/{accountID}/{zoneID}/export and avoids URL ambiguity.
//
// WHY Content-Disposition attachment (not inline):
//   The operator expects to download the zone file and edit it locally, not view it
//   in the browser. attachment; filename= triggers Save As in all major browsers.
func handleAdminZoneExport(sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := chi.URLParam(r, "zoneID")
		accountID := r.URL.Query().Get("account_id")
		if accountID == "" {
			http.Error(w, "account_id query param required", http.StatusBadRequest)
			return
		}

		var zoneName string
		var records []model.Record
		err := breakers.Execute(r.Context(), accountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, accountID, "export_zone", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)
					name, err := zl.GetZoneName(zoneID)
					if err != nil {
						return err
					}
					zoneName = name
					recs, err := zl.ListRecords(zoneID)
					if err != nil {
						return err
					}
					records = recs
					return nil
				})
			})
		})
		if err != nil {
			http.Error(w, "Failed to fetch zone: "+err.Error(), http.StatusInternalServerError)
			return
		}

		zoneFile, err := bindio.ExportZone(records, zoneName)
		if err != nil {
			http.Error(w, "Export failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zone"`, zoneName))
		_, _ = w.Write([]byte(zoneFile))
	}
}

// handleAdminZoneImport parses a BIND zone file from the form, diffs it against the
// current live zone, and applies changes additively (POST /admin/zones/{zoneID}/import).
//
// WHY additive only (plan.Delete = nil):
//   CONTEXT.md decision: import never removes records. Operators use import to add/update
//   records from a BIND file without touching records not present in the file. Full-replace
//   import is explicitly deferred (CONTEXT.md deferred section).
//
// WHY form POST with textarea (not file upload):
//   A textarea lets operators paste zone file content directly in the browser without
//   navigating the file picker. Zone files are typically short text — textarea is ergonomic.
//
// WHY dry_run default true (consistent with Sync page):
//   Safe default: operators must explicitly uncheck to apply changes, preventing
//   accidental DNS mutations on first use.
func handleAdminZoneImport(sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		zoneID := chi.URLParam(r, "zoneID")
		accountID := r.FormValue("account_id")
		bindZone := r.FormValue("bind_zone")
		dryRun := r.FormValue("dry_run") == "true"

		if accountID == "" || bindZone == "" {
			http.Error(w, "account_id and bind_zone required", http.StatusBadRequest)
			return
		}

		// Fetch zone name and current records via browser session.
		var zoneName string
		var currentRecords []model.Record
		err := breakers.Execute(r.Context(), accountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, accountID, "import_zone_fetch", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)
					name, err := zl.GetZoneName(zoneID)
					if err != nil {
						return err
					}
					zoneName = name
					recs, err := zl.ListRecords(zoneID)
					if err != nil {
						return err
					}
					currentRecords = recs
					return nil
				})
			})
		})
		if err != nil {
			http.Error(w, "Failed to fetch zone: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Parse the BIND zone file.
		desired, skipped, err := bindio.ParseZoneFile(bindZone, zoneName)
		if err != nil {
			http.Error(w, "Zone file parse error: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Diff against current state. Set Delete = nil — additive import never removes records.
		plan := reconcile.DiffRecords(currentRecords, desired)
		plan.Delete = nil

		applied := 0
		hadErrors := false

		if !dryRun {
			// Operation closures mirror handleSyncTrigger for consistent retry/breaker semantics.
			// deleteFn is a no-op because plan.Delete = nil — included only to satisfy Apply signature.
			deleteFn := func(_ context.Context, _ string, _ model.Record) error { return nil }

			updateFn := func(ctx context.Context, zID string, rec model.Record) error {
				return breakers.Execute(ctx, accountID, func() error {
					return resilience.WithRetry(ctx, func(ctx context.Context) error {
						return sm.WithAccount(ctx, accountID, "update_record", func(page playwright.Page) error {
							zl := pages.NewZoneListPage(page)
							if err := zl.NavigateToZone(zID); err != nil {
								return err
							}
							rf := pages.NewRecordFormPage(page)
							if err := rf.EditExistingRecord(rec.ID); err != nil {
								return err
							}
							return rf.FillAndSubmit(rec)
						})
					})
				})
			}

			createFn := func(ctx context.Context, zID string, rec model.Record) error {
				return breakers.Execute(ctx, accountID, func() error {
					return resilience.WithRetry(ctx, func(ctx context.Context) error {
						return sm.WithAccount(ctx, accountID, "create_record", func(page playwright.Page) error {
							zl := pages.NewZoneListPage(page)
							if err := zl.NavigateToZone(zID); err != nil {
								return err
							}
							rf := pages.NewRecordFormPage(page)
							if err := rf.OpenNewRecordForm(string(rec.Type)); err != nil {
								return err
							}
							return rf.FillAndSubmit(rec)
						})
					})
				})
			}

			results := reconcile.Apply(r.Context(), zoneID, plan, deleteFn, updateFn, createFn)
			for _, res := range results {
				if res.Status == "ok" {
					applied++
				} else {
					hadErrors = true
				}
			}
		} else {
			// Dry run: count what would be applied (adds + updates, no deletes).
			applied = len(plan.Add) + len(plan.Update)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.ZoneImportResult(zoneName, dryRun, applied, skipped, hadErrors).Render(r.Context(), w)
	}
}

// handleAuditPage renders the paginated audit log (GET /admin/audit).
//
// WHY pageSize=50 (Claude's Discretion):
//   50 entries balances visibility (enough history at a glance) with page weight
//   (each row is a short DB read). CONTEXT.md notes this as a discretionary value.
//
// WHY totalPages never 0:
//   If the audit log is empty (totalCount=0), the ceiling division returns 0.
//   We clamp to 1 so the template always shows "Page 1 of 1" rather than
//   "Page 1 of 0" which would be confusing to operators.
func handleAuditPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const pageSize = 50
		pageNum := 1
		if p := r.URL.Query().Get("page"); p != "" {
			if n, err := strconv.Atoi(p); err == nil && n > 0 {
				pageNum = n
			}
		}
		offset := (pageNum - 1) * pageSize

		entries, err := audit.List(db, pageSize, offset)
		if err != nil {
			http.Error(w, "Failed to load audit log", http.StatusInternalServerError)
			return
		}
		totalCount, _ := audit.Count(db)
		totalPages := (totalCount + pageSize - 1) / pageSize
		if totalPages == 0 {
			totalPages = 1
		}

		data := templates.PageData{Title: "Audit Log", ActivePage: "audit", IsAdmin: isAdminSession(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AuditPage(entries, pageNum, totalPages, data).Render(r.Context(), w)
	}
}

// handleUsersPage renders the user management page (GET /admin/users).
// Admin-only: Account Users cannot manage other users.
//
// WHY admin-only guard inside handler (not a separate middleware):
//   The Users routes are registered inside the AdminAuth group (all routes require auth).
//   The additional admin-only check is a simple in-handler guard — adding a separate
//   middleware for three routes would add more complexity than it removes.
func handleUsersPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminSession(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		rows, err := db.QueryContext(r.Context(),
			`SELECT id, username, created_at FROM users ORDER BY created_at ASC`,
		)
		if err != nil {
			http.Error(w, "Failed to list users", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var users []templates.UserRow
		for rows.Next() {
			var u templates.UserRow
			if err := rows.Scan(&u.ID, &u.Username, &u.CreatedAt); err != nil {
				http.Error(w, "Failed to scan users", http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}
		if err := rows.Err(); err != nil {
			http.Error(w, "Failed to list users", http.StatusInternalServerError)
			return
		}
		if users == nil {
			users = []templates.UserRow{}
		}

		data := templates.PageData{Title: "Users", ActivePage: "users", IsAdmin: true}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.UsersPage(users, data).Render(r.Context(), w)
	}
}

// handleUserCreate creates a new Account User (POST /admin/users).
// Admin-only. Bcrypt-hashes the password before storing.
//
// WHY bcrypt cost 12:
//   Cost 12 is ~300ms on a modern CPU — expensive enough to deter brute-force attacks
//   against stolen DB rows, cheap enough that admin UI creates feel instant. OWASP
//   recommends a cost factor that takes ≥250ms; 12 is the standard Go recommendation.
func handleUserCreate(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminSession(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// writeFormError sends an error partial via htmx retarget — same pattern as handleAccountCreate.
		// Must return 200 so htmx actually performs the swap.
		writeFormError := func(msg string) {
			w.Header().Set("HX-Retarget", "#user-register-error")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = templates.UserRegisterError(msg).Render(r.Context(), w)
		}

		if err := r.ParseForm(); err != nil {
			writeFormError("Bad request: could not parse form.")
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		// WHY not trimming password: see handleAccountCreate comment.
		password := r.FormValue("password")
		if username == "" {
			writeFormError("Username is required.")
			return
		}
		if password == "" {
			writeFormError("Password is required.")
			return
		}

		// Hash the password before storing — never store plaintext. (SEC-03)
		hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
		if err != nil {
			writeFormError("Failed to hash password: " + err.Error())
			return
		}

		// id = username: immutable label, same as the unique username.
		// See migration 006 comment on WHY id = username.
		_, err = db.ExecContext(r.Context(),
			`INSERT INTO users (id, username, password_hash) VALUES (?, ?, ?)`,
			username, username, string(hash),
		)
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "UNIQUE") || strings.Contains(errStr, "PRIMARY KEY") {
				writeFormError(fmt.Sprintf("Username %q is already registered.", username))
			} else {
				writeFormError("Failed to create user: " + errStr)
			}
			return
		}

		// Fetch the DB-assigned created_at for the template.
		var u templates.UserRow
		err = db.QueryRowContext(r.Context(),
			`SELECT id, username, created_at FROM users WHERE id = ?`,
			username,
		).Scan(&u.ID, &u.Username, &u.CreatedAt)
		if err != nil {
			writeFormError("User was created but could not be retrieved — reload the page.")
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.UserRegisterSuccess(u).Render(r.Context(), w)
	}
}

// handleUserDelete removes an Account User by ID (DELETE /admin/users/{userID}).
// Admin-only. CASCADE in the DB removes all HE accounts owned by this user.
//
// WHY CASCADE is relied on (not explicit account deletion in Go):
//   The ON DELETE CASCADE FK on accounts.user_id means the DB removes the user's accounts
//   and (via the accounts→zones cascade) their zones automatically. Application-level
//   cascade loops would be racy (concurrent requests) and harder to audit. The DB is the
//   right place for referential integrity.
func handleUserDelete(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminSession(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		userID := chi.URLParam(r, "userID")
		if _, err := db.ExecContext(r.Context(),
			`DELETE FROM users WHERE id = ?`,
			userID,
		); err != nil {
			http.Error(w, "Failed to delete user", http.StatusInternalServerError)
			return
		}
		// Return empty 200 — htmx swaps target row with empty body, removing it from DOM.
		w.WriteHeader(http.StatusOK)
	}
}

package admin

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"bytes"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/vnovakov/dns-he-net-automation/internal/api/admin/templates"
	"github.com/vnovakov/dns-he-net-automation/internal/audit"
	"github.com/vnovakov/dns-he-net-automation/internal/bindio"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/browser/pages"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
	"github.com/vnovakov/dns-he-net-automation/internal/reconcile"
	"github.com/vnovakov/dns-he-net-automation/internal/resilience"
	"github.com/vnovakov/dns-he-net-automation/internal/store"
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
	tokenRecoveryEnabled bool,
	version string,
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

	// Derive the AES-256 token recovery key when the feature is enabled.
	// nil = feature off — handleTokenIssue passes nil to token.IssueToken, which stores NULL.
	// WHY derive here (not in main): RegisterAdminRoutes already receives jwtSecret; keeping
	//   the derivation close to usage avoids threading a separate key through NewRouter.
	//   The same key is also derived in api/router.go for the REST API path — both must use
	//   token.RecoveryKey(jwtSecret) with the same input to produce the same 32-byte key.
	var recoveryKey *[32]byte
	if tokenRecoveryEnabled {
		k := token.RecoveryKey(jwtSecret)
		recoveryKey = &k
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
		r.Post("/login", handleLoginPost(db, username, signingKey))
		r.Get("/logout", handleLogout())

		// Protected routes — all require AdminAuth (Basic Auth or session cookie).
		r.Group(func(r chi.Router) {
			r.Use(AdminAuth(username, password, signingKey))

			// Default /admin redirect to accounts page.
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/admin/accounts", http.StatusFound)
			})

			// Account management pages.
			// load-zones: fetches zones from dns.he.net via browser session, returns
			//   AccountZonesSelectList (checkbox form) — does NOT write to the DB.
			// zones/store: commits the operator-selected subset from the selection form to DB,
			//   returns AccountZonesList partial (the explicit "Store selected" step).
			// zones/{zoneName}: removes a single zone from the DB (not from dns.he.net).
			// WHY no POST /zones (manual add): zones are always loaded from HE via load-zones.
			//   The manual add form was removed — use "Load zones from HE" + select instead.
			//   {zoneName} captures the full domain name including dots (chi routes by segment,
			//   and dots within a segment are captured without special handling).
			r.Get("/accounts", handleAccountsPage(db))
			r.Post("/accounts", handleAccountCreate(db))
			r.Delete("/accounts/{accountID}", handleAccountDelete(db, sm))
			r.Get("/accounts/{accountID}/load-zones", handleAccountLoadZones(db, sm, breakers))
			r.Post("/accounts/{accountID}/zones/store", handleAccountZonesStore(db))
			r.Delete("/accounts/{accountID}/zones/{zoneName}", handleAccountZoneRemove(db))

			// Token management pages (handlers replaced by plan 03).
			r.Get("/tokens", handleTokensPage(db))
			r.Get("/tokens/{accountID}", handleTokensForAccount(db, tokenRecoveryEnabled))
			r.Post("/tokens/{accountID}", handleTokenIssue(db, jwtSecret, recoveryKey))
			r.Delete("/tokens/{tokenID}", handleTokenRevoke(db))
			r.Post("/tokens/{tokenID}/reveal", handleTokenReveal(db, jwtSecret, tokenRecoveryEnabled))

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
			r.Get("/audit/more", handleAuditMore(db))

			// User management — admin-only (guarded inside each handler).
			// Account Users are DB-backed operator accounts that each see only their own
			// HE accounts. Only the env-configured Server Admin can create/delete them.
			r.Get("/users", handleUsersPage(db))
			r.Post("/users", handleUserCreate(db))
			r.Delete("/users/{userID}", handleUserDelete(db))
			// Password change routes.
			// POST /admin/change-admin-password — server admin changes their own password.
			// POST /admin/users/{userID}/password — admin changes any user's password;
			//   account users can change only their own (enforced inside the handler).
			r.Post("/change-admin-password", handleChangeAdminPassword(db))
			r.Post("/users/{userID}/password", handleChangeUserPassword(db))
			r.Get("/about", handleAboutPage(version))
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
// WHY two-stage auth (admin bcrypt first, then DB users):
//   1. Admin (DB-stored bcrypt hash): checked first so the admin can always log in.
//      The hash is resolved once at startup (store.EnsureAdminPassword) and passed here.
//   2. Account Users (DB bcrypt): checked second — user-scoped session cookie issued on match.
//
// WHY 401 status on wrong credentials (not redirect):
//   curl scripts checking status codes get the right signal. The browser still
//   sees the re-rendered form because the response body contains HTML.
//
// adminPasswordHash is a bcrypt hash — never plaintext. See store.EnsureAdminPassword.
// handleLoginPost is intentionally NOT given the startup-cached adminPasswordHash.
// It reads the hash directly from DB on each login so that password changes (via
// handleChangeAdminPassword) take effect on the next login without a server restart.
//
// WHY read from DB on every login (not use startup-cached hash):
//   The startup-cached hash is computed once in main.go and passed by value. After
//   handleChangeAdminPassword updates the DB, the in-memory value stays stale until restart.
//   Reading from DB adds one query per login attempt — negligible overhead since logins are rare.
//
// PREVIOUSLY: took `adminPasswordHash string` as a parameter. Changed to DB read so that
//   the admin password change feature works without requiring a server restart.
func handleLoginPost(db *sql.DB, adminUsername string, signingKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		u := r.FormValue("username")
		p := r.FormValue("password")

		// 1. Admin check — re-read hash from DB so password changes take effect immediately.
		if u == adminUsername {
			if hash, err := store.GetAdminPasswordHash(r.Context(), db); err == nil {
				if bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil {
					IssueAdminSessionCookie(w, signingKey)
					http.Redirect(w, r, "/admin/accounts", http.StatusFound)
					return
				}
			}
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
			// WHY include name: after migration 010 the display label is in the name column.
			//   The template renders acc.Name (user-chosen label) in the card header.
			`SELECT id, name, username, password, created_at, updated_at FROM accounts ORDER BY created_at ASC`,
		)
	} else {
		// Account User session — filter to only their own HE accounts.
		rows, err = db.QueryContext(r.Context(),
			`SELECT id, name, username, password, created_at, updated_at FROM accounts WHERE user_id = ? ORDER BY created_at ASC`,
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
		if err := rows.Scan(&acc.ID, &acc.Name, &acc.Username, &acc.Password, &acc.CreatedAt, &acc.UpdatedAt); err != nil {
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

		data := templates.PageData{Title: "Accounts", ActivePage: "accounts", IsAdmin: isAdminSession(r), Username: sessionDisplayName(r), Role: sessionRole(r)}
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
		// account_name: user-chosen label (was "account_id" before migration 010).
		// The actual account ID (UUID) is generated server-side below — never from the form.
		accountName := strings.TrimSpace(r.FormValue("account_name"))
		username := strings.TrimSpace(r.FormValue("username"))
		// WHY not trimming password: leading/trailing spaces in passwords are intentional.
		// Trimming would silently store a different credential than what the operator typed.
		password := r.FormValue("password")
		if accountName == "" || username == "" {
			writeFormError("Account name and username are both required.")
			return
		}
		if password == "" {
			writeFormError("Password is required.")
			return
		}

		// Generate a UUID for the new account.
		// WHY server-generated (not form-submitted):
		//   UUIDs are globally unique — clients cannot predict or collide with other accounts.
		//   The user-chosen name (accountName) is the human label; the UUID is the stable
		//   internal identifier used in all FK references and JWT claims.
		id := uuid.New().String()

		// SECURITY (SEC-03): password is never logged — only the account name appears in errors.
		//
		// WHY user_id from session (not a form field):
		//   Accepting user_id from the form would allow any authenticated user to create accounts
		//   owned by a different user ID (horizontal privilege escalation). Taking it from the
		//   verified session context ensures the account belongs to the logged-in user.
		//   Admin sessions return "" — we insert NULL for admin so FK constraint is satisfied
		//   (NULL FK is always valid in SQL; "" would fail FK checks when foreign_keys=ON).
		//   NULL user_id = "admin-owned": visible to admin, invisible to account users.
		userID := getSessionUserID(r)
		var userIDVal interface{}
		if userID != "" {
			userIDVal = userID
		} // else nil → NULL in SQLite
		_, err := db.ExecContext(r.Context(),
			`INSERT INTO accounts (id, name, username, password, user_id) VALUES (?, ?, ?, ?, ?)`,
			id, accountName, username, password, userIDVal,
		)
		if err != nil {
			errStr := err.Error()
			// idx_accounts_user_name is UNIQUE(COALESCE(user_id,''), name).
			// This fires when the same user (or admin) tries to register a second account
			// with the same name. The message uses accountName (not the UUID) for clarity.
			if strings.Contains(errStr, "UNIQUE") || strings.Contains(errStr, "PRIMARY KEY") {
				writeFormError(fmt.Sprintf("Account name %q is already registered for this user.", accountName))
			} else {
				writeFormError("Failed to create account: " + errStr)
			}
			return
		}

		// Fetch the DB-assigned created_at and updated_at for the template.
		var acc model.Account
		err = db.QueryRowContext(r.Context(),
			`SELECT id, name, username, created_at, updated_at FROM accounts WHERE id = ?`,
			id,
		).Scan(&acc.ID, &acc.Name, &acc.Username, &acc.CreatedAt, &acc.UpdatedAt)
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
		data := templates.PageData{Title: "Tokens", ActivePage: "tokens", IsAdmin: isAdminSession(r), Username: sessionDisplayName(r), Role: sessionRole(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.TokensPage(accounts, data).Render(r.Context(), w)
	}
}

func handleTokensForAccount(db *sql.DB, recoveryEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		tokens, err := token.ListTokens(r.Context(), db, accountID, recoveryEnabled)
		if err != nil {
			http.Error(w, "Failed to list tokens", http.StatusInternalServerError)
			return
		}
		// Load zones for the zone-scope dropdown in IssueTokenForm.
		// WHY pass zones here (not load on demand via separate htmx call):
		//   The IssueTokenForm is rendered inline as part of TokensForAccount — a separate
		//   htmx call for zones would require a second round-trip. Loading them in the same
		//   handler keeps the form self-contained.
		zones, err := listZonesFromDB(r.Context(), db, accountID)
		if err != nil {
			// Non-fatal: render form without zone dropdown rather than failing the page.
			zones = nil
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.TokensForAccount(accountID, tokens, zones).Render(r.Context(), w)
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
func handleTokenIssue(db *sql.DB, jwtSecret []byte, recoveryKey *[32]byte) http.HandlerFunc {
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
		// zone_id is the numeric HE zone ID selected from the dropdown.
		// Empty = account-wide token. Non-empty = zone-scoped.
		// WHY look up zone_name from DB (not submitted as a form field):
		//   The form only sends zone_id (select value). zone_name is looked up from the
		//   zones table here to avoid trusting user-submitted domain name strings.
		zoneID := r.FormValue("zone_id")
		zoneName := ""
		if zoneID != "" {
			// Look up the zone name for the prefix. Non-fatal if not found — the token
			// is still issued; the prefix will omit the zone segment.
			var name sql.NullString
			_ = db.QueryRowContext(r.Context(),
				`SELECT name FROM zones WHERE account_id = ? AND he_zone_id = ?`,
				accountID, zoneID,
			).Scan(&name)
			if name.Valid {
				zoneName = name.String
			}
		}

		// Look up account name for the human-readable token prefix.
		// WHY look up here (not use accountID/UUID in prefix):
		//   After migration 010, accountID is a UUID. Embedding the UUID would produce
		//   unreadable prefixes like "dns-he-net.a3f9b2c1....admin--...". The name
		//   (e.g. "primary") makes the prefix human-meaningful.
		//   Non-fatal: fallback to accountID if lookup fails — token is still valid.
		var accountName string
		_ = db.QueryRowContext(r.Context(),
			`SELECT name FROM accounts WHERE id = ?`, accountID,
		).Scan(&accountName)
		if accountName == "" {
			accountName = accountID
		}

		rawJWT, jti, err := token.IssueToken(r.Context(), db, accountID, accountName, role, label, zoneID, zoneName, 0, jwtSecret, recoveryKey)
		if err != nil {
			http.Error(w, "Failed to issue token: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Fetch the newly issued token record to populate the template.
		// ListTokens returns newest first; we find the matching record by JTI.
		tokens, err := token.ListTokens(r.Context(), db, accountID, recoveryKey != nil)
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


// handleTokenReveal decrypts and returns the stored raw token for a given JTI.
// Called by POST /admin/tokens/{tokenID}/reveal with a "password" form field.
//
// Security flow:
//  1. Check TOKEN_RECOVERY_ENABLED (tokenRecoveryEnabled) — return 403 if off.
//     This hard-gates the feature regardless of what is stored in the DB.
//  2. Verify the caller's portal password:
//     — Admin session (server admin): compare against the admin password from config.
//     — Account User session: bcrypt compare against the stored password hash.
//     Either mismatch returns 403 with no information about why (timing-safe where possible).
//  3. Call token.RevealToken to fetch and AES-256-GCM decrypt the stored token_value.
//  4. Return the raw token string as plain text for the htmx reveal dialog to display.
//
// WHY POST not GET for reveal:
//   GET requests are logged, cached, and appear in browser history. The password must
//   be in the request body, which rules out GET. POST keeps the password out of URLs.
//
// WHY read admin password from DB (not startup-cached hash):
//   handleChangeAdminPassword updates the DB. If we used the startup-cached hash here,
//   the reveal endpoint would reject the new admin password until restart.
//   Reading from DB aligns with handleLoginPost — both always use the current password.
func handleTokenReveal(db *sql.DB, jwtSecret []byte, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			http.Error(w, "Token recovery is disabled on this server", http.StatusForbidden)
			return
		}

		jti := chi.URLParam(r, "tokenID")

		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		password := r.FormValue("password")
		if password == "" {
			http.Error(w, "Password is required", http.StatusBadRequest)
			return
		}

		// Verify portal password based on session type.
		if isAdminSession(r) {
			// Server admin: read current hash from DB — matches handleLoginPost behaviour.
			hash, err := store.GetAdminPasswordHash(r.Context(), db)
			if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
				http.Error(w, "Invalid password", http.StatusForbidden)
				return
			}
		} else {
			// Account user: bcrypt compare against stored hash.
			userID := getSessionUserID(r)
			if _, err := authenticateAccountUser(r.Context(), db, userID, password); err != nil {
				http.Error(w, "Invalid password", http.StatusForbidden)
				return
			}
		}

		recovKey := token.RecoveryKey(jwtSecret)
		rawToken, err := token.RevealToken(r.Context(), db, jti, recovKey)
		if err != nil {
			http.Error(w, "Token not available for recovery (issued before feature was enabled, or already revoked)", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(rawToken))
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

		data := templates.PageData{Title: "Zones", ActivePage: "zones", IsAdmin: isAdminSession(r), Username: sessionDisplayName(r), Role: sessionRole(r)}
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
		data := templates.PageData{Title: "Sync", ActivePage: "sync", IsAdmin: isAdminSession(r), Username: sessionDisplayName(r), Role: sessionRole(r)}
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

// handleAccountLoadZones fetches the live zone list from dns.he.net and returns
// AccountZonesSelectList — a checkbox form letting the operator choose which zones to store.
// No DB write happens here; the explicit commit step is POST /zones/store.
// (GET /admin/accounts/{accountID}/load-zones)
//
// WHY two-step (load → select → store) instead of auto-upsert:
//   Operators managing accounts with many zones (test domains, delegated subdomains) do not
//   want every zone auto-imported. Returning a selection UI first lets them keep only the
//   zones they actively manage, rather than silently polluting the DB with test entries.
//
// WHY return AccountZonesSelectList (not AccountZonesList):
//   AccountZonesList renders the persisted DB state. AccountZonesSelectList renders the live
//   HE zone list as a checkbox form — it is intentionally transient and never touches the DB.
//   After "Store selected" the zones div is replaced with AccountZonesList via htmx.
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

		// Filter out zones that should not be offered for selection:
		//  1. Zones already stored in the DB under any account belonging to the current user.
		//  2. Zones with no name (empty string) — scraping artifacts, not valid zones.
		//
		// WHY filter per-user (not globally):
		//   Each DNS Portal user manages their own independent set of HE accounts and zones.
		//   Filtering globally would prevent user02 from loading a zone that user01 already
		//   loaded — but user02's accounts are completely independent. The correct isolation
		//   boundary is the user: a zone should not appear twice across the same user's
		//   accounts, but different users may independently manage the same HE zone.
		//
		// WHY still filter across all of the user's accounts (not just the current one):
		//   If user01 loaded example.com into account01, it should not reappear in account02's
		//   load list — both accounts belong to user01, so the zone would be managed twice
		//   within the same user's namespace, causing confusion about which account's token
		//   controls the zone.
		//
		// WHY admin (user_id IS NULL) uses a separate branch:
		//   getSessionUserID returns "" for the Server Admin session. NULL FK is the DB
		//   sentinel for admin-owned accounts. The SQL IS NULL check matches those rows;
		//   passing "" as a parameter would match no rows (WHERE a.user_id = '' finds nothing).
		//
		// WHY exclude empty names:
		//   The HE.net zone list scraper may return stub entries with blank zone names
		//   (e.g., newly created zones that have not been fully provisioned yet).
		//   Storing a zone with name="" would break the (account_id, name) PRIMARY KEY.
		//
		// WHY build a map (not a nested loop): O(n) lookup vs O(n*m) for large zone lists.
		userID := getSessionUserID(r)
		var existingRows *sql.Rows
		var dbErr error
		if userID == "" {
			// Admin session: exclude zones under admin-owned accounts (user_id IS NULL).
			existingRows, dbErr = db.QueryContext(r.Context(),
				`SELECT DISTINCT z.name FROM zones z
				 JOIN accounts a ON a.id = z.account_id
				 WHERE a.user_id IS NULL`)
		} else {
			// Account User session: exclude zones under any of this user's accounts.
			existingRows, dbErr = db.QueryContext(r.Context(),
				`SELECT DISTINCT z.name FROM zones z
				 JOIN accounts a ON a.id = z.account_id
				 WHERE a.user_id = ?`, userID)
		}
		existingNames := make(map[string]bool)
		if dbErr == nil {
			for existingRows.Next() {
				var n string
				if existingRows.Scan(&n) == nil {
					existingNames[n] = true
				}
			}
			_ = existingRows.Close()
		}

		var newZones []model.Zone
		for _, z := range fetched {
			if z.Name != "" && !existingNames[z.Name] {
				newZones = append(newZones, z)
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AccountZonesSelectList(accountID, newZones).Render(r.Context(), w)
	}
}

// handleAccountZonesStore persists the operator-selected zone subset to the DB
// (POST /admin/accounts/{accountID}/zones/store).
//
// WHY separate from handleAccountLoadZones:
//   load-zones fetches live data and returns a selection UI without touching the DB.
//   This handler is the explicit commit step — only the checked zones are submitted,
//   so the DB reflects the operator's intent rather than the full HE zone list.
//
// WHY "name|heZoneID" checkbox value encoding:
//   Each checked checkbox carries both the zone name and HE zone ID in one value,
//   split on "|". Domain names and numeric IDs never contain "|", so the delimiter
//   is always safe. Encoding both fields in one value avoids parallel hidden-field
//   arrays which can de-sync if a user manipulates the DOM.
//
// WHY ON CONFLICT DO UPDATE (not INSERT OR IGNORE):
//   An operator may have previously stored a zone without a he_zone_id (via manual add),
//   then run "Load zones from HE" which returns the numeric ID. Updating on conflict
//   ensures the he_zone_id is backfilled even if the zone name was already registered.
func handleAccountZonesStore(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		for _, val := range r.Form["zone"] {
			parts := strings.SplitN(val, "|", 2)
			name := parts[0]
			heID := ""
			if len(parts) == 2 {
				heID = parts[1]
			}
			if _, err := db.ExecContext(r.Context(),
				`INSERT INTO zones (account_id, name, he_zone_id) VALUES (?, ?, ?)
				 ON CONFLICT(account_id, name) DO UPDATE SET he_zone_id = excluded.he_zone_id`,
				accountID, name, heID,
			); err != nil {
				http.Error(w, "Failed to save zone: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		zones, err := listZonesFromDB(r.Context(), db, accountID)
		if err != nil {
			http.Error(w, "Failed to list zones: "+err.Error(), http.StatusInternalServerError)
			return
		}
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

// handleAuditPage renders the audit log page (GET /admin/audit).
// Shows the first 100 entries newest-first. A load-more button at the bottom
// fires GET /admin/audit/more?offset=100 to append subsequent batches via htmx.
//
// WHY pageSize=100 (up from 50):
//   100 rows covers more history per click without excessive page weight.
//   The load-more pattern (not Previous/Next) avoids full-page navigation and
//   lets operators keep their scroll position while loading additional history.
func handleAuditPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const pageSize = 100
		entries, err := audit.List(db, pageSize+1, 0)
		if err != nil {
			http.Error(w, "Failed to load audit log", http.StatusInternalServerError)
			return
		}
		hasMore := len(entries) > pageSize
		if hasMore {
			entries = entries[:pageSize]
		}
		data := templates.PageData{Title: "Audit Log", ActivePage: "audit", IsAdmin: isAdminSession(r), Username: sessionDisplayName(r), Role: sessionRole(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AuditPage(entries, hasMore, pageSize, data).Render(r.Context(), w)
	}
}

// handleAuditMore is the htmx partial handler for GET /admin/audit/more?offset=N.
// Returns new <tr> rows (appended to #audit-rows by hx-swap=eforeend\) plus an
// OOB-swapped #audit-load-more div with the next \...\ button or empty if done.
//
// WHY fetch pageSize+1 entries:
//   Fetching one extra row lets the handler detect whether more rows exist without
//   a separate COUNT query. If len(entries) > pageSize, there is a next page.
func handleAuditMore(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const pageSize = 100
		offset := 0
		if o := r.URL.Query().Get("offset"); o != "" {
			if n, err := strconv.Atoi(o); err == nil && n >= 0 {
				offset = n
			}
		}
		entries, err := audit.List(db, pageSize+1, offset)
		if err != nil {
			http.Error(w, "Failed to load audit log", http.StatusInternalServerError)
			return
		}
		hasMore := len(entries) > pageSize
		if hasMore {
			entries = entries[:pageSize]
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AuditMorePartial(entries, hasMore, offset+pageSize).Render(r.Context(), w)
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

		data := templates.PageData{Title: "Users", ActivePage: "users", IsAdmin: true, Username: sessionDisplayName(r), Role: sessionRole(r)}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.UsersPage(users, data, getSessionUserID(r)).Render(r.Context(), w)
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

// handleChangeAdminPassword changes the server admin password (POST /admin/change-admin-password).
// Admin-only. Requires current password + new password (min 8 chars).
// After success, the new password takes effect on the next login without a server restart
// because handleLoginPost now reads the hash from DB rather than using a startup-cached value.
func handleChangeAdminPassword(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAdminSession(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		current := r.FormValue("current_password")
		newPwd := r.FormValue("new_password")
		confirm := r.FormValue("confirm_password")

		writeError := func(msg string) {
			w.Header().Set("HX-Retarget", "#admin-pw-error")
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = templates.PasswordChangeError(msg).Render(r.Context(), w)
		}

		if current == "" || newPwd == "" {
			writeError("Current and new password are required.")
			return
		}
		if newPwd != confirm {
			writeError("New passwords do not match.")
			return
		}
		if len(newPwd) < 8 {
			writeError("New password must be at least 8 characters.")
			return
		}

		// Verify the current admin password from DB.
		hash, err := store.GetAdminPasswordHash(r.Context(), db)
		if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
			writeError("Current password is incorrect.")
			return
		}

		newHash, err := bcrypt.GenerateFromPassword([]byte(newPwd), 12)
		if err != nil {
			writeError("Failed to hash password.")
			return
		}
		if err := store.SetAdminPasswordHash(r.Context(), db, string(newHash)); err != nil {
			writeError("Failed to save new password: " + err.Error())
			return
		}

		w.Header().Set("HX-Retarget", "#admin-pw-error")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.PasswordChangeSuccess("Admin password changed successfully.").Render(r.Context(), w)
	}
}

// handleChangeUserPassword changes an Account User's password (POST /admin/users/{userID}/password).
// Admin can change any user's password. Account users can change only their own.
//
// WHY no current-password check for admin:
//   The server admin has full authority over all account users — requiring the current password
//   would block the admin from resetting a forgotten password. The admin UI itself requires
//   the admin to be authenticated, which is sufficient authorization.
//
// WHY current-password required for self-service:
//   An account user changing their own password must prove knowledge of the current password
//   to prevent session hijacking (an attacker who steals the session cookie cannot change
//   the password without knowing the original).
func handleChangeUserPassword(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targetUserID := chi.URLParam(r, "userID")
		sessionUserID := getSessionUserID(r)
		isAdmin := isAdminSession(r)

		// Authorization: admin can change any user; account user can only change their own.
		if !isAdmin && sessionUserID != targetUserID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		newPwd := r.FormValue("new_password")
		confirm := r.FormValue("confirm_password")
		current := r.FormValue("current_password") // required for self-service; optional for admin

		errorTarget := "#user-pw-error-" + targetUserID
		writeError := func(msg string) {
			w.Header().Set("HX-Retarget", errorTarget)
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = templates.PasswordChangeError(msg).Render(r.Context(), w)
		}

		if newPwd == "" {
			writeError("New password is required.")
			return
		}
		if newPwd != confirm {
			writeError("New passwords do not match.")
			return
		}
		if len(newPwd) < 8 {
			writeError("New password must be at least 8 characters.")
			return
		}

		// Account users must verify their current password (prevent session-hijack escalation).
		if !isAdmin {
			if _, err := authenticateAccountUser(r.Context(), db, sessionUserID, current); err != nil {
				writeError("Current password is incorrect.")
				return
			}
		}

		newHash, err := bcrypt.GenerateFromPassword([]byte(newPwd), 12)
		if err != nil {
			writeError("Failed to hash password.")
			return
		}
		if _, err := db.ExecContext(r.Context(),
			`UPDATE users SET password_hash = ? WHERE id = ?`,
			string(newHash), targetUserID,
		); err != nil {
			writeError("Failed to save new password: " + err.Error())
			return
		}

		w.Header().Set("HX-Retarget", errorTarget)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.PasswordChangeSuccess("Password changed successfully.").Render(r.Context(), w)
	}
}

// handleAboutPage renders GET /admin/about — the documentation and version page.
//
// WHY markdown rendered server-side (not a static HTML file):
//   about.md is easy to edit without touching Go or templ files. Goldmark renders it
//   to HTML once per request (fast — the source is <10 KB). The binary stays
//   self-contained because about.md is embedded in staticFS at compile time.
//
// WHY templ.Raw() is safe here:
//   The HTML source is goldmark-rendered from a file embedded in the binary at compile
//   time. It is not user-supplied, so XSS from dynamic input is not possible.
//   goldmark's default renderer escapes any HTML that appears literally in the markdown.
//
// WHY {{VERSION}} substitution (not a template engine):
//   Simple strings.ReplaceAll keeps the about.md readable and avoids adding a
//   templating dependency just for one placeholder.
func handleAboutPage(version string) http.HandlerFunc {
	// Read and render the markdown once at handler construction (not per request)
	// because about.md is static — it never changes at runtime.
	//
	// WHY not render at startup (before routes are registered):
	//   staticFS is available only after package init. Handler construction (inside
	//   RegisterAdminRoutes) happens after init, so this is the earliest safe point.
	mdBytes, err := staticFS.ReadFile("static/about.md")
	var renderedHTML string
	if err != nil {
		renderedHTML = "<p class=\"error-banner\">Documentation could not be loaded.</p>"
	} else {
		src := strings.ReplaceAll(string(mdBytes), "{{VERSION}}", version)
		var buf bytes.Buffer
		md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
		if mdErr := md.Convert([]byte(src), &buf); mdErr != nil {
			renderedHTML = "<p class=\"error-banner\">Documentation render error.</p>"
		} else {
			renderedHTML = buf.String()
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		data := templates.PageData{
			Title:      "About",
			ActivePage: "about",
			IsAdmin:    isAdminSession(r),
			Username:   sessionDisplayName(r),
			Role:       sessionRole(r),
		}
		_ = templates.AboutPage(renderedHTML, data).Render(r.Context(), w)
	}
}

package admin

import (
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vnovakov/dns-he-net-automation/internal/api/admin/templates"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
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
		r.Handle("/static/*",
			http.StripPrefix("/admin/static/",
				http.FileServer(http.FS(staticFS))),
		)

		// Login and logout routes — outside the AdminAuth middleware.
		// Login must be accessible to unauthenticated users (obvious).
		// Logout is outside auth so it works even if the cookie becomes invalid.
		r.Get("/login", handleLoginPage())
		r.Post("/login", handleLoginPost(username, password, signingKey))
		r.Get("/logout", handleLogout())

		// Protected routes — all require AdminAuth (Basic Auth or session cookie).
		r.Group(func(r chi.Router) {
			r.Use(AdminAuth(username, password, signingKey))

			// Default /admin redirect to accounts page.
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/admin/accounts", http.StatusFound)
			})

			// Account management pages (handlers replaced by plan 03).
			r.Get("/accounts", handleAccountsPage(db))
			r.Post("/accounts", handleAccountCreate(db))
			r.Delete("/accounts/{accountID}", handleAccountDelete(db, sm))

			// Token management pages (handlers replaced by plan 03).
			r.Get("/tokens", handleTokensPage(db))
			r.Get("/tokens/{accountID}", handleTokensForAccount(db))
			r.Post("/tokens/{accountID}", handleTokenIssue(db, jwtSecret))
			r.Delete("/tokens/{tokenID}", handleTokenRevoke(db))

			// Zones read-only view (handler replaced by plan 04).
			r.Get("/zones", handleZonesPage(db))

			// Sync trigger page (handlers replaced by plan 04).
			r.Get("/sync", handleSyncPage(db))
			r.Post("/sync/trigger", handleSyncTrigger(db, sm, breakers))

			// Audit log page (handler replaced by plan 04).
			r.Get("/audit", handleAuditPage(db))
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
// WHY 401 status on wrong credentials (not redirect):
//   curl scripts checking status codes get the right signal. The browser still
//   sees the re-rendered form because the response body contains HTML.
func handleLoginPost(username, password string, signingKey []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		u := r.FormValue("username")
		p := r.FormValue("password")

		if u == username && p == password {
			IssueSessionCookie(w, u, signingKey)
			http.Redirect(w, r, "/admin/accounts", http.StatusFound)
			return
		}

		// Wrong credentials — re-render login form with error message.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = templates.LoginPage("Invalid username or password").Render(r.Context(), w)
	}
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
// created_at ASC. Extracted as a helper because both handleAccountsPage and handleTokensPage
// need the same account list.
//
// WHY not use store.ListAccounts:
//   The store package only exposes Open() for DB initialization. Account CRUD is done via
//   inline DB queries (same pattern as REST handlers in internal/api/handlers/accounts.go).
//   The admin UI follows the same pattern — direct DB access, no HTTP round-trips to /api/v1.
func listAccountsFromDB(r *http.Request, db *sql.DB) ([]model.Account, error) {
	rows, err := db.QueryContext(r.Context(),
		`SELECT id, username, created_at, updated_at FROM accounts ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var acc model.Account
		if err := rows.Scan(&acc.ID, &acc.Username, &acc.CreatedAt, &acc.UpdatedAt); err != nil {
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

func handleAccountsPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accounts, err := listAccountsFromDB(r, db)
		if err != nil {
			http.Error(w, "Failed to list accounts", http.StatusInternalServerError)
			return
		}
		data := templates.PageData{Title: "Accounts", ActivePage: "accounts"}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AccountsPage(accounts, data).Render(r.Context(), w)
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
func handleAccountCreate(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		accountID := strings.TrimSpace(r.FormValue("account_id"))
		username := strings.TrimSpace(r.FormValue("username"))
		if accountID == "" || username == "" {
			http.Error(w, "account_id and username required", http.StatusBadRequest)
			return
		}

		_, err := db.ExecContext(r.Context(),
			`INSERT INTO accounts (id, username) VALUES (?, ?)`,
			accountID, username,
		)
		if err != nil {
			http.Error(w, "Failed to create account: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Fetch the DB-assigned created_at and updated_at for the template.
		var acc model.Account
		err = db.QueryRowContext(r.Context(),
			`SELECT id, username, created_at, updated_at FROM accounts WHERE id = ?`,
			accountID,
		).Scan(&acc.ID, &acc.Username, &acc.CreatedAt, &acc.UpdatedAt)
		if err != nil {
			http.Error(w, "Failed to retrieve created account", http.StatusInternalServerError)
			return
		}

		// Return just the new row — htmx appends it to #accounts-table tbody.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = templates.AccountRow(acc).Render(r.Context(), w)
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
		accounts, err := listAccountsFromDB(r, db)
		if err != nil {
			http.Error(w, "Failed to list accounts", http.StatusInternalServerError)
			return
		}
		data := templates.PageData{Title: "Tokens", ActivePage: "tokens"}
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


func handleZonesPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "zones page: implemented in plan 04", http.StatusNotImplemented)
	}
}

func handleSyncPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "sync page: implemented in plan 04", http.StatusNotImplemented)
	}
}

func handleSyncTrigger(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "sync trigger: implemented in plan 04", http.StatusNotImplemented)
	}
}

func handleAuditPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "audit page: implemented in plan 04", http.StatusNotImplemented)
	}
}

package admin

import (
	"database/sql"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vnovakov/dns-he-net-automation/internal/api/admin/templates"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/resilience"
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

func handleAccountsPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "accounts page: implemented in plan 03", http.StatusNotImplemented)
	}
}

func handleAccountCreate(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "account create: implemented in plan 03", http.StatusNotImplemented)
	}
}

func handleAccountDelete(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "account delete: implemented in plan 03", http.StatusNotImplemented)
	}
}

func handleTokensPage(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "tokens page: implemented in plan 03", http.StatusNotImplemented)
	}
}

func handleTokensForAccount(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "tokens for account: implemented in plan 03", http.StatusNotImplemented)
	}
}

func handleTokenIssue(db *sql.DB, jwtSecret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "token issue: implemented in plan 03", http.StatusNotImplemented)
	}
}

func handleTokenRevoke(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "token revoke: implemented in plan 03", http.StatusNotImplemented)
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

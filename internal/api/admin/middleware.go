// Package admin provides the embedded admin UI for dns-he-net-automation.
//
// Auth strategy (CONTEXT.md decision):
//   - HTTP Basic Auth checked first — for curl/scripted access (ADMIN_USERNAME/ADMIN_PASSWORD).
//   - HMAC-SHA256 signed session cookie checked second — for browser sessions.
//   - Neither present: redirect to /admin/login.
//
// The admin auth layer is completely separate from the REST API Bearer JWT system.
// This prevents coupling: rotating JWT_SECRET does not log out admin sessions.
// (RESEARCH.md open question 2 resolution: separate signing keys for separate auth domains)
package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const sessionCookieName = "admin_session"

// Context keys for values injected by AdminAuth into the request context.
//
// WHY a typed contextKey (not a plain string):
//   Using a package-private type prevents key collisions with other middleware
//   that might also inject values keyed by plain strings. This is the standard
//   Go pattern for context keys (https://pkg.go.dev/context#WithValue).
type contextKey string

const (
	// ctxIsAdmin is true when the session belongs to the env-configured server admin.
	// false means an Account User session (or unauthenticated — but AdminAuth blocks that).
	ctxIsAdmin contextKey = "isAdmin"

	// ctxUserID holds the Account User's id (= username) for user sessions.
	// Empty string for admin sessions — admin has no DB row.
	ctxUserID contextKey = "userID"
)

// isAdminSession returns true if the current request carries an admin session.
// Safe to call after AdminAuth middleware has run (always returns false otherwise).
func isAdminSession(r *http.Request) bool {
	v, _ := r.Context().Value(ctxIsAdmin).(bool)
	return v
}

// getSessionUserID returns the Account User ID from the session context.
// Returns "" for admin sessions (admin has no DB user ID).
// Safe to call after AdminAuth middleware has run.
func getSessionUserID(r *http.Request) string {
	v, _ := r.Context().Value(ctxUserID).(string)
	return v
}

// sessionDisplayName returns the username to show in the top-bar identity badge.
// Admin sessions always show "admin"; Account User sessions show their user ID.
func sessionDisplayName(r *http.Request) string {
	if isAdminSession(r) {
		return "admin"
	}
	return getSessionUserID(r)
}

// sessionRole returns the human-readable role label for the top-bar identity badge.
// "Admin" for the env-configured server admin; "User" for Account Users.
func sessionRole(r *http.Request) string {
	if isAdminSession(r) {
		return "Admin"
	}
	return "User"
}

// AdminAuth returns a middleware that protects /admin routes with Basic Auth + session cookie.
//
// Check order (CONTEXT.md decision — Basic Auth checked before cookie):
//  1. HTTP Basic Auth header present → validate credentials → 200 or 401.
//  2. Signed session cookie present → validate HMAC → inject context → pass through or redirect.
//  3. Neither → redirect to /admin/login.
//
// Cookie format (new multi-user format):
//   hex(HMAC-SHA256(sessionCookieName + role + ":" + identifier)) + ":" + role + ":" + identifier
//   Admin:   HMAC:admin:admin-username
//   User:    HMAC:user:user-id
//
// WHY changing the cookie payload (compared to the old HMAC:username format):
//   Adding role to the HMAC input means old single-role cookies (HMAC:username) produce a
//   different signature than new cookies (HMAC:admin:username). This forces a re-login once
//   on upgrade, which is the correct and safe behavior — old cookies should not be reusable
//   after the format changes.
//
// SameSite=Strict prevents CSRF on POST/DELETE mutations in the admin UI.
// (RESEARCH.md Pitfall 5: SameSite=Lax is not sufficient for admin mutations)
//
// WHY /admin/login and /admin/static/* are excluded from auth:
//   Login page must be accessible before a session exists (obvious), and static assets
//   (CSS, JS) must load before the login page renders. Without these exclusions the
//   browser would redirect CSS/JS requests to /admin/login, causing a redirect loop
//   that prevents the login form from styling itself correctly.
func AdminAuth(username, password string, signingKey []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip auth for the login page itself and static assets to avoid redirect loop.
			if r.URL.Path == "/admin/login" || strings.HasPrefix(r.URL.Path, "/admin/static/") {
				next.ServeHTTP(w, r)
				return
			}

			// 1. HTTP Basic Auth — checked first for scripted/curl access.
			// Basic Auth is admin-only (env credentials). Account users must use session cookies.
			if u, p, ok := r.BasicAuth(); ok {
				if u == username && p == password {
					// Inject admin context so handlers can distinguish admin from user sessions.
					ctx := context.WithValue(r.Context(), ctxIsAdmin, true)
					ctx = context.WithValue(ctx, ctxUserID, "")
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Basic Auth credentials provided but wrong — return 401, NOT redirect.
				// A redirect would confuse curl scripts expecting 401 on bad credentials.
				// WWW-Authenticate header tells HTTP clients this endpoint supports Basic Auth.
				w.Header().Set("WWW-Authenticate", `Basic realm="DNS Admin"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// 2. Session cookie — checked for browser sessions.
			isAdmin, userID, ok := parseSessionCookie(r, signingKey)
			if ok {
				// Inject role and identity into context for downstream handlers.
				ctx := context.WithValue(r.Context(), ctxIsAdmin, isAdmin)
				ctx = context.WithValue(ctx, ctxUserID, userID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 3. Neither auth mechanism present — redirect to login form.
			http.Redirect(w, r, "/admin/login", http.StatusFound)
		})
	}
}

// IssueAdminSessionCookie sets a signed admin session cookie on the response.
// Called after successful admin login (env-var credentials match).
//
// Cookie format: HMAC:admin:admin-username
//
// WHY HMAC-SHA256 over a simple random token stored in DB:
//   No DB lookup required on every admin request — the HMAC signature is self-verifying.
//   This keeps admin auth stateless and fast, trading off the ability to invalidate
//   individual sessions (acceptable for a single-admin tool).
func IssueAdminSessionCookie(w http.ResponseWriter, signingKey []byte) {
	// HMAC covers the entire payload (role + ":" + identifier) to bind role and identity
	// together — forging an admin cookie from a valid user cookie is computationally infeasible.
	payload := "admin:" + "admin"
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(sessionCookieName + payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	cookieVal := sig + ":" + payload

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    cookieVal,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Secure: false — set to true when behind TLS terminator in production.
		// Omitting Secure allows HTTP development access without breaking the cookie.
		// In production, place this service behind an HTTPS reverse proxy (nginx/Caddy).
	})
}

// IssueUserSessionCookie sets a signed Account User session cookie on the response.
// Called after successful login with DB-stored user credentials.
//
// Cookie format: HMAC:user:user-id
func IssueUserSessionCookie(w http.ResponseWriter, userID string, signingKey []byte) {
	payload := "user:" + userID
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(sessionCookieName + payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	cookieVal := sig + ":" + payload

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    cookieVal,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie clears the session cookie on logout by setting MaxAge=-1.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// parseSessionCookie validates the HMAC signature of the session cookie and extracts
// the role and identifier.
//
// Returns (isAdmin, userID, ok):
//   - isAdmin=true, userID="", ok=true  → admin session
//   - isAdmin=false, userID=<id>, ok=true → Account User session
//   - ok=false → missing, malformed, or tampered cookie
//
// Cookie format: hex(HMAC) + ":" + role + ":" + identifier
//   → 3 parts when split on ":" with maxParts=3: [HMAC, role, identifier]
//
// WHY hmac.Equal for comparison:
//   Constant-time comparison prevents timing attacks — an attacker cannot determine
//   how many bytes of a forged signature are correct by measuring response latency.
//
// WHY HMAC covers sessionCookieName + role + ":" + identifier (not just identifier):
//   Binding the cookie name prevents a cookie issued for one cookie name from being
//   used for another. Binding the role prevents upgrading a user cookie to admin role
//   by changing the role field — the HMAC would not match.
func parseSessionCookie(r *http.Request, key []byte) (isAdmin bool, userID string, ok bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false, "", false
	}
	// Split into exactly 3 parts: [HMAC, role, identifier].
	// SplitN with n=3 allows the identifier to contain ":" if needed in the future.
	parts := strings.SplitN(c.Value, ":", 3)
	if len(parts) != 3 {
		return false, "", false
	}
	sigHex, role, identifier := parts[0], parts[1], parts[2]

	// Recompute expected HMAC over the full payload.
	payload := role + ":" + identifier
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sessionCookieName + payload))
	expected := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison — see WHY comment above.
	if !hmac.Equal([]byte(sigHex), []byte(expected)) {
		return false, "", false
	}

	switch role {
	case "admin":
		return true, "", true
	case "user":
		return false, identifier, true
	default:
		// Unknown role — reject to prevent future role bypasses.
		return false, "", false
	}
}

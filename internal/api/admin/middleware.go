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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const sessionCookieName = "admin_session"

// AdminAuth returns a middleware that protects /admin routes with Basic Auth + session cookie.
//
// Check order (CONTEXT.md decision — Basic Auth checked before cookie):
//  1. HTTP Basic Auth header present → validate credentials → 200 or 401.
//  2. Signed session cookie present → validate HMAC → pass through or redirect.
//  3. Neither → redirect to /admin/login.
//
// Cookie format: hex(HMAC-SHA256(sessionCookieName+username)) + ":" + username
// (RESEARCH.md Pattern 5 — no external dependency required)
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
			if u, p, ok := r.BasicAuth(); ok {
				if u == username && p == password {
					next.ServeHTTP(w, r)
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
			if validateSessionCookie(r, signingKey) {
				next.ServeHTTP(w, r)
				return
			}

			// 3. Neither auth mechanism present — redirect to login form.
			http.Redirect(w, r, "/admin/login", http.StatusFound)
		})
	}
}

// IssueSessionCookie sets a signed session cookie on the response.
// Called after successful form login (POST /admin/login).
//
// WHY HMAC-SHA256 over a simple random token stored in DB:
//   No DB lookup required on every admin request — the HMAC signature is self-verifying.
//   This keeps admin auth stateless and fast, trading off the ability to invalidate
//   individual sessions (acceptable for a single-admin tool).
func IssueSessionCookie(w http.ResponseWriter, username string, signingKey []byte) {
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
		// Secure: false — set to true when behind TLS terminator in production.
		// Omitting Secure allows HTTP development access without breaking the cookie.
		// In production, place this service behind an HTTPS reverse proxy (nginx/Caddy).
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

// validateSessionCookie checks the HMAC signature of the session cookie.
// Returns false if cookie is missing, malformed, or signature is invalid.
//
// WHY hmac.Equal for comparison:
//   Constant-time comparison prevents timing attacks — an attacker cannot determine
//   how many bytes of a forged signature are correct by measuring response latency.
func validateSessionCookie(r *http.Request, key []byte) bool {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	parts := strings.SplitN(c.Value, ":", 2)
	if len(parts) != 2 {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sessionCookieName + parts[1]))
	expected := hex.EncodeToString(mac.Sum(nil))
	// Use hmac.Equal for constant-time comparison to prevent timing attacks.
	return hmac.Equal([]byte(parts[0]), []byte(expected))
}

// Package middleware provides HTTP middleware for the dns-he-net-automation API.
package middleware

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/vnovakovic/dns-he-net-automation/internal/api/response"
	"github.com/vnovakovic/dns-he-net-automation/internal/token"
)

// contextKey is a private type for context keys in this package.
// Using a named type prevents collisions with context keys from other packages (research pitfall #4).
type contextKey string

// claimsContextKey is the key under which JWT claims are stored in request context.
const claimsContextKey contextKey = "jwt_claims"

// ClaimsFromContext retrieves JWT claims injected by BearerAuth middleware.
// Returns nil if not present (unauthenticated path or misconfigured middleware).
func ClaimsFromContext(ctx context.Context) *token.Claims {
	c, _ := ctx.Value(claimsContextKey).(*token.Claims)
	return c
}

// BearerAuth returns an HTTP middleware that validates JWT bearer tokens on every request.
//
// Validation steps:
//  1. Extract Authorization header; reject missing or malformed headers with 401.
//  2. Call token.ValidateToken which handles JWT parsing + revocation check.
//  3. Inject claims into request context for downstream handlers.
//  4. Log a structured entry with request_id, account_id, role, jti (OPS-02).
//
// Error codes:
//   - "missing_token"   — Authorization header absent or not Bearer scheme
//   - "token_not_found" — Token not in DB (never issued or DB mismatch)
//   - "token_revoked"   — Token was revoked via RevokeToken
//   - "invalid_token"   — Expired, bad signature, algorithm mismatch, or malformed
func BearerAuth(db *sql.DB, secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" || !strings.HasPrefix(header, "Bearer ") {
				response.WriteError(w, http.StatusUnauthorized, "missing_token", "Authorization header required")
				return
			}

			rawToken := strings.TrimPrefix(header, "Bearer ")

			claims, err := token.ValidateToken(r.Context(), db, rawToken, secret)
			if err != nil {
				// Map error messages from token.ValidateToken to API error codes.
				msg := err.Error()
				switch {
				case strings.Contains(msg, "token not found"):
					response.WriteError(w, http.StatusUnauthorized, "token_not_found", "Token not found")
				case strings.Contains(msg, "token revoked"):
					response.WriteError(w, http.StatusUnauthorized, "token_revoked", "Token has been revoked")
				default:
					response.WriteError(w, http.StatusUnauthorized, "invalid_token", "Invalid or expired token")
				}
				return
			}

			// Inject claims into context for downstream handlers.
			ctx := context.WithValue(r.Context(), claimsContextKey, claims)

			// Structured log entry for every authenticated request (OPS-02).
			// SECURITY: Never log raw token value (SEC-02).
			reqID := chiMiddleware.GetReqID(r.Context())
			slog.InfoContext(r.Context(), "token authenticated",
				"request_id", reqID,
				"account_id", claims.AccountID,
				"role", claims.Role,
				"jti", claims.ID,
			)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

package middleware

import (
	"net/http"

	"github.com/vnovakov/dns-he-net-automation/internal/api/response"
)

// RequireAdmin rejects requests where the authenticated token's role is not "admin".
// Must be used AFTER BearerAuth in the middleware chain.
// Returns 401 if claims are missing (misconfigured middleware stack),
// 403 if role is "viewer" or any non-admin value.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}
		if claims.Role != "admin" {
			response.WriteError(w, http.StatusForbidden, "insufficient_role", "Admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

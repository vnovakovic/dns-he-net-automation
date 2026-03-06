package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/vnovakovic/dns-he-net-automation/internal/api/response"
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

// RequireZoneAccess enforces zone-level token scoping.
// Must be used AFTER BearerAuth in the middleware chain, on routes that have a {zoneID} param.
//
// Rules:
//   - If claims.ZoneID == "" (account-wide token): allow access to any zone. No restriction.
//   - If claims.ZoneID != "" (zone-scoped token): the {zoneID} URL param must match claims.ZoneID exactly.
//     Mismatches return 403. This prevents a token issued for zone A from operating on zone B.
//
// WHY check claims.ZoneID (not zone_name):
//   ZoneID is the numeric HE zone ID — stable and unambiguous. Zone names can be renamed.
//   Enforcement via ZoneID guarantees the correct zone even if the domain name changes.
//
// WHY this is separate from RequireAdmin:
//   Zone scoping applies to both admin and viewer tokens. RequireAdmin gates on role;
//   RequireZoneAccess gates on zone scope. They compose independently.
//
// DEPENDENCY: router.go must apply this middleware to the /{zoneID} sub-router AFTER BearerAuth.
//   Applying it before BearerAuth would make claims == nil, always returning 401.
func RequireZoneAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}
		// Account-wide token: no zone restriction.
		if claims.ZoneID == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Zone-scoped token: URL {zoneID} must match the token's zone.
		urlZoneID := chi.URLParam(r, "zoneID")
		if urlZoneID != claims.ZoneID {
			response.WriteError(w, http.StatusForbidden, "zone_access_denied",
				"Token is not authorized for this zone")
			return
		}
		next.ServeHTTP(w, r)
	})
}

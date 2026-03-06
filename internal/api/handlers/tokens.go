package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/vnovakov/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/response"
	"github.com/vnovakov/dns-he-net-automation/internal/token"
)

// issueTokenRequest is the JSON body for POST /api/v1/accounts/{accountID}/tokens.
type issueTokenRequest struct {
	Role          string `json:"role"`
	Label         string `json:"label"`
	ExpiresInDays int    `json:"expires_in_days"`
	// ZoneID and ZoneName are optional. When ZoneID is set, the issued token is scoped
	// to that specific zone and will be rejected by RequireZoneAccess for any other zone.
	// ZoneName is informational only (included in token prefix for readability).
	// Leave both empty for an account-wide token.
	ZoneID   string `json:"zone_id"`
	ZoneName string `json:"zone_name"`
}

// issueTokenResponse is the JSON response for a newly issued token.
// The raw token is returned ONCE here and never stored (TOKEN-02, SEC-02).
type issueTokenResponse struct {
	JTI  string `json:"jti"`
	// raw token returned once; never stored (TOKEN-02, SEC-02)
	Token     string  `json:"token"`
	Role      string  `json:"role"`
	ExpiresAt *string `json:"expires_at"`
}

// IssueToken handles POST /api/v1/accounts/{accountID}/tokens.
// Requires admin role (enforced by RequireAdmin middleware in router).
// Enforces account isolation (ACCT-04).
// Returns 201 with raw JWT once — it is never stored and cannot be retrieved again
// unless TOKEN_RECOVERY_ENABLED=true, in which case recoveryKey must be non-nil and
// the encrypted token is stored in token_value (see token.IssueToken for details).
func IssueToken(db *sql.DB, secret []byte, recoveryKey *[32]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")

		// Account isolation check (ACCT-04).
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil || claims.AccountID != accountID {
			response.WriteError(w, http.StatusForbidden, "account_mismatch",
				"Token is not authorized for this account")
			return
		}

		var req issueTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}

		// Validate role: must be "admin" or "viewer" (TOKEN-03).
		if req.Role != "admin" && req.Role != "viewer" {
			response.WriteError(w, http.StatusBadRequest, "invalid_role",
				"Role must be \"admin\" or \"viewer\"")
			return
		}

		// Validate expires_in_days: must be >= 0 (TOKEN-04).
		if req.ExpiresInDays < 0 {
			response.WriteError(w, http.StatusBadRequest, "invalid_expires",
				"expires_in_days must be >= 0 (0 means unlimited)")
			return
		}

		// Validate label: max 200 chars (SEC-04).
		if len(req.Label) > 200 {
			response.WriteError(w, http.StatusBadRequest, "invalid_label",
				"Label must be 200 characters or fewer")
			return
		}

		rawToken, jti, err := token.IssueToken(r.Context(), db, accountID, req.Role, req.Label, req.ZoneID, req.ZoneName, req.ExpiresInDays, secret, recoveryKey)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "issue_error", "Failed to issue token")
			return
		}

		// Fetch the issued token record to get expires_at for the response.
		// recoveryEnabled=false: Recoverable field is unused in the API JSON response (json:"-").
		tokens, err := token.ListTokens(r.Context(), db, accountID, false)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to retrieve issued token")
			return
		}

		resp := issueTokenResponse{
			JTI:   jti,
			Token: rawToken,
			Role:  req.Role,
		}

		// Find the just-issued token in the list to get its expires_at.
		for _, tr := range tokens {
			if tr.JTI == jti {
				if tr.ExpiresAt != nil {
					s := tr.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
					resp.ExpiresAt = &s
				}
				break
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ListTokens handles GET /api/v1/accounts/{accountID}/tokens.
// Any authenticated token (admin or viewer) can list tokens for their account.
// Enforces account isolation (ACCT-04).
// SECURITY (TOKEN-06): Response never includes token_hash or raw token values.
// recoveryEnabled is passed through to token.ListTokens but Recoverable is json:"-"
// so it never appears in the API JSON response — safe to call with either value.
func ListTokens(db *sql.DB, recoveryEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")

		// Account isolation check (ACCT-04).
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil || claims.AccountID != accountID {
			response.WriteError(w, http.StatusForbidden, "account_mismatch",
				"Token is not authorized for this account")
			return
		}

		tokens, err := token.ListTokens(r.Context(), db, accountID, recoveryEnabled)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to list tokens")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"tokens": tokens})
	}
}

// RevokeToken handles DELETE /api/v1/accounts/{accountID}/tokens/{tokenID}.
// Requires admin role (enforced by RequireAdmin middleware in router).
// Enforces account isolation (ACCT-04).
// tokenID is the jti from the URL path parameter.
func RevokeToken(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")
		jti := chi.URLParam(r, "tokenID")

		// Account isolation check (ACCT-04).
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil || claims.AccountID != accountID {
			response.WriteError(w, http.StatusForbidden, "account_mismatch",
				"Token is not authorized for this account")
			return
		}

		err := token.RevokeToken(r.Context(), db, accountID, jti)
		if err != nil {
			if err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows") {
				response.WriteError(w, http.StatusNotFound, "token_not_found", "Token not found")
				return
			}
			response.WriteError(w, http.StatusInternalServerError, "revoke_error", "Failed to revoke token")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// Package handlers provides HTTP request handlers for the dns-he-net-automation API.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/vnovakov/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/response"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
)

// accountIDPattern validates account IDs: 1-64 chars, alphanumeric + dash + underscore (SEC-04).
var accountIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// usernamePattern validates usernames: 1-64 chars, alphanumeric + dash + underscore (SEC-04).
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// AccountRecord is the safe public representation of an account.
// SECURITY (SEC-01): Never include password or credential fields.
type AccountRecord struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// createAccountRequest is the JSON body for POST /api/v1/accounts.
type createAccountRequest struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// CreateAccount handles POST /api/v1/accounts.
// Registers a new account with id and username. Credentials come from env/Vault, not this request.
// Returns 201 with account record on success, 409 on duplicate, 400 on invalid input.
func CreateAccount(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}

		// Validate id: non-empty, max 64 chars, alphanumeric + dash + underscore (SEC-04).
		req.ID = strings.TrimSpace(req.ID)
		if !accountIDPattern.MatchString(req.ID) {
			response.WriteError(w, http.StatusBadRequest, "invalid_account_id",
				"Account ID must be 1-64 characters: alphanumeric, dash, or underscore")
			return
		}

		// Validate username: non-empty, max 64 chars, alphanumeric + dash + underscore (SEC-04).
		req.Username = strings.TrimSpace(req.Username)
		if !usernamePattern.MatchString(req.Username) {
			response.WriteError(w, http.StatusBadRequest, "invalid_username",
				"Username must be 1-64 characters: alphanumeric, dash, or underscore")
			return
		}

		// SECURITY (SEC-01): Never log username or any credential field.
		_, err := db.ExecContext(r.Context(),
			`INSERT INTO accounts (id, username) VALUES (?, ?)`,
			req.ID, req.Username,
		)
		if err != nil {
			// Detect SQLite UNIQUE constraint violation.
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				response.WriteError(w, http.StatusConflict, "account_exists", "Account already exists")
				return
			}
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to create account")
			return
		}

		// Fetch the created_at timestamp assigned by the DB.
		var rec AccountRecord
		err = db.QueryRowContext(r.Context(),
			`SELECT id, username, created_at FROM accounts WHERE id = ?`,
			req.ID,
		).Scan(&rec.ID, &rec.Username, &rec.CreatedAt)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to retrieve created account")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(rec)
	}
}

// ListAccounts handles GET /api/v1/accounts.
// Returns all registered accounts. Any authenticated token (admin or viewer) can list.
// SECURITY (SEC-01): Response never includes passwords or credential fields.
func ListAccounts(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(),
			`SELECT id, username, created_at FROM accounts ORDER BY created_at ASC`,
		)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to list accounts")
			return
		}
		defer rows.Close()

		accounts := []AccountRecord{}
		for rows.Next() {
			var rec AccountRecord
			if err := rows.Scan(&rec.ID, &rec.Username, &rec.CreatedAt); err != nil {
				response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to scan account row")
				return
			}
			accounts = append(accounts, rec)
		}
		if err := rows.Err(); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to iterate accounts")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"accounts": accounts})
	}
}

// GetAccount handles GET /api/v1/accounts/{accountID}.
// Enforces account isolation: token's account_id must match the URL parameter (ACCT-04).
func GetAccount(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")

		// Account isolation check (ACCT-04).
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil || claims.AccountID != accountID {
			response.WriteError(w, http.StatusForbidden, "account_mismatch",
				"Token is not authorized for this account")
			return
		}

		var rec AccountRecord
		err := db.QueryRowContext(r.Context(),
			`SELECT id, username, created_at FROM accounts WHERE id = ?`,
			accountID,
		).Scan(&rec.ID, &rec.Username, &rec.CreatedAt)
		if err == sql.ErrNoRows {
			response.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
			return
		}
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to get account")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(rec)
	}
}

// DeleteAccount handles DELETE /api/v1/accounts/{accountID}.
// Requires admin role (enforced by RequireAdmin middleware in router).
// Enforces account isolation: token's account_id must match the URL parameter (ACCT-04).
// Removes the account from DB. Browser sessions are cleaned up on next operation attempt.
func DeleteAccount(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := chi.URLParam(r, "accountID")

		// Account isolation check (ACCT-04).
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil || claims.AccountID != accountID {
			response.WriteError(w, http.StatusForbidden, "account_mismatch",
				"Token is not authorized for this account")
			return
		}

		result, err := db.ExecContext(r.Context(),
			`DELETE FROM accounts WHERE id = ?`,
			accountID,
		)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to delete account")
			return
		}

		n, err := result.RowsAffected()
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "db_error", "Failed to check delete result")
			return
		}
		if n == 0 {
			response.WriteError(w, http.StatusNotFound, "account_not_found", "Account not found")
			return
		}

		// TODO: call sm.CloseAccount(accountID) once SessionManager exposes that method (Phase 3).
		// sm.Close() closes ALL sessions; do not call it here.
		// The sm parameter is retained so the method signature is ready for Phase 3.
		_ = sm

		w.WriteHeader(http.StatusNoContent)
	}
}

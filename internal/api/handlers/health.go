// Package handlers provides HTTP request handlers for the dns-he-net-automation API.
package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/vnovakovic/dns-he-net-automation/internal/browser"
)

// HealthResponse is the JSON response body for GET /healthz.
type HealthResponse struct {
	Status string            `json:"status"` // "ok" or "degraded"
	Checks map[string]string `json:"checks"`
}

// HealthHandler returns a handler for GET /healthz.
// It checks SQLite via db.PingContext, browser via launcher.IsConnected(),
// and Vault connectivity via vaultHealthFn (VAULT-04).
// OPS-01: returns 200 {"status": "ok", "checks": {...}} or 503 {"status": "degraded", ...}.
//
// vaultHealthFn returns:
//   - "ok" when Vault is reachable and healthy
//   - "degraded: <reason>" when Vault is unreachable or sealed
//   - "disabled" when running without Vault (EnvProvider mode)
//
// SECURITY (SEC-01): The error string from db.PingContext may appear in the health response.
// This is acceptable for an internal health endpoint — it does not expose credentials or
// HE.net account data. The endpoint is unauthenticated (internal observability only).
func HealthHandler(db *sql.DB, launcher *browser.Launcher, vaultHealthFn func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		checks := make(map[string]string)
		status := "ok"

		// SQLite check: PingContext sends a lightweight query to verify the connection.
		if err := db.PingContext(r.Context()); err != nil {
			checks["sqlite"] = "error: " + err.Error()
			status = "degraded"
		} else {
			checks["sqlite"] = "ok"
		}

		// Browser launcher connectivity check.
		// launcher.IsConnected() returns true if the Playwright process is running and reachable.
		if launcher != nil && launcher.IsConnected() {
			checks["browser"] = "ok"
		} else {
			checks["browser"] = "not connected"
			status = "degraded"
		}

		// Vault connectivity status (VAULT-04).
		// vaultHealthFn is a closure injected at startup from main.go.
		vaultStatus := vaultHealthFn()
		checks["vault"] = vaultStatus
		if vaultStatus != "ok" && vaultStatus != "disabled" {
			status = "degraded"
		}

		// OPS-02: Log health check result with request_id for traceability.
		slog.InfoContext(r.Context(), "health check",
			"request_id", chiMiddleware.GetReqID(r.Context()),
			"status", status,
		)

		httpStatus := http.StatusOK
		if status == "degraded" {
			httpStatus = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(HealthResponse{Status: status, Checks: checks})
	}
}

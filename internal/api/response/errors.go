// Package response provides shared HTTP response helpers for the API layer.
package response

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the standard JSON error shape for all API error responses.
// All error responses use {"error": "...", "code": "SNAKE_CASE"} (API-04).
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// WriteError writes a JSON error response with the given HTTP status, machine-readable
// code (SNAKE_CASE), and human-readable message. Sets Content-Type: application/json.
// Call WriteError before writing any body bytes to avoid double-header writes.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: message, Code: code})
}

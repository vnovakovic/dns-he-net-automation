// Package audit provides structured audit logging for DNS mutation operations.
// Every create, update, delete, and sync operation on zones and records is
// recorded in the audit_log table (OBS-02) for post-mortem analysis.
package audit

import (
	"context"
	"database/sql"
)

// Entry holds all fields required for a single audit log row.
// Action must be one of: "create", "update", "delete", "sync".
// Result must be one of: "success", "failure".
// Resource uses the form "zone:<id>" or "record:<id>".
// ErrorMsg is empty on success and contains the error message on failure.
type Entry struct {
	TokenID   string
	AccountID string
	Action    string // "create" | "update" | "delete" | "sync"
	Resource  string // "record:<id>" | "zone:<id>"
	Result    string // "success" | "failure"
	ErrorMsg  string // empty string on success
}

// Write inserts one audit log row into the audit_log table.
// The error_msg column is nullable: when e.ErrorMsg is empty, NULL is stored;
// when non-empty, the message string is stored.
// Returns the error from db.ExecContext — callers must never silently discard it.
func Write(ctx context.Context, db *sql.DB, e Entry) error {
	var errMsg any
	if e.ErrorMsg != "" {
		errMsg = e.ErrorMsg
	}

	_, err := db.ExecContext(ctx,
		`INSERT INTO audit_log (token_id, account_id, action, resource, result, error_msg)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.TokenID, e.AccountID, e.Action, e.Resource, e.Result, errMsg,
	)
	return err
}

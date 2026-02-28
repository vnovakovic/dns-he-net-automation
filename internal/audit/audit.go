// Package audit provides structured audit logging for DNS mutation operations.
// Every create, update, delete, and sync operation on zones and records is
// recorded in the audit_log table (OBS-02) for post-mortem analysis.
package audit

import (
	"context"
	"database/sql"
	"time"
)

// Entry holds all fields required for a single audit log row.
// Action must be one of: "create", "update", "delete", "sync".
// Result must be one of: "success", "failure".
// Resource uses the form "zone:<id>" or "record:<id>".
// ErrorMsg is empty on success and contains the error message on failure.
//
// WHY ID and CreatedAt added (Rule 2 — missing critical functionality):
//   The audit_log table has id (INTEGER PK) and created_at (DATETIME) columns.
//   The admin UI audit log page needs both: id for row identity and created_at
//   for the timestamp column. Without these fields, List() cannot scan the rows
//   and AuditPage cannot display the time column.
//   Write() only inserts rows (doesn't need id/created_at from the struct —
//   the DB assigns them), so existing Write() callers are unaffected.
type Entry struct {
	ID        int64
	CreatedAt time.Time
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

// List returns audit log entries ordered by created_at DESC for paginated display.
// Used by the admin UI audit log page (handleAuditPage in router.go).
//
// WHY context.Background() (not ctx parameter):
//   db.QueryContext requires a context. Using context.Background() is consistent
//   with the pattern used in other read-only DB helpers in this project.
//   Callers that need request-scoped cancellation should pass their own context —
//   this function signature uses context.Background() as the default for the
//   admin UI path where a request context is not available to the package.
//
// WHY nullable errMsg scanned as interface{} (not *string):
//   The error_msg column is nullable (TEXT). Scanning into *string requires the
//   address of a string pointer. Using interface{} (any) and type-asserting to
//   string after the scan is the idiomatic pattern for nullable TEXT in SQLite
//   with modernc.org/sqlite (pure-Go driver). A nil interface{} means NULL;
//   a string interface{} holds the message. (Same pattern used in Write().)
func List(db *sql.DB, limit, offset int) ([]Entry, error) {
	rows, err := db.QueryContext(context.Background(),
		`SELECT id, created_at, token_id, account_id, action, resource, result, error_msg
		 FROM audit_log ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var errMsg interface{}
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.TokenID, &e.AccountID, &e.Action, &e.Resource, &e.Result, &errMsg); err != nil {
			return nil, err
		}
		if errMsg != nil {
			if s, ok := errMsg.(string); ok {
				e.ErrorMsg = s
			}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Count returns the total number of audit log entries for pagination calculations.
//
// WHY db.QueryRowContext (not db.QueryContext):
//   COUNT(*) always returns exactly one row. QueryRowContext is the correct
//   sql.DB method for single-row scalar queries — it avoids the rows.Next()
//   loop and rows.Close() bookkeeping required by QueryContext.
//
// NOTE: db.QueryContextContext does NOT exist in database/sql. Only QueryContext
//   and QueryRowContext exist. Using the wrong method name causes a compile error.
func Count(db *sql.DB) (int, error) {
	var count int
	err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_log`).Scan(&count)
	return count, err
}

// Package reconcile implements the DNS record diff and apply engine.
// The package name is intentionally "reconcile" (not "sync") to avoid
// collision with the Go stdlib "sync" package.
package reconcile

import (
	"context"

	"github.com/vnovakovic/dns-he-net-automation/internal/model"
)

// RecordKey is the composite key used to match current and desired records.
// For most record types the key is (Type, Name, Content), which allows
// multiple A records for the same name with different IP addresses to be
// tracked independently (round-robin / multi-value support).
// For SRV records Priority, Weight, and Port are also included because two
// SRV records can share the same name and content target while differing
// only in those fields.
type RecordKey struct {
	Type    model.RecordType
	Name    string
	Content string
	// SRV disambiguation fields — zero value for non-SRV types.
	Priority int
	Weight   int
	Port     int
}

// SyncPlan describes the set of operations needed to reconcile current DNS
// state with the desired state.
type SyncPlan struct {
	Add    []model.Record
	Update []model.Record
	Delete []model.Record
}

// SyncResult records the outcome of a single apply operation.
type SyncResult struct {
	Op       string       // "add" | "update" | "delete"
	Record   model.Record
	Status   string // "ok" | "error"
	ErrorMsg string // non-empty when Status == "error"
}

// SyncResponse is the top-level response envelope returned by the sync HTTP
// handler (Plan 05). It is defined here so the reconcile package owns the
// full sync contract.
type SyncResponse struct {
	DryRun  bool        `json:"dry_run"`
	Plan    SyncPlan    `json:"plan"`
	Results []SyncResult `json:"results"`
}

// keyOf builds the composite RecordKey for a record.
// SRV records extend the key with Priority, Weight, and Port so that two SRV
// entries with the same name can be distinguished without examining Content.
func keyOf(r model.Record) RecordKey {
	k := RecordKey{
		Type:    r.Type,
		Name:    r.Name,
		Content: r.Content,
	}
	if r.Type == model.RecordTypeSRV {
		k.Priority = r.Priority
		k.Weight = r.Weight
		k.Port = r.Port
	}
	return k
}

// recordsEqual returns true when two records with the same key also have
// identical non-key fields. Type, Name, Content, and ID are excluded:
// Type/Name/Content are already captured in the key; ID differs between
// current (server-assigned) and desired (empty or caller-supplied) records.
func recordsEqual(a, b model.Record) bool {
	return a.TTL == b.TTL &&
		a.Priority == b.Priority &&
		a.Weight == b.Weight &&
		a.Port == b.Port &&
		a.Target == b.Target &&
		a.Dynamic == b.Dynamic
}

// DiffRecords computes the minimal set of Add, Update, and Delete operations
// needed to make current match desired.
//
// Matching is performed by keyOf() — see RecordKey for the key composition
// rules. When a desired record matches a current record:
//   - If all non-key fields are equal → no operation (idempotent)
//   - Otherwise → Update, with the current record's ID carried into the
//     desired record so the browser caller knows which record to edit
//
// Any current record that has no matching desired record is marked for
// deletion.
func DiffRecords(current, desired []model.Record) SyncPlan {
	plan := SyncPlan{
		Add:    []model.Record{},
		Update: []model.Record{},
		Delete: []model.Record{},
	}

	// Build index of current records by key.
	currentMap := make(map[RecordKey]model.Record, len(current))
	for _, r := range current {
		currentMap[keyOf(r)] = r
	}

	// Track which current keys have been matched (identical or update) so
	// they are not also emitted as deletes.
	matched := make(map[RecordKey]bool, len(current))

	for _, d := range desired {
		k := keyOf(d)
		cur, exists := currentMap[k]
		if !exists {
			// No current record with this key — must be created.
			plan.Add = append(plan.Add, d)
			continue
		}

		// Mark as matched regardless of whether the fields changed.
		matched[k] = true

		if recordsEqual(cur, d) {
			// Identical — nothing to do (idempotent).
			continue
		}

		// Fields differ — update. Carry the current record's ID so the
		// browser knows which record to PUT/edit.
		d.ID = cur.ID
		plan.Update = append(plan.Update, d)
	}

	// Any current record not matched by a desired record must be deleted.
	for _, r := range current {
		if !matched[keyOf(r)] {
			plan.Delete = append(plan.Delete, r)
		}
	}

	return plan
}

// opResult is a small helper that constructs a SyncResult from an operation
// name, record, and error returned by a browser function.
func opResult(op string, r model.Record, err error) SyncResult {
	if err != nil {
		return SyncResult{Op: op, Record: r, Status: "error", ErrorMsg: err.Error()}
	}
	return SyncResult{Op: op, Record: r, Status: "ok"}
}

// Apply executes a SyncPlan by calling the supplied browser functions.
//
// Execution order: deletes → updates → adds. This order is intentional:
// removing stale records before creating new ones avoids transient conflicts
// (e.g., a unique-constraint violation when replacing a CNAME with a new one
// of the same name).
//
// Apply NEVER short-circuits on error (SYNC-04 compliance). All items in all
// three slices are attempted regardless of earlier failures. Each outcome is
// recorded in the returned []SyncResult.
//
// An empty plan returns an empty (non-nil) slice so callers can safely range
// over the result without a nil check.
func Apply(
	ctx context.Context,
	zoneID string,
	plan SyncPlan,
	deleteFn func(context.Context, string, model.Record) error,
	updateFn func(context.Context, string, model.Record) error,
	createFn func(context.Context, string, model.Record) error,
) []SyncResult {
	results := make([]SyncResult, 0, len(plan.Delete)+len(plan.Update)+len(plan.Add))

	for _, r := range plan.Delete {
		results = append(results, opResult("delete", r, deleteFn(ctx, zoneID, r)))
	}
	for _, r := range plan.Update {
		results = append(results, opResult("update", r, updateFn(ctx, zoneID, r)))
	}
	for _, r := range plan.Add {
		results = append(results, opResult("add", r, createFn(ctx, zoneID, r)))
	}

	return results
}

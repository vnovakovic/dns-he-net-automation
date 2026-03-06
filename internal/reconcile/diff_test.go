package reconcile_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
	"github.com/vnovakovic/dns-he-net-automation/internal/reconcile"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func aRecord(id, name, content string, ttl int) model.Record {
	return model.Record{
		ID:      id,
		Type:    model.RecordTypeA,
		Name:    name,
		Content: content,
		TTL:     ttl,
	}
}

func srvRecord(id, name string, priority, weight, port int) model.Record {
	return model.Record{
		ID:       id,
		Type:     model.RecordTypeSRV,
		Name:     name,
		Priority: priority,
		Weight:   weight,
		Port:     port,
		Target:   "target.example.com",
		TTL:      300,
	}
}

// ---------------------------------------------------------------------------
// DiffRecords tests
// ---------------------------------------------------------------------------

func TestDiffRecords_EmptyBoth(t *testing.T) {
	plan := reconcile.DiffRecords(nil, nil)
	assert.Empty(t, plan.Add)
	assert.Empty(t, plan.Update)
	assert.Empty(t, plan.Delete)
}

func TestDiffRecords_AllNew(t *testing.T) {
	desired := []model.Record{aRecord("", "foo.example.com", "1.2.3.4", 300)}
	plan := reconcile.DiffRecords(nil, desired)
	require.Len(t, plan.Add, 1)
	assert.Empty(t, plan.Update)
	assert.Empty(t, plan.Delete)
	assert.Equal(t, "1.2.3.4", plan.Add[0].Content)
}

func TestDiffRecords_AllDelete(t *testing.T) {
	current := []model.Record{aRecord("id-1", "foo.example.com", "1.2.3.4", 300)}
	plan := reconcile.DiffRecords(current, nil)
	assert.Empty(t, plan.Add)
	assert.Empty(t, plan.Update)
	require.Len(t, plan.Delete, 1)
	assert.Equal(t, "id-1", plan.Delete[0].ID)
}

func TestDiffRecords_Identical(t *testing.T) {
	r := aRecord("id-1", "foo.example.com", "1.2.3.4", 300)
	plan := reconcile.DiffRecords([]model.Record{r}, []model.Record{r})
	assert.Empty(t, plan.Add)
	assert.Empty(t, plan.Update)
	assert.Empty(t, plan.Delete)
}

func TestDiffRecords_UpdateTTL(t *testing.T) {
	cur := aRecord("id-1", "foo.example.com", "1.2.3.4", 300)
	des := aRecord("", "foo.example.com", "1.2.3.4", 3600) // TTL changed
	plan := reconcile.DiffRecords([]model.Record{cur}, []model.Record{des})
	assert.Empty(t, plan.Add)
	assert.Empty(t, plan.Delete)
	require.Len(t, plan.Update, 1)
	// ID must be carried over from current so the browser call knows which record to edit
	assert.Equal(t, "id-1", plan.Update[0].ID)
	assert.Equal(t, 3600, plan.Update[0].TTL)
}

func TestDiffRecords_MultiValueSameName(t *testing.T) {
	// Two A records with same name but different IPs — both must be tracked by (Type,Name,Content) key
	cur1 := aRecord("id-1", "www.example.com", "1.2.3.4", 300)
	cur2 := aRecord("id-2", "www.example.com", "5.6.7.8", 300)
	// Desired keeps only the first IP
	des := aRecord("", "www.example.com", "1.2.3.4", 300)
	plan := reconcile.DiffRecords([]model.Record{cur1, cur2}, []model.Record{des})
	assert.Empty(t, plan.Add)
	assert.Empty(t, plan.Update)
	require.Len(t, plan.Delete, 1)
	assert.Equal(t, "id-2", plan.Delete[0].ID)
	assert.Equal(t, "5.6.7.8", plan.Delete[0].Content)
}

func TestDiffRecords_SRVDistinct(t *testing.T) {
	// Two SRV records with same name but different port — must be disambiguated by key
	cur1 := srvRecord("id-1", "_svc._tcp.example.com", 10, 10, 80)
	cur2 := srvRecord("id-2", "_svc._tcp.example.com", 5, 5, 443)
	// Desired keeps only port 80
	des := srvRecord("", "_svc._tcp.example.com", 10, 10, 80)
	plan := reconcile.DiffRecords([]model.Record{cur1, cur2}, []model.Record{des})
	assert.Empty(t, plan.Add)
	assert.Empty(t, plan.Update)
	require.Len(t, plan.Delete, 1)
	assert.Equal(t, "id-2", plan.Delete[0].ID)
	assert.Equal(t, 443, plan.Delete[0].Port)
}

func TestDiffRecords_MixedOps(t *testing.T) {
	// current: A 1.2.3.4 (TTL 300), A 5.6.7.8, AAAA ::1
	// desired: A 1.2.3.4 (TTL 3600, changed), A 9.9.9.9 (new), AAAA ::1 (same)
	cur := []model.Record{
		{ID: "id-1", Type: model.RecordTypeA, Name: "host.example.com", Content: "1.2.3.4", TTL: 300},
		{ID: "id-2", Type: model.RecordTypeA, Name: "host.example.com", Content: "5.6.7.8", TTL: 300},
		{ID: "id-3", Type: model.RecordTypeAAAA, Name: "host.example.com", Content: "::1", TTL: 300},
	}
	des := []model.Record{
		{Type: model.RecordTypeA, Name: "host.example.com", Content: "1.2.3.4", TTL: 3600},  // update TTL
		{Type: model.RecordTypeA, Name: "host.example.com", Content: "9.9.9.9", TTL: 300},   // add
		{Type: model.RecordTypeAAAA, Name: "host.example.com", Content: "::1", TTL: 300},    // same
	}
	plan := reconcile.DiffRecords(cur, des)

	require.Len(t, plan.Add, 1)
	assert.Equal(t, "9.9.9.9", plan.Add[0].Content)

	require.Len(t, plan.Update, 1)
	assert.Equal(t, "id-1", plan.Update[0].ID)
	assert.Equal(t, 3600, plan.Update[0].TTL)

	require.Len(t, plan.Delete, 1)
	assert.Equal(t, "id-2", plan.Delete[0].ID)
}

func TestDiffRecords_UpdateCarriesID(t *testing.T) {
	cur := aRecord("existing-id-42", "test.example.com", "10.0.0.1", 300)
	des := aRecord("", "test.example.com", "10.0.0.1", 86400) // only TTL differs
	plan := reconcile.DiffRecords([]model.Record{cur}, []model.Record{des})
	require.Len(t, plan.Update, 1)
	assert.Equal(t, "existing-id-42", plan.Update[0].ID,
		"Update record must carry the current record's ID for browser UpdateRecord call")
}

// ---------------------------------------------------------------------------
// Apply tests
// ---------------------------------------------------------------------------

func TestApply_DeleteBeforeAdd(t *testing.T) {
	var callOrder []string

	deleteFn := func(_ context.Context, _ string, r model.Record) error {
		callOrder = append(callOrder, "delete:"+r.Content)
		return nil
	}
	updateFn := func(_ context.Context, _ string, r model.Record) error {
		callOrder = append(callOrder, "update:"+r.Content)
		return nil
	}
	createFn := func(_ context.Context, _ string, r model.Record) error {
		callOrder = append(callOrder, "add:"+r.Content)
		return nil
	}

	plan := reconcile.SyncPlan{
		Delete: []model.Record{aRecord("id-1", "x.example.com", "1.1.1.1", 300)},
		Update: []model.Record{aRecord("id-2", "x.example.com", "2.2.2.2", 300)},
		Add:    []model.Record{aRecord("", "x.example.com", "3.3.3.3", 300)},
	}

	results := reconcile.Apply(context.Background(), "zone-1", plan, deleteFn, updateFn, createFn)
	require.Len(t, results, 3)

	// Verify order: delete → update → add
	require.Equal(t, "delete:1.1.1.1", callOrder[0])
	require.Equal(t, "update:2.2.2.2", callOrder[1])
	require.Equal(t, "add:3.3.3.3", callOrder[2])
}

func TestApply_PartialFailure(t *testing.T) {
	boom := errors.New("simulated failure")
	callCount := 0

	deleteFn := func(_ context.Context, _ string, _ model.Record) error {
		callCount++
		return boom // always fail
	}
	updateFn := func(_ context.Context, _ string, _ model.Record) error {
		callCount++
		return nil
	}
	createFn := func(_ context.Context, _ string, _ model.Record) error {
		callCount++
		return nil
	}

	plan := reconcile.SyncPlan{
		Delete: []model.Record{
			aRecord("id-1", "a.example.com", "1.1.1.1", 300),
			aRecord("id-2", "b.example.com", "2.2.2.2", 300),
		},
		Update: []model.Record{aRecord("id-3", "c.example.com", "3.3.3.3", 300)},
		Add:    []model.Record{aRecord("", "d.example.com", "4.4.4.4", 300)},
	}

	results := reconcile.Apply(context.Background(), "zone-1", plan, deleteFn, updateFn, createFn)

	// All 4 operations must be attempted despite delete failures (SYNC-04: never short-circuit)
	assert.Equal(t, 4, callCount, "all 4 fns must be called despite errors")
	require.Len(t, results, 4)

	// First two are deletes — must have error status
	assert.Equal(t, "error", results[0].Status)
	assert.NotEmpty(t, results[0].ErrorMsg)
	assert.Equal(t, "error", results[1].Status)

	// Update and add must succeed
	assert.Equal(t, "ok", results[2].Status)
	assert.Equal(t, "ok", results[3].Status)
}

func TestApply_EmptyPlan(t *testing.T) {
	noop := func(_ context.Context, _ string, _ model.Record) error { return nil }
	plan := reconcile.SyncPlan{}
	results := reconcile.Apply(context.Background(), "zone-1", plan, noop, noop, noop)
	// Must return empty slice (not nil) — callers can range over it safely
	require.NotNil(t, results)
	assert.Empty(t, results)
}

func TestApply_AllOps(t *testing.T) {
	var ops []string
	track := func(label string) func(context.Context, string, model.Record) error {
		return func(_ context.Context, _ string, _ model.Record) error {
			ops = append(ops, label)
			return nil
		}
	}

	plan := reconcile.SyncPlan{
		Delete: []model.Record{aRecord("id-1", "x.example.com", "1.1.1.1", 300)},
		Update: []model.Record{aRecord("id-2", "x.example.com", "2.2.2.2", 300)},
		Add:    []model.Record{aRecord("", "x.example.com", "3.3.3.3", 300)},
	}

	results := reconcile.Apply(context.Background(), "zone-1", plan, track("delete"), track("update"), track("add"))
	require.Len(t, results, 3)
	assert.Equal(t, []string{"delete", "update", "add"}, ops)

	// Verify op labels in results
	assert.Equal(t, "delete", results[0].Op)
	assert.Equal(t, "update", results[1].Op)
	assert.Equal(t, "add", results[2].Op)

	// All statuses ok
	for _, r := range results {
		assert.Equal(t, "ok", r.Status)
	}
}

package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakovic/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakovic/dns-he-net-automation/internal/api/response"
	"github.com/vnovakovic/dns-he-net-automation/internal/audit"
	"github.com/vnovakovic/dns-he-net-automation/internal/browser"
	"github.com/vnovakovic/dns-he-net-automation/internal/browser/pages"
	"github.com/vnovakovic/dns-he-net-automation/internal/metrics"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
	"github.com/vnovakovic/dns-he-net-automation/internal/reconcile"
	"github.com/vnovakovic/dns-he-net-automation/internal/resilience"
)

// syncHTTPResponse is the JSON envelope returned by the POST /sync endpoint.
// HTTP 200 is always returned — had_errors signals partial failure in the body.
type syncHTTPResponse struct {
	DryRun    bool                   `json:"dry_run"`
	Plan      reconcile.SyncPlan     `json:"plan"`
	Results   []reconcile.SyncResult `json:"results"`
	HadErrors bool                   `json:"had_errors"`
}

// SyncRecords handles POST /api/v1/zones/{zoneID}/sync.
//
// It computes the diff between the desired state (request body) and the live
// dns.he.net state, then either returns the dry-run plan or applies changes
// in delete → update → add order (SYNC-05).
//
// The reg parameter may be nil — all metric calls are nil-guarded.
// Requires admin role (registered via RequireAdmin middleware in router).
func SyncRecords(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry, reg *metrics.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		zoneID := chi.URLParam(r, "zoneID")
		dryRun := r.URL.Query().Get("dry_run") == "true"

		// Decode desired state from request body.
		// An empty array is valid: it means delete all managed records (full wipe).
		var desiredRecords []model.Record
		if err := json.NewDecoder(r.Body).Decode(&desiredRecords); err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_body", "Request body must be a JSON array of records")
			return
		}
		if desiredRecords == nil {
			desiredRecords = []model.Record{}
		}

		start := time.Now()

		// Step 1: Scrape live state from dns.he.net.
		var currentRecords []model.Record
		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "list_records", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)
					recs, err := zl.ListRecords(zoneID)
					if err != nil {
						return err
					}
					currentRecords = recs
					return nil
				})
			})
		})
		if err != nil {
			handleBrowserError(w, r, err)
			return
		}

		// Step 2: Compute diff.
		plan := reconcile.DiffRecords(currentRecords, desiredRecords)

		// Step 3: If dry-run, respond immediately without any browser mutations.
		if dryRun {
			slog.InfoContext(r.Context(), "sync dry-run complete",
				"account_id", claims.AccountID,
				"zone_id", zoneID,
				"add", len(plan.Add),
				"update", len(plan.Update),
				"delete", len(plan.Delete),
				"duration_ms", time.Since(start).Milliseconds(),
			)
			response.WriteJSON(w, http.StatusOK, syncHTTPResponse{
				DryRun:    true,
				Plan:      plan,
				Results:   []reconcile.SyncResult{},
				HadErrors: false,
			})
			return
		}

		// Step 4: Define operation closures for reconcile.Apply.

		// deleteFn navigates to the zone, looks up the zone name and record type,
		// then calls the deleteRecord JS function. Mirrors the DeleteRecord handler pattern.
		deleteFn := func(ctx context.Context, zID string, rec model.Record) error {
			return breakers.Execute(ctx, claims.AccountID, func() error {
				return resilience.WithRetry(ctx, func(ctx context.Context) error {
					return sm.WithAccount(ctx, claims.AccountID, "delete_record", func(page playwright.Page) error {
						zl := pages.NewZoneListPage(page)
						if err := zl.NavigateToZone(zID); err != nil {
							return err
						}
						zoneName, err := zl.GetZoneName(zID)
						if err != nil {
							return err
						}
						parsed, err := zl.ParseRecordRow(rec.ID)
						if err != nil {
							return err
						}
						rf := pages.NewRecordFormPage(page)
						return rf.DeleteRecord(rec.ID, zoneName, string(parsed.Type))
					})
				})
			})
		}

		// updateFn opens the edit form for the existing record ID and submits new field values.
		// Mirrors the UpdateRecord handler pattern.
		updateFn := func(ctx context.Context, zID string, rec model.Record) error {
			return breakers.Execute(ctx, claims.AccountID, func() error {
				return resilience.WithRetry(ctx, func(ctx context.Context) error {
					return sm.WithAccount(ctx, claims.AccountID, "update_record", func(page playwright.Page) error {
						zl := pages.NewZoneListPage(page)
						if err := zl.NavigateToZone(zID); err != nil {
							return err
						}
						rf := pages.NewRecordFormPage(page)
						if err := rf.EditExistingRecord(rec.ID); err != nil {
							return err
						}
						return rf.FillAndSubmit(rec)
					})
				})
			})
		}

		// createFn opens the new-record form for the given type and submits.
		// Mirrors the CreateRecord handler pattern.
		createFn := func(ctx context.Context, zID string, rec model.Record) error {
			return breakers.Execute(ctx, claims.AccountID, func() error {
				return resilience.WithRetry(ctx, func(ctx context.Context) error {
					return sm.WithAccount(ctx, claims.AccountID, "create_record", func(page playwright.Page) error {
						zl := pages.NewZoneListPage(page)
						if err := zl.NavigateToZone(zID); err != nil {
							return err
						}
						rf := pages.NewRecordFormPage(page)
						if err := rf.OpenNewRecordForm(string(rec.Type)); err != nil {
							return err
						}
						return rf.FillAndSubmit(rec)
					})
				})
			})
		}

		// Step 5: Apply changes — delete → update → add, no short-circuit (SYNC-04).
		results := reconcile.Apply(r.Context(), zoneID, plan, deleteFn, updateFn, createFn)

		// Step 6: Count errors and increment sync metrics.
		hadErrors := false
		for _, res := range results {
			if res.Status == "error" {
				hadErrors = true
				if reg != nil {
					reg.SyncOpsTotal.WithLabelValues(res.Op, "error").Inc()
				}
			} else {
				if reg != nil {
					reg.SyncOpsTotal.WithLabelValues(res.Op, "ok").Inc()
				}
			}
		}

		// Step 7: Write audit log — always, regardless of partial failure (OBS-02).
		auditResult := "success"
		if hadErrors {
			auditResult = "failure"
		}
		if err := audit.Write(r.Context(), db, audit.Entry{
			TokenID:   claims.ID,
			AccountID: claims.AccountID,
			Action:    "sync",
			Resource:  "zone:" + zoneID,
			Result:    auditResult,
		}); err != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "error", err)
		}

		slog.InfoContext(r.Context(), "sync apply complete",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"add", len(plan.Add),
			"update", len(plan.Update),
			"delete", len(plan.Delete),
			"had_errors", hadErrors,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		// Step 8: Respond with HTTP 200 always — had_errors signals partial failure.
		response.WriteJSON(w, http.StatusOK, syncHTTPResponse{
			DryRun:    false,
			Plan:      plan,
			Results:   results,
			HadErrors: hadErrors,
		})
	}
}

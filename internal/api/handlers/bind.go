package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakovic/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakovic/dns-he-net-automation/internal/api/response"
	"github.com/vnovakovic/dns-he-net-automation/internal/audit"
	"github.com/vnovakovic/dns-he-net-automation/internal/bindio"
	"github.com/vnovakovic/dns-he-net-automation/internal/browser"
	"github.com/vnovakovic/dns-he-net-automation/internal/browser/pages"
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
	"github.com/vnovakovic/dns-he-net-automation/internal/reconcile"
	"github.com/vnovakovic/dns-he-net-automation/internal/resilience"
)

// importHTTPResponse is the JSON envelope for POST /import.
//
// Response shape mirrors POST /sync (CONTEXT.md decision) — HTTP 200 always,
// had_errors signals partial failure in body. Key difference from sync: this
// response carries SkippedRecord entries for zone file records that could not
// be parsed or are unsupported types.
type importHTTPResponse struct {
	DryRun    bool                   `json:"dry_run"`
	Applied   []reconcile.SyncResult `json:"applied"`
	Skipped   []bindio.SkippedRecord `json:"skipped"`
	HadErrors bool                   `json:"had_errors"`
}

// ExportZone handles GET /api/v1/zones/{zoneID}/export.
//
// Scrapes live records from dns.he.net, converts to BIND zone file format
// using miekg/dns, and returns the zone file as text/plain with a
// Content-Disposition attachment header for browser download (BIND-01).
//
// WHY two steps (zone name + records) use one browser session:
//   Both GetZoneName (from zone list page) and ListRecords (from zone edit page)
//   require browser automation. Combining them in a single WithAccount call
//   avoids two separate queue acquisitions and browser navigations.
//   NavigateToZoneList is called first to populate the zone list before GetZoneName.
//
// Requires admin role (registered with RequireAdmin middleware in router).
func ExportZone(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		zoneID := chi.URLParam(r, "zoneID")

		// Scrape zone name and records in a single browser session.
		var zoneName string
		var records []model.Record

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "export_zone", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)

					// Step 1: Navigate to zone list so GetZoneName selector works.
					if err := zl.NavigateToZoneList(); err != nil {
						return err
					}
					name, err := zl.GetZoneName(zoneID)
					if err != nil {
						return fmt.Errorf("get zone name for export: %w", err)
					}
					zoneName = name

					// Step 2: Scrape live records from the zone edit page.
					recs, err := zl.ListRecords(zoneID)
					if err != nil {
						return fmt.Errorf("list records for export: %w", err)
					}
					records = recs
					return nil
				})
			})
		})
		if err != nil {
			handleBrowserError(w, r, err)
			return
		}

		// Convert to BIND zone file format using miekg/dns.
		zoneFile, err := bindio.ExportZone(records, zoneName)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "export_error", "Failed to generate zone file")
			return
		}

		// Return as downloadable text/plain file.
		// Content-Disposition attachment triggers browser download dialog (BIND-01).
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zone"`, zoneName))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(zoneFile))

		slog.InfoContext(r.Context(), "zone exported",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"zone_name", zoneName,
			"record_count", len(records),
		)
	}
}

// ImportZone handles POST /api/v1/zones/{zoneID}/import.
//
// Accepts a BIND zone file in the request body (Content-Type: text/plain or
// application/octet-stream), parses it with miekg/dns, and applies an
// additive-only sync (no deletes). (BIND-02, BIND-03)
//
// WHY additive-only (plan.Delete = nil):
//   Import is intended as a migration tool, not a full reconcile. Records
//   absent from the imported file are NOT deleted — they are kept as-is.
//   This prevents accidental data loss when importing a partial zone file.
//   Full replacement (all-or-nothing) is not in scope for Phase 6.
//   (CONTEXT.md decision: import is additive only, ?mode=replace deferred)
//
// Response shape mirrors POST /sync for API consistency (CONTEXT.md decision).
// Skipped records (unsupported types, SOA) are reported in the skipped[] array.
// Requires admin role (registered with RequireAdmin middleware in router).
func ImportZone(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		zoneID := chi.URLParam(r, "zoneID")
		dryRun := r.URL.Query().Get("dry_run") == "true"
		start := time.Now()

		// Read zone file body (max 1 MB — zone files are small; 1 MB is generous).
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "body_read_error", "Failed to read request body")
			return
		}
		if len(bodyBytes) == 0 {
			response.WriteError(w, http.StatusBadRequest, "empty_body", "Request body must be a BIND zone file")
			return
		}

		// Fetch zone name (needed as ZoneParser origin) and current live state
		// in a single browser session to minimise queue acquisitions.
		var zoneName string
		var currentRecords []model.Record

		err = breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "import_zone_read", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)

					// Navigate to zone list first so GetZoneName selector resolves.
					if err := zl.NavigateToZoneList(); err != nil {
						return err
					}
					name, err := zl.GetZoneName(zoneID)
					if err != nil {
						return fmt.Errorf("get zone name for import: %w", err)
					}
					zoneName = name

					// Scrape current live state for diff computation.
					recs, err := zl.ListRecords(zoneID)
					if err != nil {
						return fmt.Errorf("list current records for import: %w", err)
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

		// Parse BIND zone file — unsupported types go to skipped[], not errors.
		desiredFromFile, skipped, err := bindio.ParseZoneFile(string(bodyBytes), zoneName)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_zone_file", err.Error())
			return
		}

		// Compute diff using the existing reconcile engine.
		plan := reconcile.DiffRecords(currentRecords, desiredFromFile)

		// ADDITIVE ONLY: clear the Delete slice before Apply.
		// Import never removes records absent from the zone file.
		// This is the only intentional deviation from the standard sync pattern.
		// (CONTEXT.md decision: import is additive, not a full replacement)
		plan.Delete = nil

		// Dry-run: return the additive plan without touching dns.he.net.
		if dryRun {
			response.WriteJSON(w, http.StatusOK, importHTTPResponse{
				DryRun:    true,
				Applied:   []reconcile.SyncResult{},
				Skipped:   skipped,
				HadErrors: false,
			})
			return
		}

		// Define operation closures for reconcile.Apply.
		// deleteFn is a no-op guard — plan.Delete is nil so it should never be called,
		// but we guard defensively in case reconcile.Apply behaviour changes.
		deleteFn := func(ctx context.Context, zID string, rec model.Record) error {
			// plan.Delete is always nil for import — this closure should not be invoked.
			return nil
		}

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

		// Apply the additive plan (no deletes; only update + add).
		results := reconcile.Apply(r.Context(), zoneID, plan, deleteFn, updateFn, createFn)

		// Count errors across all apply results.
		hadErrors := false
		for _, res := range results {
			if res.Status == "error" {
				hadErrors = true
				break
			}
		}

		// Write audit log — always, regardless of partial failure (OBS-02).
		auditResult := "success"
		if hadErrors {
			auditResult = "failure"
		}
		if auditErr := audit.Write(r.Context(), db, audit.Entry{
			TokenID:   claims.ID,
			AccountID: claims.AccountID,
			Action:    "import",
			Resource:  "zone:" + zoneID,
			Result:    auditResult,
		}); auditErr != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
		}

		slog.InfoContext(r.Context(), "zone import complete",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"zone_name", zoneName,
			"added", len(plan.Add),
			"updated", len(plan.Update),
			"skipped", len(skipped),
			"had_errors", hadErrors,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		response.WriteJSON(w, http.StatusOK, importHTTPResponse{
			DryRun:    false,
			Applied:   results,
			Skipped:   skipped,
			HadErrors: hadErrors,
		})
	}
}

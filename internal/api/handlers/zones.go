// Package handlers provides HTTP request handlers for the dns-he-net-automation API.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"github.com/vnovakovic/dns-he-net-automation/internal/model"
	"github.com/vnovakovic/dns-he-net-automation/internal/resilience"
)

// ZoneResponse is the JSON representation of a DNS zone returned by the API.
// FetchedAt records when the scrape happened (API-05).
type ZoneResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AccountID string    `json:"account_id"`
	FetchedAt time.Time `json:"fetched_at"`
}

// createZoneRequest is the JSON body for POST /api/v1/zones.
type createZoneRequest struct {
	Name string `json:"name"`
}

// ListZones handles GET /api/v1/zones.
// Scrapes the live zone list for the authenticated account and returns all zones
// with their IDs, names, account_id, and the time the scrape occurred.
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func ListZones(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		slog.InfoContext(r.Context(), "list zones", "account_id", claims.AccountID)
		start := time.Now()

		var zones []model.Zone

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "list_zones", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)
					if err := zl.NavigateToZoneList(); err != nil {
						return err
					}
					list, err := zl.ListZones()
					if err != nil {
						return err
					}
					for i := range list {
						list[i].AccountID = claims.AccountID
					}
					zones = list
					return nil
				})
			})
		})

		if err != nil {
			switch {
			case errors.Is(err, browser.ErrQueueTimeout):
				response.WriteError(w, http.StatusTooManyRequests, "queue_timeout", "Operation queue timeout; retry later")
			case errors.Is(err, browser.ErrSessionUnhealthy):
				response.WriteError(w, http.StatusServiceUnavailable, "session_unhealthy", "Browser session unavailable; retry later")
			default:
				response.WriteError(w, http.StatusInternalServerError, "browser_error", "Browser operation failed")
			}
			return
		}

		slog.InfoContext(r.Context(), "list zones done",
			"account_id", claims.AccountID,
			"count", len(zones),
			"duration_ms", time.Since(start).Milliseconds(),
		)

		result := make([]ZoneResponse, 0, len(zones))
		for _, z := range zones {
			result = append(result, ZoneResponse{
				ID:        z.ID,
				Name:      z.Name,
				AccountID: z.AccountID,
				FetchedAt: start,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"zones": result})
	}
}

// CreateZone handles POST /api/v1/zones.
// Creates a new DNS zone on dns.he.net. Idempotent: if the zone already exists,
// returns 200 with the existing zone. If newly created, returns 201.
// Requires admin role (enforced by RequireAdmin middleware in router).
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func CreateZone(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createZoneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}

		// Validate: name must be non-empty and <= 253 characters.
		if req.Name == "" {
			response.WriteError(w, http.StatusBadRequest, "invalid_name", "Zone name must not be empty")
			return
		}
		if len(req.Name) > 253 {
			response.WriteError(w, http.StatusBadRequest, "invalid_name", "Zone name must not exceed 253 characters")
			return
		}

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()
		var result ZoneResponse
		var existed bool

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "create_zone", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)

					// Idempotency pre-check: if zone already exists, return it without creating.
					if err := zl.NavigateToZoneList(); err != nil {
						return err
					}
					existingID, lookupErr := zl.GetZoneID(req.Name)
					if lookupErr == nil && existingID != "" {
						// Zone already exists — populate result and signal 200.
						result = ZoneResponse{
							ID:        existingID,
							Name:      req.Name,
							AccountID: claims.AccountID,
							FetchedAt: time.Now(),
						}
						existed = true
						return nil
					}

					// Zone does not exist — create it.
					zoneID, err := zl.AddZone(req.Name)
					if err != nil {
						return err
					}
					result = ZoneResponse{
						ID:        zoneID,
						Name:      req.Name,
						AccountID: claims.AccountID,
						FetchedAt: time.Now(),
					}
					return nil
				})
			})
		})

		auditResult := "success"
		auditErrMsg := ""
		if err != nil {
			auditResult = "failure"
			auditErrMsg = err.Error()
		}
		if auditErr := audit.Write(r.Context(), db, audit.Entry{
			TokenID:   claims.ID,
			AccountID: claims.AccountID,
			Action:    "create",
			Resource:  "zone:" + result.ID,
			Result:    auditResult,
			ErrorMsg:  auditErrMsg,
		}); auditErr != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
		}

		if err != nil {
			switch {
			case errors.Is(err, browser.ErrQueueTimeout):
				response.WriteError(w, http.StatusTooManyRequests, "queue_timeout", "Operation queue timeout; retry later")
			case errors.Is(err, browser.ErrSessionUnhealthy):
				response.WriteError(w, http.StatusServiceUnavailable, "session_unhealthy", "Browser session unavailable; retry later")
			default:
				response.WriteError(w, http.StatusInternalServerError, "browser_error", "Browser operation failed")
			}
			return
		}

		slog.InfoContext(r.Context(), "create zone done",
			"account_id", claims.AccountID,
			"zone_id", result.ID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		w.Header().Set("Content-Type", "application/json")
		if existed {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(result)
	}
}

// DeleteZone handles DELETE /api/v1/zones/{zoneID}.
// Deletes a DNS zone on dns.he.net. Idempotent: if the zone does not exist, returns 204.
// Requires admin role (enforced by RequireAdmin middleware in router).
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func DeleteZone(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := chi.URLParam(r, "zoneID")
		if zoneID == "" {
			response.WriteError(w, http.StatusBadRequest, "missing_zone_id", "Zone ID is required")
			return
		}

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "delete_zone", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)

					// Idempotency: look up zone name. If not found, zone is already gone — success.
					if err := zl.NavigateToZoneList(); err != nil {
						return err
					}
					zoneName, nameErr := zl.GetZoneName(zoneID)
					if nameErr != nil {
						// Zone not found — already deleted; 204 is the correct idempotent response.
						return nil
					}

					return zl.DeleteZone(zoneID, zoneName)
				})
			})
		})

		auditResult := "success"
		auditErrMsg := ""
		if err != nil {
			auditResult = "failure"
			auditErrMsg = err.Error()
		}
		if auditErr := audit.Write(r.Context(), db, audit.Entry{
			TokenID:   claims.ID,
			AccountID: claims.AccountID,
			Action:    "delete",
			Resource:  "zone:" + zoneID,
			Result:    auditResult,
			ErrorMsg:  auditErrMsg,
		}); auditErr != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
		}

		if err != nil {
			switch {
			case errors.Is(err, browser.ErrQueueTimeout):
				response.WriteError(w, http.StatusTooManyRequests, "queue_timeout", "Operation queue timeout; retry later")
			case errors.Is(err, browser.ErrSessionUnhealthy):
				response.WriteError(w, http.StatusServiceUnavailable, "session_unhealthy", "Browser session unavailable; retry later")
			default:
				response.WriteError(w, http.StatusInternalServerError, "browser_error", "Browser operation failed")
			}
			return
		}

		slog.InfoContext(r.Context(), "delete zone done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		w.WriteHeader(http.StatusNoContent)
	}
}

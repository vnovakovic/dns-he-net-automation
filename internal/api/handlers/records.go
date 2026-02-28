package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakov/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/response"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/browser/pages"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
)

// errRecordNotFound is a sentinel error returned by GetRecord and UpdateRecord
// when the requested record ID does not exist in the zone.
var errRecordNotFound = errors.New("record not found")

// v1RecordTypes is the set of DNS record types supported in v1 of the API (COMPAT-02).
// Requests for other types are rejected with HTTP 422 before any browser operation.
var v1RecordTypes = map[model.RecordType]bool{
	model.RecordTypeA:     true,
	model.RecordTypeAAAA:  true,
	model.RecordTypeCNAME: true,
	model.RecordTypeMX:    true,
	model.RecordTypeTXT:   true,
	model.RecordTypeSRV:   true,
	model.RecordTypeCAA:   true,
	model.RecordTypeNS:    true,
}

// RecordResponse is the JSON representation of a DNS record returned by the API.
// FetchedAt records when the scrape occurred (API-05).
type RecordResponse struct {
	ID        string           `json:"id"`
	ZoneID    string           `json:"zone_id"`
	Type      model.RecordType `json:"type"`
	Name      string           `json:"name"`
	Content   string           `json:"content"`
	TTL       int              `json:"ttl"`
	Priority  int              `json:"priority"`
	Weight    int              `json:"weight"`
	Port      int              `json:"port"`
	Target    string           `json:"target"`
	Dynamic   bool             `json:"dynamic"`
	FetchedAt time.Time        `json:"fetched_at"`
}

// toRecordResponse converts a model.Record to a RecordResponse for JSON encoding.
func toRecordResponse(r model.Record, fetchedAt time.Time) RecordResponse {
	return RecordResponse{
		ID:        r.ID,
		ZoneID:    r.ZoneID,
		Type:      r.Type,
		Name:      r.Name,
		Content:   r.Content,
		TTL:       r.TTL,
		Priority:  r.Priority,
		Weight:    r.Weight,
		Port:      r.Port,
		Target:    r.Target,
		Dynamic:   r.Dynamic,
		FetchedAt: fetchedAt,
	}
}

// handleBrowserError writes a standardised JSON error response for browser-layer
// errors, mapping session-manager sentinel errors to the appropriate HTTP status codes.
func handleBrowserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, browser.ErrQueueTimeout):
		response.WriteError(w, http.StatusTooManyRequests, "queue_timeout", "Request queue full, retry later")
	case errors.Is(err, browser.ErrSessionUnhealthy):
		response.WriteError(w, http.StatusServiceUnavailable, "session_unhealthy", "Browser session unavailable")
	default:
		response.WriteError(w, http.StatusInternalServerError, "browser_error", "Browser operation failed")
	}
}

// validateRecordFields performs lightweight presence validation on the decoded record.
// Returns a non-empty message string when validation fails; returns "" on success.
func validateRecordFields(rec model.Record) string {
	if rec.Name == "" {
		return "Record name must not be empty"
	}
	switch rec.Type {
	case model.RecordTypeMX:
		if rec.Priority <= 0 {
			return "MX records require priority > 0"
		}
	case model.RecordTypeSRV:
		if rec.Priority <= 0 {
			return "SRV records require priority > 0"
		}
		if rec.Port <= 0 {
			return "SRV records require port > 0"
		}
		if rec.Target == "" {
			return "SRV records require a non-empty target"
		}
	}
	return ""
}

// ListRecords handles GET /api/v1/zones/{zoneID}/records.
// Scrapes all editable DNS records for the zone and returns them as a JSON array.
func ListRecords(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
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
		var records []model.Record

		err := sm.WithAccount(r.Context(), claims.AccountID, func(page playwright.Page) error {
			zl := pages.NewZoneListPage(page)
			list, err := zl.ListRecords(zoneID)
			if err != nil {
				return err
			}
			records = list
			return nil
		})

		if err != nil {
			handleBrowserError(w, err)
			return
		}

		slog.InfoContext(r.Context(), "list records done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"count", len(records),
			"duration_ms", time.Since(start).Milliseconds(),
		)

		result := make([]RecordResponse, 0, len(records))
		for _, rec := range records {
			result = append(result, toRecordResponse(rec, start))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"records": result})
	}
}

// GetRecord handles GET /api/v1/zones/{zoneID}/records/{recordID}.
// Returns a single DNS record by its dns.he.net internal ID.
func GetRecord(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := chi.URLParam(r, "zoneID")
		recordID := chi.URLParam(r, "recordID")

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()
		var found model.Record

		err := sm.WithAccount(r.Context(), claims.AccountID, func(page playwright.Page) error {
			zl := pages.NewZoneListPage(page)
			records, err := zl.ListRecords(zoneID)
			if err != nil {
				return err
			}
			for _, rec := range records {
				if rec.ID == recordID {
					found = rec
					return nil
				}
			}
			return errRecordNotFound
		})

		if err != nil {
			if errors.Is(err, errRecordNotFound) {
				response.WriteError(w, http.StatusNotFound, "record_not_found", "Record not found")
				return
			}
			handleBrowserError(w, err)
			return
		}

		slog.InfoContext(r.Context(), "get record done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"record_id", recordID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(toRecordResponse(found, start))
	}
}

// CreateRecord handles POST /api/v1/zones/{zoneID}/records.
// Creates a DNS record. Idempotent: returns 200 with the existing record when an
// identical record (same type+name+content or type-specific fields) already exists;
// returns 201 when a new record is created.
func CreateRecord(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rec model.Record
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}

		zoneID := chi.URLParam(r, "zoneID")
		rec.ZoneID = zoneID

		// Validate record type is in the v1 supported set.
		if !v1RecordTypes[rec.Type] {
			response.WriteError(w, http.StatusUnprocessableEntity, "unsupported_type",
				"Unsupported record type. Supported: A, AAAA, CNAME, MX, TXT, SRV, CAA, NS")
			return
		}

		// Basic field presence validation.
		if msg := validateRecordFields(rec); msg != "" {
			response.WriteError(w, http.StatusBadRequest, "invalid_record", msg)
			return
		}

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()
		var existed bool
		var result model.Record

		err := sm.WithAccount(r.Context(), claims.AccountID, func(page playwright.Page) error {
			zl := pages.NewZoneListPage(page)

			// Idempotency pre-check: find an existing identical record before creating.
			existingID, findErr := zl.FindRecord(zoneID, rec)
			if findErr == nil && existingID != "" {
				// Record already exists — fetch it and return 200.
				records, listErr := zl.ListRecords(zoneID)
				if listErr != nil {
					return listErr
				}
				for _, r := range records {
					if r.ID == existingID {
						result = r
						break
					}
				}
				existed = true
				return nil
			}

			// Record does not exist — open the form and create it.
			rf := pages.NewRecordFormPage(page)
			if err := rf.OpenNewRecordForm(string(rec.Type)); err != nil {
				return err
			}
			if err := rf.FillAndSubmit(rec); err != nil {
				return err
			}

			// After form submission the page reloads; find the new record ID.
			newID, err := zl.FindRecord(zoneID, rec)
			if err != nil {
				return err
			}
			if newID == "" {
				return errors.New("could not find newly created record after submission")
			}
			rec.ID = newID
			result = rec
			return nil
		})

		if err != nil {
			handleBrowserError(w, err)
			return
		}

		slog.InfoContext(r.Context(), "create record done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"type", string(rec.Type),
			"name", rec.Name,
			"existed", existed,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		w.Header().Set("Content-Type", "application/json")
		if existed {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(toRecordResponse(result, start))
	}
}

// UpdateRecord handles PUT /api/v1/zones/{zoneID}/records/{recordID}.
// Updates an existing DNS record's fields. Returns 404 if the record does not exist.
func UpdateRecord(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := chi.URLParam(r, "zoneID")
		recordID := chi.URLParam(r, "recordID")

		var rec model.Record
		if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}
		rec.ID = recordID
		rec.ZoneID = zoneID

		// Validate record type is in the v1 supported set.
		if !v1RecordTypes[rec.Type] {
			response.WriteError(w, http.StatusUnprocessableEntity, "unsupported_type",
				"Unsupported record type. Supported: A, AAAA, CNAME, MX, TXT, SRV, CAA, NS")
			return
		}

		// Basic field presence validation.
		if msg := validateRecordFields(rec); msg != "" {
			response.WriteError(w, http.StatusBadRequest, "invalid_record", msg)
			return
		}

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()
		var updated model.Record

		err := sm.WithAccount(r.Context(), claims.AccountID, func(page playwright.Page) error {
			zl := pages.NewZoneListPage(page)

			// Navigate and verify the record exists before attempting edit.
			if err := zl.NavigateToZone(zoneID); err != nil {
				return err
			}

			rows, err := zl.GetRecordRows()
			if err != nil {
				return err
			}
			found := false
			for _, row := range rows {
				if row.ID == recordID {
					found = true
					break
				}
			}
			if !found {
				return errRecordNotFound
			}

			rf := pages.NewRecordFormPage(page)
			if err := rf.EditExistingRecord(recordID); err != nil {
				return err
			}
			if err := rf.FillAndSubmit(rec); err != nil {
				return err
			}

			// Parse the updated record from the refreshed page.
			parsedRec, parseErr := zl.ParseRecordRow(recordID)
			if parseErr != nil {
				return parseErr
			}
			parsedRec.ZoneID = zoneID
			updated = *parsedRec
			return nil
		})

		if err != nil {
			if errors.Is(err, errRecordNotFound) {
				response.WriteError(w, http.StatusNotFound, "record_not_found", "Record not found")
				return
			}
			handleBrowserError(w, err)
			return
		}

		slog.InfoContext(r.Context(), "update record done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"record_id", recordID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(toRecordResponse(updated, start))
	}
}

// DeleteRecord handles DELETE /api/v1/zones/{zoneID}/records/{recordID}.
// Idempotent: always returns 204, whether or not the record existed.
func DeleteRecord(db *sql.DB, sm *browser.SessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := chi.URLParam(r, "zoneID")
		recordID := chi.URLParam(r, "recordID")

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()

		err := sm.WithAccount(r.Context(), claims.AccountID, func(page playwright.Page) error {
			zl := pages.NewZoneListPage(page)

			if err := zl.NavigateToZone(zoneID); err != nil {
				return err
			}

			// Idempotency: if record is not present, return success immediately.
			rows, err := zl.GetRecordRows()
			if err != nil {
				return err
			}
			found := false
			for _, row := range rows {
				if row.ID == recordID {
					found = true
					break
				}
			}
			if !found {
				// Record already gone — 204 is correct per REC-08.
				return nil
			}

			// Look up zone name and record type required by DeleteRecord JS call.
			zoneName, err := zl.GetZoneName(zoneID)
			if err != nil {
				return err
			}

			rec, parseErr := zl.ParseRecordRow(recordID)
			if parseErr != nil {
				return parseErr
			}

			rf := pages.NewRecordFormPage(page)
			return rf.DeleteRecord(recordID, zoneName, string(rec.Type))
		})

		if err != nil {
			handleBrowserError(w, err)
			return
		}

		slog.InfoContext(r.Context(), "delete record done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"record_id", recordID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		w.WriteHeader(http.StatusNoContent)
	}
}

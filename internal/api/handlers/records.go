package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	playwright "github.com/playwright-community/playwright-go"
	"github.com/vnovakov/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/response"
	"github.com/vnovakov/dns-he-net-automation/internal/api/validate"
	"github.com/vnovakov/dns-he-net-automation/internal/audit"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/browser/pages"
	"github.com/vnovakov/dns-he-net-automation/internal/model"
	"github.com/vnovakov/dns-he-net-automation/internal/resilience"
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
	// DDNSKey is returned only when a DDNS key was set or auto-generated during this request.
	// omitempty ensures the field is absent for non-dynamic records and idempotent calls
	// where the caller provided no key (avoiding silent key rotation of existing DDNS clients).
	DDNSKey string `json:"ddns_key,omitempty"`
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

// generateDDNSKey produces a 24-char hex string from 12 crypto/rand bytes.
// WHY crypto/rand: DDNS keys act as passwords; math/rand is not safe for credentials.
func generateDDNSKey() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate DDNS key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// updateDDNSIP pushes a new IP to HE.net's dynamic DNS update endpoint.
// Returns the confirmed IP string on success ("good" or "nochg" response).
//
// WHY a plain HTTP GET (not browser automation):
//   dyn.dns.he.net is a simple REST endpoint — no session, no JavaScript,
//   no CAPTCHA. Using a net/http request is ~100x faster than a Playwright
//   page navigation and does not consume a browser session slot.
//
// WHY called after SetDDNSKey, not before:
//   The DDNS key must exist on HE.net before the update request can succeed.
//   SetDDNSKey via browser creates/sets the key; this function uses it immediately.
//
// DDNS response codes (dyndns2 protocol, also used by HE.net):
//
//	"good <ip>"  — update succeeded, IP is now <ip>
//	"nochg <ip>" — no change needed, IP was already <ip>
//	"badauth"    — wrong hostname/password combination
//	"nohost"     — hostname not found in account
//	"abuse"      — account flagged for abuse
//	"dnserr"     — server-side DNS error
func updateDDNSIP(ctx context.Context, hostname, ddnsKey, myIP string) (string, error) {
	u := "https://dyn.dns.he.net/nic/update?hostname=" + url.QueryEscape(hostname) +
		"&password=" + url.QueryEscape(ddnsKey) +
		"&myip=" + url.QueryEscape(myIP)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("build DDNS update request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("DDNS update request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	line := strings.TrimSpace(string(body))

	if strings.HasPrefix(line, "good ") {
		return strings.TrimPrefix(line, "good "), nil
	}
	if strings.HasPrefix(line, "nochg ") {
		return strings.TrimPrefix(line, "nochg "), nil
	}
	return "", fmt.Errorf("DDNS update rejected: %q", line)
}

// createRecordRequest extends model.Record with an optional ddns_key.
// ddns_key is not a DNS attribute — it is HE.net's DDNS update credential.
// Kept separate from model.Record to avoid polluting the record model with auth data.
type createRecordRequest struct {
	model.Record
	DDNSKey string `json:"ddns_key"`
}

// handleBrowserError writes a standardised JSON error response for browser-layer
// errors, mapping session-manager sentinel errors to the appropriate HTTP status codes.
// WHY r is passed here: the original implementation omitted the request context, so browser
// errors were silently swallowed — no log entry was written and the only signal was the
// `browser_error` JSON body. Adding slog.ErrorContext here ensures every browser failure
// is logged with the request ID for correlation with Playwright traces.
func handleBrowserError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "browser operation failed", "error", err)
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
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func ListRecords(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
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

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "list_records", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)
					list, err := zl.ListRecords(zoneID)
					if err != nil {
						return err
					}
					records = list
					return nil
				})
			})
		})

		if err != nil {
			handleBrowserError(w, r, err)
			return
		}

		// Apply query parameter filters (API-06).
		if filterType := r.URL.Query().Get("type"); filterType != "" {
			ft := model.RecordType(strings.ToUpper(strings.TrimSpace(filterType)))
			filtered := records[:0]
			for _, rec := range records {
				if rec.Type == ft {
					filtered = append(filtered, rec)
				}
			}
			records = filtered
		}

		if filterName := r.URL.Query().Get("name"); filterName != "" {
			fn := strings.ToLower(strings.TrimSpace(filterName))
			filtered := records[:0]
			for _, rec := range records {
				if strings.EqualFold(rec.Name, fn) {
					filtered = append(filtered, rec)
				}
			}
			records = filtered
		}

		slog.InfoContext(r.Context(), "list records done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"count", len(records),
			"filter_type", r.URL.Query().Get("type"),
			"filter_name", r.URL.Query().Get("name"),
			"duration_ms", time.Since(start).Milliseconds(),
		)

		result := make([]RecordResponse, 0, len(records))
		for _, rec := range records {
			result = append(result, toRecordResponse(rec, start))
		}

		response.WriteJSON(w, http.StatusOK, map[string]any{"records": result})
	}
}

// GetRecord handles GET /api/v1/zones/{zoneID}/records/{recordID}.
// Returns a single DNS record by its dns.he.net internal ID.
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func GetRecord(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
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

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "find_record", func(page playwright.Page) error {
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
			})
		})

		if err != nil {
			if errors.Is(err, errRecordNotFound) {
				response.WriteError(w, http.StatusNotFound, "record_not_found", "Record not found")
				return
			}
			handleBrowserError(w, r, err)
			return
		}

		slog.InfoContext(r.Context(), "get record done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"record_id", recordID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		response.WriteJSON(w, http.StatusOK, toRecordResponse(found, start))
	}
}

// CreateRecord handles POST /api/v1/zones/{zoneID}/records.
// Creates a DNS record. Idempotent: returns 200 with the existing record when an
// identical record (same type+name+content or type-specific fields) already exists;
// returns 201 when a new record is created.
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func CreateRecord(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRecordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.WriteError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
			return
		}

		zoneID := chi.URLParam(r, "zoneID")
		rec := req.Record
		rec.ZoneID = zoneID
		// ddnsKey is declared here so the closure can mutate it (auto-generate path).
		ddnsKey := strings.TrimSpace(req.DDNSKey)

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

		// Full field validation — enforces TTL allowlist, IP format, type-specific
		// constraints. Returns 422 before any browser operation (REC-09).
		if err := validate.ValidateRecord(rec); err != nil {
			response.WriteError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
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

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "create_record", func(page playwright.Page) error {
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
						// Only set DDNS key for existing dynamic records when caller explicitly
						// provides one. Auto-generating would silently rotate the key and break
						// existing DDNS clients that already use the old key.
						if rec.Dynamic && ddnsKey != "" {
							rf2 := pages.NewRecordFormPage(page)
							if err := rf2.SetDDNSKey(existingID, ddnsKey); err != nil {
								return err
							}
						}
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
					// WHY scrape the actual record for dynamic types:
					//   HE.net ignores submitted content for dynamic A/AAAA and sets it to the
					//   requester's current IP. Return the real stored content so the caller
					//   knows what HE.net actually recorded.
					if rec.Dynamic {
						actualRec, parseErr := zl.ParseRecordRow(newID)
						if parseErr != nil {
							return parseErr
						}
						actualRec.ZoneID = zoneID
						result = *actualRec
					} else {
						result = rec
					}

					// Set DDNS key for new dynamic records. Page is still on the zone list
					// after FillAndSubmit + FindRecord — both leave the page there.
					// WHY auto-generate when no key provided: a dynamic record without a key
					// is non-functional; caller must know the key to use dyn.dns.he.net updates.
					if rec.Dynamic {
						if ddnsKey == "" {
							var genErr error
							ddnsKey, genErr = generateDDNSKey()
							if genErr != nil {
								return genErr
							}
						}
						rf2 := pages.NewRecordFormPage(page)
						if err := rf2.SetDDNSKey(newID, ddnsKey); err != nil {
							return err
						}
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
			Resource:  "record:" + result.ID,
			Result:    auditResult,
			ErrorMsg:  auditErrMsg,
		}); auditErr != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
		}

		if err != nil {
			handleBrowserError(w, r, err)
			return
		}

		// Auto-update DDNS IP when caller provided content for a dynamic A/AAAA record.
		// WHY after breakers.Execute (not inside sm.WithAccount):
		//   updateDDNSIP is a plain HTTP call — it does not need the browser session.
		//   Releasing the session first means the slot is available for other operations
		//   while the DDNS HTTP request is in flight.
		//
		// WHY only A/AAAA: DDNS updates via dyn.dns.he.net only apply to address records.
		//   TXT/AFSDB can be marked dynamic but do not support the dyn.dns.he.net protocol.
		//
		// WHY ddnsKey != "": for the "existed" path with no key provided, ddnsKey is empty
		//   and we have no credential to authenticate the DDNS update — skip silently.
		//
		// WHY soft failure (Warn, not error): the record WAS created and the DDNS key WAS set.
		//   The response includes ddns_key so the caller can push the IP manually if needed.
		if ddnsKey != "" &&
			rec.Dynamic &&
			(rec.Type == model.RecordTypeA || rec.Type == model.RecordTypeAAAA) &&
			rec.Content != "" {
			updatedIP, ddnsErr := updateDDNSIP(r.Context(), rec.Name, ddnsKey, rec.Content)
			if ddnsErr != nil {
				slog.WarnContext(r.Context(), "DDNS IP update failed after record create",
					"hostname", rec.Name,
					"requested_ip", rec.Content,
					"error", ddnsErr,
				)
			} else {
				result.Content = updatedIP
			}
		}

		slog.InfoContext(r.Context(), "create record done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"type", string(rec.Type),
			"name", rec.Name,
			"existed", existed,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		resp := toRecordResponse(result, start)
		resp.DDNSKey = ddnsKey // empty for existed+no-key; omitempty ensures absent in JSON
		if existed {
			response.WriteJSON(w, http.StatusOK, resp)
		} else {
			response.WriteJSON(w, http.StatusCreated, resp)
		}
	}
}

// UpdateRecord handles PUT /api/v1/zones/{zoneID}/records/{recordID}.
// Updates an existing DNS record's fields. Returns 404 if the record does not exist.
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func UpdateRecord(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
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

		// Full field validation — enforces TTL allowlist, IP format, type-specific
		// constraints. Returns 422 before any browser operation (REC-09).
		if err := validate.ValidateRecord(rec); err != nil {
			response.WriteError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
			return
		}

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()
		var updated model.Record

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "update_record", func(page playwright.Page) error {
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
			Action:    "update",
			Resource:  "record:" + recordID,
			Result:    auditResult,
			ErrorMsg:  auditErrMsg,
		}); auditErr != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
		}

		if err != nil {
			if errors.Is(err, errRecordNotFound) {
				response.WriteError(w, http.StatusNotFound, "record_not_found", "Record not found")
				return
			}
			handleBrowserError(w, r, err)
			return
		}

		slog.InfoContext(r.Context(), "update record done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"record_id", recordID,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		response.WriteJSON(w, http.StatusOK, toRecordResponse(updated, start))
	}
}

// DeleteRecordByName handles DELETE /api/v1/zones/{zoneID}/records?name=...
//
// Finds all records whose name matches ?name= (case-insensitive), optionally filtered
// by ?type=, then deletes each one within a single browser session.
//
// WHY a separate handler instead of a client-side GET-then-DELETE loop:
//   Each browser session acquire + zone navigation costs ~5-10s. Batching all
//   name-matched deletes inside one sm.WithAccount call amortises that cost and
//   avoids races where a second caller could observe an intermediate
//   partially-deleted state.
//
// WHY ?name= is a query param (not a path segment or body):
//   DELETE semantics: the resource is identified in the URL/query. A query param
//   makes the intent explicit and auditable in server access logs without
//   requiring a request body.
//
// Idempotent: returns 204 when no matching records exist.
func DeleteRecordByName(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := chi.URLParam(r, "zoneID")

		filterName := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
		if filterName == "" {
			response.WriteError(w, http.StatusBadRequest, "missing_name", "Query parameter ?name= is required")
			return
		}
		// ?type= is required. Behaviour:
		//   absent         → 400; caller must be explicit about scope
		//   ?type= (empty) → 400; almost always a caller bug (un-interpolated variable)
		//   ?type=ANY      → delete all types with the given name
		//   ?type=A etc.   → delete only records of that specific type
		//
		// WHY ?type= is required (not optional):
		//   Omitting it previously meant "delete all types", which was too easy to
		//   trigger by accident. Requiring an explicit ?type=ANY forces the caller to
		//   acknowledge the mass-delete scope, preventing silent data loss.
		if !r.URL.Query().Has("type") {
			response.WriteError(w, http.StatusBadRequest, "missing_type",
				"Query parameter ?type= is required; use ?type=ANY to delete all types, or specify a type (A, AAAA, CNAME, MX, TXT, SRV, CAA, NS)")
			return
		}
		rawType := strings.TrimSpace(r.URL.Query().Get("type"))
		if rawType == "" {
			response.WriteError(w, http.StatusBadRequest, "invalid_type",
				"?type= must not be empty; use ?type=ANY to delete all types, or specify a type (A, AAAA, CNAME, MX, TXT, SRV, CAA, NS)")
			return
		}
		var filterType model.RecordType // empty = all types
		if upper := strings.ToUpper(rawType); upper != "ANY" {
			filterType = model.RecordType(upper)
		}
		// upper == "ANY" → filterType stays "" → all types deleted

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

	// deleteResult holds per-record metadata for the JSON response and post-delete verification.
	// WHY a struct (not just []string of IDs): the response must include name and type so the
	// caller can confirm exactly which records were removed without a follow-up GET.
	type deleteResult struct {
		Name string           `json:"name"`
		Type model.RecordType `json:"type"`
		ID   string           `json:"id"` // HE numeric row ID from the <tr> element
	}

		start := time.Now()
		var deleted []deleteResult

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "delete_record_by_name", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)

					// WHY NavigateToZoneList before GetZoneName:
					//   GetZoneName reads img[alt="delete"][value="{zoneID}"] which only
					//   exists on the zone LIST page (https://dns.he.net/), not on the
					//   zone EDIT page. Calling GetZoneName after NavigateToZone (edit page)
					//   always times out — the element is simply not present there.
					//   Fix: navigate to zone list, read the name, then navigate to zone edit.
					//
					// PREVIOUSLY TRIED (both failed):
					//   1. ListRecords → GetZoneName: timed out (edit page, element absent)
					//   2. NavigateToZone → GetZoneName immediately: also timed out (same reason)
					if err := zl.NavigateToZoneList(); err != nil {
						return err
					}
					zoneName, err := zl.GetZoneName(zoneID)
					if err != nil {
						return err
					}

					if err := zl.NavigateToZone(zoneID); err != nil {
						return err
					}
					rows, err := zl.GetRecordRows()
					if err != nil {
						return err
					}

					// Collect matching records before deleting to avoid mutating the
					// slice while iterating and to allow logging the full deleted set.
					//
					// WHY deleteTarget carries rowID separately from rec.ID:
					//   ParseRecordRow reads td[1] which is a HIDDEN cell — Playwright's
					//   InnerText() returns "" for hidden elements, so rec.ID is always "".
					//   The correct record ID for rf.DeleteRecord comes from row.ID (the <tr>
					//   element's id HTML attribute, read by GetRecordRows via GetAttribute).
					//   Passing rec.ID ("") to deleteRecord JS causes it to silently do nothing.
					type deleteTarget struct {
						rowID   string
						name    string
						recType model.RecordType
					}
					var toDelete []deleteTarget
					for _, row := range rows {
						if row.IsLocked {
							continue
						}
						rec, err := zl.ParseRecordRow(row.ID)
						if err != nil {
							slog.WarnContext(r.Context(), "skip record row parse error",
								"row_id", row.ID, "error", err)
							continue
						}
						if !strings.EqualFold(rec.Name, filterName) {
							continue
						}
						if filterType != "" && rec.Type != filterType {
							continue
						}
						toDelete = append(toDelete, deleteTarget{rowID: row.ID, name: rec.Name, recType: rec.Type})
					}

					rf := pages.NewRecordFormPage(page)
					for _, t := range toDelete {
						if err := rf.DeleteRecord(t.rowID, zoneName, string(t.recType)); err != nil {
							return err
						}
						deleted = append(deleted, deleteResult{Name: t.name, Type: t.recType, ID: t.rowID})
					}

					// Post-delete verification: reload the page and confirm none of the deleted
					// row IDs are still present in the DOM.
					//
					// WHY verify after delete (not trust DeleteRecord return value):
					//   DeleteRecord bypasses the confirmation dialog via direct form submit.
					//   Page navigation errors after submit are intentionally ignored, so absence
					//   of error does not guarantee the row is gone. Re-reading GetRecordRows from
					//   the live page is the only reliable confirmation.
					//
					// WHY NavigateToZone again (not reuse current page state):
					//   After form submit the page may be mid-reload. A fresh NavigateToZone
					//   guarantees we read a fully rendered page.
					if len(deleted) > 0 {
						if err := zl.NavigateToZone(zoneID); err != nil {
							return fmt.Errorf("post-delete verification navigate failed: %w", err)
						}
						afterRows, err := zl.GetRecordRows()
						if err != nil {
							return fmt.Errorf("post-delete verification read failed: %w", err)
						}
						afterIDs := make(map[string]bool, len(afterRows))
						for _, r := range afterRows {
							afterIDs[r.ID] = true
						}
						for _, d := range deleted {
							if afterIDs[d.ID] {
								return fmt.Errorf("post-delete verification failed: row %s (%s %s) still present after deletion", d.ID, d.Type, d.Name)
							}
						}
					}

					return nil
				})
			})
		})

		// Audit one entry per deleted record (mirrors by-ID delete behaviour).
		// If no records matched, write a single no-op entry so the action is traceable.
		auditResult := "success"
		auditErrMsg := ""
		if err != nil {
			auditResult = "failure"
			auditErrMsg = err.Error()
		}
		if len(deleted) == 0 {
			if auditErr := audit.Write(r.Context(), db, audit.Entry{
				TokenID:   claims.ID,
				AccountID: claims.AccountID,
				Action:    "delete",
				Resource:  "record:by-name:" + filterName,
				Result:    auditResult,
				ErrorMsg:  auditErrMsg,
			}); auditErr != nil {
				slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
			}
		}
		for _, d := range deleted {
			if auditErr := audit.Write(r.Context(), db, audit.Entry{
				TokenID:   claims.ID,
				AccountID: claims.AccountID,
				Action:    "delete",
				Resource:  "record:" + d.ID,
				Result:    auditResult,
				ErrorMsg:  auditErrMsg,
			}); auditErr != nil {
				slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
			}
		}

		if err != nil {
			handleBrowserError(w, r, err)
			return
		}

		slog.InfoContext(r.Context(), "delete record by name done",
			"account_id", claims.AccountID,
			"zone_id", zoneID,
			"name", filterName,
			"type_filter", string(filterType),
			"deleted_count", len(deleted),
			"duration_ms", time.Since(start).Milliseconds(),
		)

		// WHY 200 + JSON body (not 204 No Content):
		//   204 gives the caller no way to confirm what was deleted. Returning the list
		//   of deleted records (name, type, id) lets callers verify the right records
		//   were removed and is consistent with the post-delete verification already
		//   done server-side. Zero-match deletes still return 200 with deleted_count:0
		//   (idempotent — the resource was already absent).
		response.WriteJSON(w, http.StatusOK, map[string]any{
			"deleted_count": len(deleted),
			"deleted":       deleted,
		})
	}
}

// DeleteRecord handles DELETE /api/v1/zones/{zoneID}/records/{recordID}.
// Idempotent: always returns 204, whether or not the record existed.
// Browser operations are wrapped with circuit breaker + retry (RES-02, RES-03).
func DeleteRecord(db *sql.DB, sm *browser.SessionManager, breakers *resilience.BreakerRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		zoneID := chi.URLParam(r, "zoneID")
		recordID := chi.URLParam(r, "recordID")

		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "missing_claims", "Authentication required")
			return
		}

		start := time.Now()

		err := breakers.Execute(r.Context(), claims.AccountID, func() error {
			return resilience.WithRetry(r.Context(), func(ctx context.Context) error {
				return sm.WithAccount(ctx, claims.AccountID, "delete_record", func(page playwright.Page) error {
					zl := pages.NewZoneListPage(page)

					// WHY NavigateToZoneList before GetZoneName:
					//   GetZoneName reads img[alt="delete"][value="{zoneID}"] which only
					//   exists on the zone LIST page, not on the zone EDIT page.
					//   Must get the zone name first, then navigate to the edit page.
					if err := zl.NavigateToZoneList(); err != nil {
						return err
					}
					zoneName, err := zl.GetZoneName(zoneID)
					if err != nil {
						return err
					}

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

					rec, parseErr := zl.ParseRecordRow(recordID)
					if parseErr != nil {
						return parseErr
					}

					rf := pages.NewRecordFormPage(page)
					return rf.DeleteRecord(recordID, zoneName, string(rec.Type))
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
			Resource:  "record:" + recordID,
			Result:    auditResult,
			ErrorMsg:  auditErrMsg,
		}); auditErr != nil {
			slog.ErrorContext(r.Context(), "audit log write failed", "error", auditErr)
		}

		if err != nil {
			handleBrowserError(w, r, err)
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

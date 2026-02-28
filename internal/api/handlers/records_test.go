package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestListRecords_NilClaims verifies that ListRecords returns 401 when no auth
// claims are present in the request context.
func TestListRecords_NilClaims(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/zones/12345/records", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := ListRecords(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

// TestCreateRecord_MissingBody verifies that CreateRecord returns 400 when the
// request body is empty (not valid JSON).
func TestCreateRecord_MissingBody(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones/12345/records", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := CreateRecord(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

// TestCreateRecord_UnsupportedType verifies that CreateRecord returns 422 when
// the record type is not in the v1 supported set (e.g., NAPTR).
func TestCreateRecord_UnsupportedType(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")

	body := bytes.NewBufferString(`{"type":"NAPTR","name":"x","content":"y","ttl":300}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones/12345/records", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := CreateRecord(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 Unprocessable Entity, got %d", rr.Code)
	}
}

// TestCreateRecord_MissingName verifies that CreateRecord returns 400 when the
// record name is empty, even for a valid type.
func TestCreateRecord_MissingName(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")

	body := bytes.NewBufferString(`{"type":"A","name":"","content":"1.2.3.4","ttl":300}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones/12345/records", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := CreateRecord(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

// TestCreateRecord_MXMissingPriority verifies that CreateRecord returns 400 when
// an MX record is submitted with priority = 0.
func TestCreateRecord_MXMissingPriority(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")

	body := bytes.NewBufferString(`{"type":"MX","name":"example.com.","content":"mail.example.com.","ttl":300,"priority":0}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones/12345/records", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := CreateRecord(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

// TestCreateRecord_SRVMissingPort verifies that CreateRecord returns 400 when
// an SRV record is submitted with port = 0.
func TestCreateRecord_SRVMissingPort(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")

	body := bytes.NewBufferString(`{"type":"SRV","name":"_sip._tcp.example.com.","ttl":300,"priority":10,"weight":20,"port":0,"target":"sip.example.com."}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones/12345/records", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := CreateRecord(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

// TestGetRecord_NilClaims verifies that GetRecord returns 401 when no auth
// claims are present in the request context.
func TestGetRecord_NilClaims(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")
	chiCtx.URLParams.Add("recordID", "99999")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/zones/12345/records/99999", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := GetRecord(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

// TestDeleteRecord_NilClaims verifies that DeleteRecord returns 401 when no auth
// claims are present in the request context.
func TestDeleteRecord_NilClaims(t *testing.T) {
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "12345")
	chiCtx.URLParams.Add("recordID", "99999")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/zones/12345/records/99999", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler := DeleteRecord(nil, nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

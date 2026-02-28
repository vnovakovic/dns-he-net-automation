package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestListZones_NilClaims verifies that ListZones returns 401 when no auth claims
// are present in the request context.
func TestListZones_NilClaims(t *testing.T) {
	handler := ListZones(nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/zones", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

// TestCreateZone_MissingBody verifies that CreateZone returns 400 when the
// request body is empty (not valid JSON).
func TestCreateZone_MissingBody(t *testing.T) {
	handler := CreateZone(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

// TestCreateZone_EmptyName verifies that CreateZone returns 400 when the
// request body contains an empty zone name.
func TestCreateZone_EmptyName(t *testing.T) {
	handler := CreateZone(nil, nil)

	body := bytes.NewBufferString(`{"name":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

// TestCreateZone_NameTooLong verifies that CreateZone returns 400 when the
// zone name exceeds 253 characters.
func TestCreateZone_NameTooLong(t *testing.T) {
	handler := CreateZone(nil, nil)

	longName := strings.Repeat("a", 254)
	body := bytes.NewBufferString(`{"name":"` + longName + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/zones", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

// TestDeleteZone_EmptyID verifies that DeleteZone returns 400 when the
// zoneID URL parameter is empty.
func TestDeleteZone_EmptyID(t *testing.T) {
	handler := DeleteZone(nil, nil)

	// Build a chi router context with an empty zoneID parameter.
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("zoneID", "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/zones/", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

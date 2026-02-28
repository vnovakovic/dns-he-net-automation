// Package api provides the chi HTTP router for the dns-he-net-automation API.
package api

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/handlers"
	"github.com/vnovakov/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/response"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
	"github.com/vnovakov/dns-he-net-automation/internal/resilience"
)

// NewRouter builds and returns the chi HTTP router with all middleware and route registrations.
//
// Route structure:
//   - GET /healthz — unauthenticated health check (OPS-01)
//   - /api/v1/* — all require JWT bearer authentication (BearerAuth middleware)
//   - POST/DELETE mutations additionally require admin role (RequireAdmin middleware)
//
// Middleware order:
//   - GlobalRateLimit applied before BearerAuth (DDoS protection layer — research Pitfall 4)
//   - PerTokenRateLimit applied after BearerAuth (needs token identity from auth)
func NewRouter(db *sql.DB, sm *browser.SessionManager, launcher *browser.Launcher,
	secret []byte, breakers *resilience.BreakerRegistry,
	globalRPM, perTokenRPM int,
	vaultHealthFn func() string) http.Handler {
	r := chi.NewRouter()

	// Global rate limit — registered first, before auth, for DDoS protection (RES-03).
	// Limits total requests per minute across all clients (research Pitfall 4).
	r.Use(middleware.GlobalRateLimit(globalRPM))

	// Global middleware — applied to all routes.
	r.Use(chiMiddleware.RequestID) // X-Request-Id header; accessible via GetReqID(ctx)
	r.Use(chiMiddleware.RealIP)   // Populates RemoteAddr from X-Real-IP / X-Forwarded-For

	// Custom panic recovery middleware: returns JSON error response instead of chi's plain text 500.
	// This replaces chiMiddleware.Recoverer to satisfy the JSON error contract (API-04).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					slog.ErrorContext(r.Context(), "panic recovered",
						"request_id", chiMiddleware.GetReqID(r.Context()),
						"panic", fmt.Sprintf("%v", rec),
					)
					response.WriteError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
				}
			}()
			next.ServeHTTP(w, r)
		})
	})

	// NOTE: Do NOT use chiMiddleware.Logger here — it uses log.Printf, not slog (research anti-pattern).
	// Structured request logging is handled in the BearerAuth middleware via slog.InfoContext.

	// Custom 404 and 405 handlers so unmatched routes follow the JSON error contract (API-04).
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		response.WriteError(w, http.StatusNotFound, "not_found", "The requested resource does not exist")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		response.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "HTTP method not allowed for this endpoint")
	})

	// Health check — no authentication required (OPS-01).
	// vaultHealthFn returns "ok", "degraded: <reason>", or "disabled" (VAULT-04).
	r.Get("/healthz", handlers.HealthHandler(db, launcher, vaultHealthFn))

	// All /api/v1/* routes require authentication.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.BearerAuth(db, secret))
		// Per-token rate limit — registered after BearerAuth (needs token identity).
		// Limits requests per unique bearer token per minute (RES-03).
		r.Use(middleware.PerTokenRateLimit(perTokenRPM))

		r.Route("/accounts", func(r chi.Router) {
			r.Get("/", handlers.ListAccounts(db))
			r.With(middleware.RequireAdmin).Post("/", handlers.CreateAccount(db))

			r.Route("/{accountID}", func(r chi.Router) {
				r.Get("/", handlers.GetAccount(db))
				r.With(middleware.RequireAdmin).Delete("/", handlers.DeleteAccount(db, sm))

				r.Route("/tokens", func(r chi.Router) {
					r.Get("/", handlers.ListTokens(db))
					r.With(middleware.RequireAdmin).Post("/", handlers.IssueToken(db, secret))
					r.With(middleware.RequireAdmin).Delete("/{tokenID}", handlers.RevokeToken(db))
				})
			})
		})

		r.Route("/zones", func(r chi.Router) {
			r.Get("/", handlers.ListZones(db, sm, breakers))
			r.With(middleware.RequireAdmin).Post("/", handlers.CreateZone(db, sm, breakers))
			r.With(middleware.RequireAdmin).Delete("/{zoneID}", handlers.DeleteZone(db, sm, breakers))

			r.Route("/{zoneID}/records", func(r chi.Router) {
				r.Get("/", handlers.ListRecords(db, sm, breakers))
				r.With(middleware.RequireAdmin).Post("/", handlers.CreateRecord(db, sm, breakers))
				r.Route("/{recordID}", func(r chi.Router) {
					r.Get("/", handlers.GetRecord(db, sm, breakers))
					r.With(middleware.RequireAdmin).Put("/", handlers.UpdateRecord(db, sm, breakers))
					r.With(middleware.RequireAdmin).Delete("/", handlers.DeleteRecord(db, sm, breakers))
				})
			})
		})
	})

	return r
}

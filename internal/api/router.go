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
)

// NewRouter builds and returns the chi HTTP router with all middleware and route registrations.
//
// Route structure:
//   - GET /healthz — unauthenticated health check (OPS-01)
//   - /api/v1/* — all require JWT bearer authentication (BearerAuth middleware)
//   - POST/DELETE mutations additionally require admin role (RequireAdmin middleware)
func NewRouter(db *sql.DB, sm *browser.SessionManager, launcher *browser.Launcher, secret []byte) http.Handler {
	r := chi.NewRouter()

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
	r.Get("/healthz", handlers.HealthHandler(db, launcher))

	// All /api/v1/* routes require authentication.
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.BearerAuth(db, secret))

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
	})

	return r
}

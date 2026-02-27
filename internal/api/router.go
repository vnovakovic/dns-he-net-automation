// Package api provides the chi HTTP router for the dns-he-net-automation API.
package api

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/api/handlers"
	"github.com/vnovakov/dns-he-net-automation/internal/api/middleware"
	"github.com/vnovakov/dns-he-net-automation/internal/browser"
)

// NewRouter builds and returns the chi HTTP router with all middleware and route registrations.
//
// Route structure:
//   - /api/v1/* — all require JWT bearer authentication (BearerAuth middleware)
//   - POST/DELETE mutations additionally require admin role (RequireAdmin middleware)
//
// The /healthz route placeholder is commented out — it will be wired in plan 02-03
// once the health handler exists.
func NewRouter(db *sql.DB, sm *browser.SessionManager, secret []byte) http.Handler {
	r := chi.NewRouter()

	// Global middleware — applied to all routes.
	r.Use(chiMiddleware.RequestID)  // X-Request-Id header; accessible via GetReqID(ctx)
	r.Use(chiMiddleware.RealIP)     // Populates RemoteAddr from X-Real-IP / X-Forwarded-For
	r.Use(chiMiddleware.Recoverer)  // Recover panics, return 500 (prevents server crash)

	// NOTE: Do NOT use chiMiddleware.Logger here — it uses log.Printf, not slog (research anti-pattern).
	// Structured request logging is handled in the BearerAuth middleware via slog.InfoContext.

	// /healthz placeholder — uncomment when handlers.HealthHandler is implemented in plan 02-03.
	// r.Get("/healthz", handlers.HealthHandler(db, sm))

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
